package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type TextRequest struct {
	URL       string
	Referer   string
	UserAgent string
	Cookie    string
	NoCache   bool
	Body      []byte
}

type TextResponse struct {
	Body string
	URL  string
}

type HTTPRequest struct {
	Label   string
	Method  string
	URL     string
	Header  map[string]string
	Body    []byte
	Limit   int64
	Timeout time.Duration
}

type HTTPResponse struct {
	Status int
	Body   []byte
}

type Host interface {
	Fetch(ctx context.Context, request HTTPRequest) (HTTPResponse, error)
	FetchText(ctx context.Context, request TextRequest) (TextResponse, error)
	FetchPlaylist(ctx context.Context, address, referer string) (playlist, finalURL string, err error)
	LoadState(name string) ([]byte, error)
	SaveState(name string, data []byte) error
	PrepareWebMedia(ctx context.Context, media ProviderMedia, sourceName string) (ProviderMedia, error)
}

func (d *Downloader) FetchText(ctx context.Context, request TextRequest) (TextResponse, error) {
	responses := &playbackResponseURLs{}
	ctx = context.WithValue(ctx, playbackResponseURLsKey{}, responses)
	if request.UserAgent != "" {
		ctx = context.WithValue(ctx, providerTextUserAgentKey{}, request.UserAgent)
	}
	if request.Cookie != "" {
		ctx = context.WithValue(ctx, providerTextCookieKey{}, request.Cookie)
	}
	if request.NoCache {
		ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	}
	body, err := d.fetchProviderRequest(ctx, request.URL, request.Referer, request.Body)
	if err != nil {
		return TextResponse{}, err
	}
	address := request.URL
	if actual, found := responses.values.Load(request.URL); found {
		address = actual.(string)
	}
	return TextResponse{Body: body, URL: address}, nil
}

func (d *Downloader) PrepareWebMedia(ctx context.Context, media ProviderMedia, sourceName string) (ProviderMedia, error) {
	return d.prepareWebProviderMedia(ctx, media, sourceName)
}

func (d *Downloader) FetchPlaylist(ctx context.Context, address, referer string) (string, string, error) {
	return d.fetchMediaPlaylist(ctx, address, referer)
}

func (d *Downloader) Fetch(ctx context.Context, input HTTPRequest) (HTTPResponse, error) {
	limit, timeout := input.Limit, input.Timeout
	if limit <= 0 {
		limit = 4 << 20
	}
	if background, _ := ctx.Value(backgroundCatalogKey{}).(bool); background && (timeout <= 0 || timeout > 8*time.Second) {
		timeout = 8 * time.Second
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var payload io.Reader
	if input.Body != nil {
		payload = bytes.NewReader(input.Body)
	}
	request, err := http.NewRequestWithContext(ctx, input.Method, input.URL, payload)
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("无法创建%s请求", input.Label)
	}
	for name, value := range input.Header {
		request.Header.Set(name, value)
	}
	if d.limiter != nil {
		release, err := d.limiter.acquire(ctx, request)
		if err != nil {
			return HTTPResponse{}, err
		}
		defer release()
	}
	origin := providerMediaOrigin(request.URL)
	transport := *d.client
	transport.Jar = nil
	previousRedirect := transport.CheckRedirect
	transport.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || providerMediaOrigin(next.URL) != origin {
			return fmt.Errorf("%s接口重定向地址异常", input.Label)
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		return nil
	}
	response, err := transport.Do(request)
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("%s连接失败：%w", input.Label, publicError(err))
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if d.limiter != nil {
		d.limiter.observe(request, response)
	}
	oversized := readErr != nil || int64(len(data)) > limit
	if response.StatusCode < 200 || response.StatusCode >= 300 || catalogResponseBlockReason(response, data) != "" {
		result := HTTPResponse{Status: response.StatusCode}
		if !oversized {
			result.Body = data
		}
		return result, d.catalogResponseError(request, response, data)
	}
	if oversized {
		return HTTPResponse{}, fmt.Errorf("%s返回的数据过大或读取失败", input.Label)
	}
	return HTTPResponse{Status: response.StatusCode, Body: data}, nil
}

func (d *Downloader) statePath(name string) (string, error) {
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) {
		return "", errors.New("站源状态名无效")
	}
	return filepath.Join(d.cfg.dataDir, name), nil
}

func (d *Downloader) LoadState(name string) ([]byte, error) {
	path, err := d.statePath(name)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, 1<<20))
}

func (d *Downloader) SaveState(name string, data []byte) error {
	path, err := d.statePath(name)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(d.cfg.dataDir, 0o700); err != nil {
		return err
	}
	return nativeDownloadWrite(path, data)
}
