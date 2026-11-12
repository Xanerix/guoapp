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
	"time"
)

type ProviderMedia struct {
	Credentials *MediaCredentials
	URL         string
	Referer     string
	Duration    time.Duration
	Playlist    string
	HLSKey      []byte
	CENCKey     []byte
	Quality     int
	Variants    []ProviderMedia
}

func (d *Downloader) providerBaseURL(source string) string {
	source = canonicalProviderSource(source)
	if descriptor, ok := registeredDescriptor(source); ok {
		if p, ok := d.provider(source); ok {
			if sited, ok := p.(Sited); ok {
				return strings.TrimRight(sited.BaseURL(), "/")
			}
		}
		return strings.TrimRight(descriptor.BaseURL, "/")
	}
	return "https://d2pypzndaqisk.cloudfront.net"
}

func ProviderSourceForURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if id, ok := registeredSource(host); ok {
		return id
	}
	return ""
}

func (d *Downloader) providerURLCandidates(raw string) []string {
	source := ProviderSourceForURL(raw)
	if descriptor, registered := registeredDescriptor(source); source == "" || registered && descriptor.NoHostRotation {
		return []string{raw}
	}
	parsed, _ := url.Parse(raw)
	d.providerMu.Lock()
	preferred := d.providerHosts[source]
	d.providerMu.Unlock()
	var candidates []string
	seen := map[string]bool{}
	add := func(candidate string) {
		if candidate != "" && !seen[candidate] {
			seen[candidate] = true
			candidates = append(candidates, candidate)
		}
	}
	add(RehostProviderURL(parsed, preferred))
	configured := d.providerBaseURL(source)
	add(RehostProviderURL(parsed, configured))
	if ProviderSourceForURL(configured) == "" {
		return candidates
	}
	rotated := []string{raw}
	if p, ok := d.provider(source); ok {
		if rotator, ok := p.(URLRotator); ok {
			rotated = rotator.URLCandidates(raw)
		}
	}
	for _, candidate := range rotated {
		add(candidate)
	}
	return candidates
}

func (d *Downloader) resolveProviderMedia(ctx context.Context, task Task) (ProviderMedia, error) {
	chapter := task.Chapter
	chapter.Source = canonicalProviderSource(chapter.Source)
	if chapter.Source == "" {
		chapter.Source = SourceFromDramaID(task.DramaID)
	}
	p, ok := d.provider(chapter.Source)
	if !ok {
		return ProviderMedia{}, fmt.Errorf("unsupported provider source: %s", chapter.Source)
	}
	return p.ResolveMedia(ctx, task)
}

func (d *Downloader) finishProviderMedia(ctx context.Context, media ProviderMedia, source string) (ProviderMedia, error) {
	if !IsProviderHTTPMediaURL(media.URL) {
		return ProviderMedia{}, fmt.Errorf("%s 未返回有效播放地址，请刷新章节或确认站点访问权限", source)
	}
	parsed, _ := url.Parse(media.URL)
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		playlist, finalURL, err := d.fetchMediaPlaylist(ctx, media.URL, media.Referer)
		if err != nil {
			return ProviderMedia{}, fmt.Errorf("获取播放列表失败: %w", err)
		}
		if !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(playlist, "\ufeff")), "#EXTM3U") {
			return ProviderMedia{}, fmt.Errorf("站点未返回有效 M3U8，可能需要登录或链接已失效")
		}
		if duration := M3u8Duration(playlist); duration > 0 {
			media.Duration = duration
		}
		media.Playlist = playlist
		media.URL = finalURL
	}
	return media, nil
}

