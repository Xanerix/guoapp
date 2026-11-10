package core

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
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

	AcceptJSONStatus []int
	FollowRedirects  bool
	AnyStatus        bool
	Direct           bool
	Ticket           uint64
}

type HTTPResponse struct {
	Status    int
	Header    map[string][]string
	Body      []byte
	Blocked   bool
	Oversized bool
}

type PlaylistError struct{ Body []byte }

func (*PlaylistError) Error() string {
	return "站源未返回有效的播放列表，链接可能已失效"
}

type Host interface {
	Reserve(ctx context.Context, address, lane string) (ticket uint64, err error)
	Retries() int
	MaxPages() int
	Fetch(ctx context.Context, request HTTPRequest) (HTTPResponse, error)
	BaseURL(source string) string
	PreferredSite(source string) string
	RememberSite(source, site string)
	FetchText(ctx context.Context, request TextRequest) (TextResponse, error)
	FetchPlaylist(ctx context.Context, address, referer string, credentials *MediaCredentials) (playlist, finalURL string, err error)
	LoadState(name string) ([]byte, error)
	SaveState(name string, data []byte) error
	PrepareWebMedia(ctx context.Context, media ProviderMedia, sourceName string) (ProviderMedia, error)
	FinishMedia(ctx context.Context, media ProviderMedia, source string) (ProviderMedia, error)
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

func (d *Downloader) FinishMedia(ctx context.Context, media ProviderMedia, source string) (ProviderMedia, error) {
	return d.finishProviderMedia(ctx, media, source)
}

func (d *Downloader) FetchPlaylist(ctx context.Context, address, referer string, credentials *MediaCredentials) (string, string, error) {
	if credentials != nil {
		ctx = providerMediaContext(ctx, credentials)
	}
	return d.fetchMediaPlaylist(ctx, address, referer)
}

func (d *Downloader) Fetch(ctx context.Context, input HTTPRequest) (HTTPResponse, error) {
	limit, timeout := input.Limit, input.Timeout
	if limit <= 0 {
		limit = 4 << 20
	}
	if background, _ := ctx.Value(BackgroundCatalogKey{}).(bool); background && (timeout <= 0 || timeout > 8*time.Second) {
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
	if input.Ticket != 0 {
		release, reserved := d.takeReservation(input.Ticket)
		if !reserved {
			return HTTPResponse{}, errors.New("站源请求名额已失效")
		}
		defer release()
	} else if d.limiter != nil {
		release, err := d.limiter.acquire(ctx, request)
		if err != nil {
			return HTTPResponse{}, err
		}
		defer release()
	}
	base := d.client
	if input.Direct {
		base = d.directClient()
	}
	transport := *base
	if !input.FollowRedirects {
		origin := ProviderMediaOrigin(request.URL)
		transport.Jar = nil
		previousRedirect := transport.CheckRedirect
		transport.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 || ProviderMediaOrigin(next.URL) != origin {
				return fmt.Errorf("%s接口重定向地址异常", input.Label)
			}
			if previousRedirect != nil {
				return previousRedirect(next, via)
			}
			return nil
		}
	}
	response, err := transport.Do(request)
	if err != nil {
		return HTTPResponse{}, fmt.Errorf("%s连接失败：%w", input.Label, PublicError(err))
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	oversized := readErr != nil || int64(len(data)) > limit
	blocked := catalogResponseBlockReason(response, data) != ""
	result := HTTPResponse{Status: response.StatusCode, Header: response.Header.Clone(), Blocked: blocked}
	if !oversized {
		result.Body = data
	}
	if slices.Contains(input.AcceptJSONStatus, response.StatusCode) && !oversized && !blocked && json.Valid(data) {
		return result, nil
	}
	if d.limiter != nil {
		d.limiter.observe(request, response)
	}
	if !input.AnyStatus && (response.StatusCode < 200 || response.StatusCode >= 300 || blocked) {
		return result, d.catalogResponseError(request, response, data)
	}
	if oversized {
		return HTTPResponse{Oversized: true}, fmt.Errorf("%s返回的数据过大或读取失败", input.Label)
	}
	return result, nil
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
	if d.cfg.dataDir == "" {
		return nil, nil
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
	if d.cfg.dataDir == "" {
		return errors.New("应用数据目录无效")
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

func (d *Downloader) directClient() *http.Client {
	d.directOnce.Do(func() {
		if d.client != nil && d.client.Transport != nil {
			if _, production := d.client.Transport.(*huangguoBrowserTransport); !production {
				d.direct = &http.Client{Transport: d.client.Transport}
				return
			}
		}
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		if d.cfg.InsecureTLS {
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
		if d.proxyRouter != nil {
			transport.Proxy = d.proxyRouter.proxy
		}
		d.direct = &http.Client{
			Transport: transport,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return errors.New("站源接口重定向次数过多")
				}
				return nil
			},
		}
	})
	return d.direct
}

const ProviderMaxBodyBytes = 20 * 1024 * 1024

const ProviderTimeout = 12 * time.Second

const reservationLifetime = time.Minute

func (d *Downloader) Retries() int { return d.cfg.Retries }

func (d *Downloader) MaxPages() int { return d.cfg.MaxPagesPerSort }

func (d *Downloader) Reserve(ctx context.Context, address, lane string) (uint64, error) {
	release := func() {}
	if lane != "" {
		ctx = context.WithValue(ctx, requestLaneKey{}, lane)
	}
	if d.limiter != nil {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return 0, err
		}
		if release, err = d.limiter.acquire(ctx, request); err != nil {
			return 0, err
		}
	}
	d.reserveMu.Lock()
	d.reserveNext++
	ticket := d.reserveNext
	if d.reservations == nil {
		d.reservations = map[uint64]func(){}
	}
	d.reservations[ticket] = release
	d.reserveMu.Unlock()
	time.AfterFunc(reservationLifetime, func() {
		if release, reserved := d.takeReservation(ticket); reserved {
			release()
		}
	})
	return ticket, nil
}

func (d *Downloader) takeReservation(ticket uint64) (func(), bool) {
	d.reserveMu.Lock()
	defer d.reserveMu.Unlock()
	release, reserved := d.reservations[ticket]
	delete(d.reservations, ticket)
	return release, reserved
}

func (d *Downloader) PreferredSite(source string) string {
	d.providerMu.Lock()
	defer d.providerMu.Unlock()
	return d.providerHosts[source]
}

func (d *Downloader) RememberSite(source, site string) {
	d.providerMu.Lock()
	defer d.providerMu.Unlock()
	if d.providerHosts == nil {
		d.providerHosts = map[string]string{}
	}
	d.providerHosts[source] = site
}

func (d *Downloader) BaseURL(source string) string { return d.providerBaseURL(source) }
