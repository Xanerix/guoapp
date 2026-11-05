package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	jinguoDuanjuBaseURL   = "https://jinguoduanju.app"
	jinguoDuanjuUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1"
)

var (
	errJinguoDuanjuAuthExpired = errors.New("禁果短剧访问授权已失效，请重试")
	jinguoDuanjuContentHosts   = []string{
		"jinguoduanju.app",
		"www.jinguoduanju.app",
		"play.jinguoduanju.app",
		"jgdj.app",
		"www.jgdj.app",
	}
)

type jinguoDuanjuAuthCall struct {
	done  chan struct{}
	token string
	err   error
}

type jinguoDuanjuAPIClient struct {
	downloader *Downloader
	mu         sync.Mutex
	base       string
	site       string
	deviceID   string
	token      string
	pending    *jinguoDuanjuAuthCall
	listings   map[string]jinguoDuanjuListing
}

type jinguoDuanjuListing struct {
	items   []Drama
	expires time.Time
}

func (client *jinguoDuanjuAPIClient) cachedListing(key string) ([]Drama, bool) {
	client.mu.Lock()
	defer client.mu.Unlock()
	listing, found := client.listings[key]
	if !found || time.Now().After(listing.expires) {
		return nil, false
	}
	return listing.items, true
}

func (client *jinguoDuanjuAPIClient) storeListing(key string, items []Drama) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.listings == nil || len(client.listings) >= 32 {
		client.listings = map[string]jinguoDuanjuListing{}
	}
	client.listings[key] = jinguoDuanjuListing{items: items, expires: time.Now().Add(10 * time.Minute)}
}

type jinguoDuanjuStatusError struct {
	message string
}

func (err *jinguoDuanjuStatusError) Error() string { return err.message }

func (d *Downloader) jinguoDuanjuClient() *jinguoDuanjuAPIClient {
	d.jinguoDuanjuOnce.Do(func() {
		d.jinguoDuanju = &jinguoDuanjuAPIClient{downloader: d}
	})
	return d.jinguoDuanju
}

func (client *jinguoDuanjuAPIClient) siteURL() string {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.site != "" {
		return client.site
	}
	if site := jinguoDuanjuContentSite(client.downloader.cfg.JinguoDuanjuURL); site != "" {
		return site
	}
	return jinguoDuanjuBaseURL
}

func jinguoDuanjuContentSite(raw string) string {
	address, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || address.Scheme != "https" || address.User != nil || address.RawQuery != "" || address.Fragment != "" || address.Hostname() == "" {
		return ""
	}
	if path := strings.Trim(address.EscapedPath(), "/"); path != "" {
		return ""
	}
	host := strings.ToLower(address.Hostname())
	for _, known := range jinguoDuanjuContentHosts {
		if host == known {
			return "https://" + host
		}
	}
	return ""
}