func (d *Downloader) fetchProviderRequest(ctx context.Context, rawURL, referer string, payload []byte) (string, error) {
	retries := d.cfg.Retries
	if retries <= 0 {
		retries = 3
	}
	if referer == "" {
		referer = rawURL
	}
	candidates := d.providerURLCandidates(rawURL)
	var lastErr error
	tried := 0
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		attempts := retries
		if len(candidates) > 1 {
			attempts = 1
		}
		for attempt := 1; attempt <= attempts; attempt++ {
			tried++
			if attempt > 1 {
				select {
				case <-time.After(time.Duration(attempt) * time.Second):
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			timeout := ProviderTimeout
			if len(candidates) > 1 {
				timeout = 5 * time.Second
			}
			method, requestBody := http.MethodGet, io.Reader(nil)
			if payload != nil {
				method, requestBody = http.MethodPost, bytes.NewReader(payload)
			}
			req, err := http.NewRequestWithContext(ctx, method, candidate, requestBody)
			if err != nil {
				return "", err
			}
			if payload != nil {
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Accept", "application/json")
			}
			req.Header.Set("User-Agent", UserAgent)
			if agent, ok := ctx.Value(providerTextUserAgentKey{}).(string); ok && agent != "" {
				req.Header.Set("User-Agent", agent)
			}
			if ProviderSourceForURL(rawURL) != "" {
				req.Header.Set("Referer", providerRefererForURL(candidate, referer))
			} else {
				req.Header.Set("Referer", referer)
			}
			req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
			if cookie, ok := ctx.Value(providerTextCookieKey{}).(string); ok && cookie != "" {
				req.Header.Set("Cookie", cookie)
			}
			if noCache, _ := ctx.Value(providerTextNoCacheKey{}).(bool); noCache {
				req.Header.Set("Cache-Control", "no-cache")
			}
			resp, err := d.doCatalogRequestWithTimeout(req, timeout)
			if err != nil {
				lastErr = err
				var backoff *RequestBackoff
				if errors.As(err, &backoff) {
					break
				}
				continue
			}
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, ProviderMaxBodyBytes+1))
			closeErr := resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
				continue
			}
			if closeErr != nil {
				lastErr = closeErr
				continue
			}
			if len(body) > ProviderMaxBodyBytes {
				lastErr = fmt.Errorf("response exceeds %d bytes", ProviderMaxBodyBytes)
				continue
			}
			if resp.StatusCode < 200 || resp.StatusCode >= 300 || catalogResponseBlockReason(resp, body) != "" {
				lastErr = d.catalogResponseError(req, resp, body)
				if catalogResponseBlockReason(resp, body) != "" || resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 408 {
					break
				}
				continue
			}
			if source := ProviderSourceForURL(rawURL); source != "" {
				effectiveURL := req.URL
				if resp.Request != nil {
					effectiveURL = resp.Request.URL
				}
				d.providerMu.Lock()
				d.providerHosts[source] = effectiveURL.Scheme + "://" + effectiveURL.Host
				d.providerMu.Unlock()
			}
			rememberPlaybackResponseURL(ctx, rawURL, resp)
			return string(body), nil
		}
	}
	if len(candidates) > 1 && lastErr != nil {
		name := "站源"
		if descriptor, ok := registeredDescriptor(ProviderSourceForURL(rawURL)); ok && descriptor.Name != "" {
			name = descriptor.Name
		}
		return "", fmt.Errorf("%s请求失败：已尝试 %d 个域名，最后错误：%w", name, tried, lastErr)
	}
	return "", lastErr
}

func RehostProviderURL(u *url.URL, mirror string) string {
	mu, err := url.Parse(mirror)
	if err != nil || mu.Host == "" {
		return ""
	}
	copyURL := *u
	copyURL.Scheme = mu.Scheme
	copyURL.Host = mu.Host
	return copyURL.String()
}

func providerRefererForURL(candidate, fallback string) string {
	target, err := url.Parse(candidate)
	previous, previousErr := url.Parse(fallback)
	if err != nil || target.Host == "" || previousErr != nil || previous.Host == "" {
		return fallback
	}
	if source := ProviderSourceForURL(fallback); source != "" && (source == ProviderSourceForURL(candidate) || ProviderSourceForURL(candidate) == "") {
		previous.Scheme, previous.Host = target.Scheme, target.Host
	}
	previous.Fragment = ""
	return previous.String()
}

func (d *Downloader) GetHuangguoChapters(ctx context.Context, source, sourceID string) (string, []Chapter, error) {
	p, ok := d.provider(source)
	if !ok {
		return "", nil, fmt.Errorf("unsupported provider source: %s", source)
	}
	drama, chapters, err := p.Detail(ctx, sourceID)
	return drama.DisplayTitle(), chapters, err
}

func (d *Downloader) repairCover(drama LibraryDrama) string {
	if d == nil {
		return drama.Cover
	}
	for _, source := range []string{drama.Source, SourceFromDramaID(drama.ID)} {
		if p, ok := d.provider(source); ok {
			if repairer, ok := p.(CoverRepairer); ok {
				return repairer.RepairCover(drama)
			}
		}
	}
	return drama.Cover
}

func (d *Downloader) mediaAuthorizer(address *url.URL) MediaAuthorizer {
	for _, descriptor := range registeredDescriptors() {
		if p, ok := d.provider(descriptor.ID); ok {
			if authorizer, ok := p.(MediaAuthorizer); ok && authorizer.AuthorizesMedia(address) {
				return authorizer
			}
		}
	}
	return nil
}
