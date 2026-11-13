package core

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func (stream *nativeStreamServer) cencRange(ctx context.Context, session *nativeStreamSession, address string, start, end int64) (*http.Response, int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("User-Agent", UserAgent)
	request.Header.Set("Referer", session.referer)
	request.Header.Set("Range", "bytes="+strconv.FormatInt(start, 10)+"-"+strconv.FormatInt(end, 10))
	response, err := stream.nativeRequest(request)
	if err != nil {
		return nil, 0, err
	}
	_, size, found := strings.Cut(response.Header.Get("Content-Range"), "/")
	total, err := strconv.ParseInt(size, 10, 64)
	if response.StatusCode != http.StatusPartialContent || !found || err != nil {
		_ = response.Body.Close()
		return nil, 0, fmt.Errorf("视频服务器不支持按位置读取（HTTP %d）", response.StatusCode)
	}
	return response, total, nil
}

func (stream *nativeStreamServer) cencBytes(ctx context.Context, session *nativeStreamSession, address string, start, end int64) ([]byte, int64, error) {
	response, total, err := stream.cencRange(ctx, session, address, start, end)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, end-start+1))
	return data, total, err
}

func (stream *nativeStreamServer) cencLayout(ctx context.Context, session *nativeStreamSession, address string) (*cencLayout, error) {
	session.cencMu.Lock()
	defer session.cencMu.Unlock()
	if session.cenc != nil {
		return session.cenc, nil
	}
	const window = 1 << 20
	position, origin := int64(0), int64(0)
	data, total, err := stream.cencBytes(ctx, session, address, 0, window-1)
	if err != nil {
		return nil, err
	}
	for range 64 {
		if position+16 > origin+int64(len(data)) {
			if position+16 > total {
				break
			}
			origin = position
			if data, _, err = stream.cencBytes(ctx, session, address, origin, origin+window-1); err != nil {
				return nil, err
			}
		}
		if position+16 > origin+int64(len(data)) {
			break
		}
		head := data[position-origin:]
		size := int64(binary.BigEndian.Uint32(head))
		if size == 1 {
			size = int64(binary.BigEndian.Uint64(head[8:]))
		} else if size == 0 {
			size = total - position
		}
		if size < 8 || size > total-position {
			break
		}
		if string(head[4:8]) != "moov" {
			position += size
			continue
		}
		if size > 64<<20 {
			break
		}
		if position+size > origin+int64(len(data)) {
			origin = position
			if data, _, err = stream.cencBytes(ctx, session, address, position, position+size-1); err != nil {
				return nil, err
			}
		}
		if position+size > origin+int64(len(data)) {
			break
		}
		moov := append([]byte{}, data[position-origin:position-origin+size]...)
		layout, err := newCENCLayout(moov, position, total, session.cencKey)
		if err != nil {
			return nil, err
		}
		session.cenc = layout
		return layout, nil
	}
	return nil, errCENCLayout
}

func (stream *nativeStreamServer) nativeDecrypt(ctx context.Context, token string, media ProviderMedia) error {
	stream.mu.Lock()
	session := stream.sessions[token]
	stream.mu.Unlock()
	if session == nil {
		return errors.New("播放已结束")
	}
	session.cencKey = media.CENCKey
	_, err := stream.cencLayout(providerMediaContext(ctx, session.credentials), session, media.URL)
	return err
}

func cencRequestRange(header string, total int64) (int64, int64, bool) {
	if header == "" {
		return 0, total - 1, true
	}
	value, found := strings.CutPrefix(header, "bytes=")
	first, last, dashed := strings.Cut(value, "-")
	if !found || !dashed || strings.Contains(value, ",") {
		return 0, 0, false
	}
	if first == "" {
		suffix, err := strconv.ParseInt(last, 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, false
		}
		return max(0, total-suffix), total - 1, true
	}
	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil || start < 0 || start >= total {
		return 0, 0, false
	}
	end := total - 1
	if last != "" {
		if end, err = strconv.ParseInt(last, 10, 64); err != nil || end < start {
			return 0, 0, false
		}
		end = min(end, total-1)
	}
	return start, end, true
}

func (stream *nativeStreamServer) nativeServeDecrypted(ctx context.Context, writer http.ResponseWriter, request *http.Request, session *nativeStreamSession, asset nativeStreamAsset) {
	layout, err := stream.cencLayout(ctx, session, asset.address)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadGateway)
		return
	}
	start, end, valid := cencRequestRange(request.Header.Get("Range"), layout.total)
	writer.Header().Set("Accept-Ranges", "bytes")
	if !valid {
		writer.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(layout.total, 10))
		writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	status := http.StatusOK
	if request.Header.Get("Range") != "" {
		status = http.StatusPartialContent
	}
	respond := func() {
		writer.Header().Set("Content-Type", "video/mp4")
		writer.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		if status == http.StatusPartialContent {
			writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, layout.total))
		}
		writer.WriteHeader(status)
	}
	if request.Method == http.MethodHead {
		respond()
		return
	}
	response, _, err := stream.cencRange(ctx, session, asset.address, start, end)
	if err != nil {
		http.Error(writer, "读取媒体失败，请重试", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	respond()
	buffer := make([]byte, 64<<10)
	for offset := start; offset <= end; {
		size, err := io.ReadFull(response.Body, buffer[:min(int64(len(buffer)), end-offset+1)])
		if size > 0 {
			layout.apply(offset, buffer[:size])
			if _, writeErr := writer.Write(buffer[:size]); writeErr != nil {
				return
			}
			offset += int64(size)
		}
		if err != nil {
			return
		}
	}
}