func jinguoDuanjuOrigin(apiBase string) string {
	parsed, err := url.Parse(apiBase)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func (client *jinguoDuanjuAPIClient) candidates() []string {
	client.mu.Lock()
	pinned := client.base
	client.mu.Unlock()
	var bases []string
	add := func(base string) {
		if base == "" || base == "/api/v1" {
			return
		}
		for _, existing := range bases {
			if existing == base {
				return
			}
		}
		bases = append(bases, base)
	}
	add(pinned)
	add(jinguoDuanjuContentSite(client.downloader.cfg.JinguoDuanjuURL) + "/api/v1")
	add(jinguoDuanjuBaseURL + "/api/v1")
	add("https://jgdj.app/api/v1")
	add("https://play.jinguoduanju.app/api/v1")
	return bases
}

func (client *jinguoDuanjuAPIClient) accessToken(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	client.mu.Lock()
	if client.token != "" {
		token := client.token
		client.mu.Unlock()
		return token, nil
	}
	if pending := client.pending; pending != nil {
		client.mu.Unlock()
		select {
		case <-pending.done:
			if (errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded)) && ctx.Err() == nil {
				return client.accessToken(ctx)
			}
			return pending.token, pending.err
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	if client.deviceID == "" {
		var identifier [16]byte
		if _, err := rand.Read(identifier[:]); err != nil {
			client.mu.Unlock()
			return "", errors.New("无法初始化禁果短剧访问会话")
		}
		identifier[6] = identifier[6]&0x0f | 0x40
		identifier[8] = identifier[8]&0x3f | 0x80
		client.deviceID = fmt.Sprintf("%x-%x-%x-%x-%x", identifier[0:4], identifier[4:6], identifier[6:8], identifier[8:10], identifier[10:])
	}
	deviceID := client.deviceID
	pending := &jinguoDuanjuAuthCall{done: make(chan struct{})}
	client.pending = pending
	client.mu.Unlock()

	body, _ := json.Marshal(map[string]string{"deviceId": "h5:" + deviceID, "channelCode": "", "attributionToken": ""})
	decoded, err := client.request(ctx, http.MethodPost, "/auth/device", nil, body, "", false, 64<<10)
	token := ""
	if err == nil {
		row, _ := decoded.(map[string]any)
		token = mapString(row, "token")
		if token == "" || len(token) > 8192 || strings.IndexFunc(token, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
			token = ""
			err = errors.New("禁果短剧未返回有效的访问授权")
		}
	}
	client.mu.Lock()
	if err == nil {
		client.token = token
	}
	pending.token, pending.err = token, err
	client.pending = nil
	close(pending.done)
	client.mu.Unlock()
	return token, err
}

func (client *jinguoDuanjuAPIClient) get(ctx context.Context, route string, query url.Values, limit int64) (any, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := client.accessToken(ctx)
		if err != nil {
			return nil, err
		}
		decoded, err := client.request(ctx, http.MethodGet, route, query, nil, token, attempt == 0, limit)
		if attempt == 0 && errors.Is(err, errJinguoDuanjuAuthExpired) {
			client.mu.Lock()
			if client.token == token {
				client.token = ""
			}
			client.mu.Unlock()
			continue
		}
		return decoded, err
	}
	return nil, errJinguoDuanjuAuthExpired
}

func (client *jinguoDuanjuAPIClient) request(ctx context.Context, method, route string, query url.Values, body []byte, token string, retryAuth bool, limit int64) (any, error) {
	var lastErr error
	for _, base := range client.candidates() {
		decoded, err := client.requestBase(ctx, base, method, route, query, body, token, retryAuth, limit)
		if err == nil || errors.Is(err, errJinguoDuanjuAuthExpired) || jinguoDuanjuStatus(err) {
			if err == nil {
				client.rememberBase(base)
			}
			return decoded, err
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("禁果短剧线路暂不可用，请稍后重试")
	}
	return nil, lastErr
}

func jinguoDuanjuStatus(err error) bool {
	var status *jinguoDuanjuStatusError
	return errors.As(err, &status)
}

func (client *jinguoDuanjuAPIClient) rememberBase(base string) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.base = base
	if origin := jinguoDuanjuOrigin(base); origin != "" {
		client.site = origin
	}
}

func (client *jinguoDuanjuAPIClient) requestBase(ctx context.Context, base, method, route string, query url.Values, body []byte, token string, retryAuth bool, limit int64) (any, error) {
	parsed, err := url.Parse(base)
	if err != nil || !isProviderHTTPMediaURL(base) || parsed.User != nil {
		return nil, errors.New("禁果短剧接口地址无效")
	}
	timeout := 15 * time.Second
	if background, _ := ctx.Value(backgroundCatalogKey{}).(bool); background {
		timeout = 8 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	address := strings.TrimRight(base, "/") + route
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("无法创建禁果短剧请求")
	}
	request.Header.Set("User-Agent", jinguoDuanjuUserAgent)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	origin := jinguoDuanjuOrigin(base)
	request.Header.Set("Referer", origin+"/")
	request.Header.Set("Origin", origin)
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	d := client.downloader
	if d.limiter != nil {
		release, err := d.limiter.acquire(ctx, request)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	transport := *d.client
	transport.Jar = nil
	previousRedirect := transport.CheckRedirect
	transport.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !strings.EqualFold(next.URL.Host, parsed.Host) {
			return errors.New("禁果短剧接口重定向地址异常")
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		return nil
	}
	response, err := transport.Do(request)
	if err != nil {
		return nil, fmt.Errorf("禁果短剧连接失败：%w", publicError(err))
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	blocked := catalogResponseBlockReason(response, data) != ""
	if retryAuth && token != "" && response.StatusCode == http.StatusUnauthorized && readErr == nil && int64(len(data)) <= limit && !blocked {
		return nil, errJinguoDuanjuAuthExpired
	}
	if d.limiter != nil {
		d.limiter.observe(request, response)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || blocked {
		if message := jinguoDuanjuErrorMessage(data); message != "" && !blocked {
			return nil, &jinguoDuanjuStatusError{message: "禁果短剧接口返回失败：" + message}
		}
		return nil, &jinguoDuanjuStatusError{message: d.catalogResponseError(request, response, data).Error()}
	}
	if readErr != nil || int64(len(data)) > limit {
		return nil, errors.New("禁果短剧返回的数据过大或读取失败")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if decoder.Decode(&decoded) != nil || decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("禁果短剧返回的数据格式无效")
	}
	return decoded, nil
}

func jinguoDuanjuErrorMessage(data []byte) string {
	var row map[string]any
	if json.Unmarshal(data, &row) != nil {
		return ""
	}
	message := mapString(row, "message", "msg", "tip")
	if message == "" || len([]rune(message)) > 180 || strings.ContainsAny(message, "\r\n") {
		return ""
	}
	return message
}
