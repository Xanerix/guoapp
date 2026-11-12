package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func mediaRequestHeaders(request *http.Request, referer string) {
	request.Header.Set("User-Agent", UserAgent)
	request.Header.Set("Referer", referer)
	request.Header.Set("Accept", "*/*")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	request.Header.Set("Sec-Fetch-Mode", "cors")
	request.Header.Set("Sec-Fetch-Dest", "empty")
	if origin, err := url.Parse(referer); err == nil && origin.Host != "" {
		request.Header.Set("Origin", origin.Scheme+"://"+origin.Host)
	}
}

func (d *Downloader) doMediaRequest(request *http.Request) (*http.Response, error) {
	return d.doMediaRequestWithClient(request, d.client)
}

func (d *Downloader) doMediaRequestWithClient(request *http.Request, client *http.Client) (*http.Response, error) {
	mediaRequestHeaders(request, request.Header.Get("Referer"))
	if credentials, _ := request.Context().Value(MediaCredentialsKey{}).(*MediaCredentials); credentials != nil {
		if err := credentials.apply(request); err != nil {
			return nil, err
		}
		client = credentials.client(client)
	}
	authorizer := d.mediaAuthorizer(request.URL)
	authorization := ""
	for attempt := 0; attempt < 2; attempt++ {
		if authorizer != nil {
			header, value, err := authorizer.MediaAuthorization(request.Context(), request.URL, request.Header.Get("Referer"))
			if err != nil {
				return nil, fmt.Errorf("获取播放凭证失败: %w", err)
			}
			request.Header.Set(header, value)
			authorization = header
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		if authorizer != nil && attempt == 0 && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
			response.Body.Close()
			if readErr == nil && catalogResponseBlockReason(response, body) == "" {
				authorizer.RejectMediaAuthorization(request.URL, request.Header.Get(authorization))
				continue
			}
			response.Body = io.NopCloser(bytes.NewReader(body))
		}
		if d.limiter != nil {
			d.limiter.observeMedia(request, response)
		}
		return response, nil
	}
	return nil, errors.New("播放凭证刷新失败")
}

func (d *Downloader) fetchMediaPlaylist(ctx context.Context, address, referer string) (string, string, error) {
	if !IsProviderHTTPMediaURL(address) {
		return "", "", errors.New("播放列表地址无效")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Referer", referer)
	response, err := d.doMediaRequest(request)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if err != nil || len(body) > 4<<20 {
		return "", "", errors.New("播放列表过大或读取失败")
	}
	if response.StatusCode != http.StatusOK || catalogResponseBlockReason(response, body) != "" {
		return "", "", d.catalogResponseError(request, response, body)
	}
	text := strings.TrimSpace(strings.TrimPrefix(string(body), "\ufeff"))
	if !strings.HasPrefix(text, "#EXTM3U") {
		return "", "", &PlaylistError{Body: body}
	}
	finalURL := address
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}
	return text, finalURL, nil
}
