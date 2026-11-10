package core

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	providerPublishRefreshInterval = time.Minute
	providerPublishRetry           = 2 * time.Minute
	providerPublishPageMaxBytes    = 2 << 20
	providerPublishUserAgent       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

type providerPublishCache struct {
	hosts     []string
	fetchedAt time.Time
	retryAt   time.Time
	lastErr   error
	inflight  chan struct{}
}

var (
	publishedProviderHosts sync.Map // hostname -> source
	rePublishHref          = regexp.MustCompile(`(?is)href=["'](https?://[^"']+)["']`)
	rePublishScript        = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc=["']([^"']+\.js(?:\?[^"']*)?)["']`)
	rePublishQuoted        = regexp.MustCompile(`['"]([^'"]+)['"]`)
)

func publishedProviderHostSource(host string) string {
	value, found := publishedProviderHosts.Load(strings.ToLower(strings.TrimSpace(host)))
	if !found {
		return ""
	}
	source, _ := value.(string)
	return source
}

func rememberPublishedProviderHosts(source string, hosts []string) {
	source = canonicalProviderSource(source)
	for _, address := range hosts {
		parsed, err := url.Parse(strings.TrimSpace(address))
		if err != nil {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if host != "" {
			publishedProviderHosts.Store(host, source)
		}
	}
}

func providerPublishPages(source string) (pages, canonical []string) {
	switch canonicalProviderSource(source) {
	case sourceHuangguoAI:
		return []string{
			"https://huangguoai.pages.dev/",
			"https://huangguoai.ai/",
			"https://hgai8.com/",
		}, []string{"https://huangguoai.com"}
	case sourceLanguo:
		return []string{
			"https://languoai.pages.dev/",
			"https://1g9z14j0.cc/",
			"https://languotv.com/",
			"https://languo.ai/go-home/",
		}, append([]string(nil), languoBaseMirrors...)
	default:
		return nil, nil
	}
}

func (d *Downloader) providerPublishedHosts(source string) []string {
	source = canonicalProviderSource(source)
	d.publishMu.Lock()
	defer d.publishMu.Unlock()
	state := d.publishCache[source]
	if state == nil || len(state.hosts) == 0 {
		return nil
	}
	return append([]string(nil), state.hosts...)
}

func (d *Downloader) refreshProviderPublishedHosts(ctx context.Context, source string) ([]string, error) {
	source = canonicalProviderSource(source)
	pages, _ := providerPublishPages(source)
	if len(pages) == 0 {
		return nil, errors.New("当前站源没有地址发布页")
	}
	d.publishMu.Lock()
	if d.publishCache == nil {
		d.publishCache = map[string]*providerPublishCache{}
	}
	state := d.publishCache[source]
	if state == nil {
		state = &providerPublishCache{}
		d.publishCache[source] = state
	}
	if len(state.hosts) > 0 && !state.fetchedAt.IsZero() && time.Since(state.fetchedAt) < providerPublishRefreshInterval {
		hosts := append([]string(nil), state.hosts...)
		d.publishMu.Unlock()
		return hosts, nil
	}
	if !state.retryAt.IsZero() && time.Now().Before(state.retryAt) {
		hosts := append([]string(nil), state.hosts...)
		err := state.lastErr
		d.publishMu.Unlock()
		if len(hosts) > 0 {
			return hosts, nil
		}
		return nil, err
	}
	if state.inflight != nil {
		wait := state.inflight
		d.publishMu.Unlock()
		select {
		case <-wait:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		d.publishMu.Lock()
		hosts := append([]string(nil), state.hosts...)
		err := state.lastErr
		d.publishMu.Unlock()
		if len(hosts) > 0 {
			return hosts, nil
		}
		return nil, err
	}
	state.inflight = make(chan struct{})
	d.publishMu.Unlock()

	hosts, err := d.loadProviderPublishedHosts(ctx, source, pages)

	d.publishMu.Lock()
	wait := state.inflight
	state.inflight = nil
	if err == nil && len(hosts) > 0 {
		state.hosts = hosts
		state.fetchedAt = time.Now()
		state.retryAt = time.Time{}
		state.lastErr = nil
	} else {
		state.lastErr = err
		state.retryAt = time.Now().Add(providerPublishRetry)
	}
	result := append([]string(nil), state.hosts...)
	storedErr := state.lastErr
	close(wait)
	d.publishMu.Unlock()
	if len(result) > 0 {
		return result, nil
	}
	return nil, storedErr
}

func (d *Downloader) loadProviderPublishedHosts(ctx context.Context, source string, pages []string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var hosts []string
	var failures []error
	for _, page := range pages {
		found, err := d.fetchProviderPublishPage(ctx, page)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		hosts = appendUniqueStrings(hosts, found...)
		if len(found) > 0 {
			break
		}
	}
	if len(hosts) == 0 {
		if len(failures) > 0 {
			return nil, errors.Join(failures...)
		}
		return nil, errors.New("地址发布页暂未提供可用域名")
	}
	rememberPublishedProviderHosts(source, hosts)
	return hosts, nil
}

func (d *Downloader) fetchProviderPublishPage(ctx context.Context, pageURL string) ([]string, error) {
	body, err := d.fetchProviderPublishText(ctx, pageURL)
	if err != nil {
		return nil, err
	}
	var hosts []string
	hosts = appendUniqueStrings(hosts, parseProviderPublishHTMLHosts(body)...)
	hosts = appendUniqueStrings(hosts, parseProviderPublishScriptHosts(body)...)
	scripts := rePublishScript.FindAllStringSubmatchIndex(body, -1)
	for index, match := range scripts {
		if index >= 3 || len(match) < 4 || match[2] < 0 || match[3] < 0 {
			break
		}
		address := resolveProviderURL(pageURL, html.UnescapeString(body[match[2]:match[3]]))
		if address == "" {
			continue
		}
		script, err := d.fetchProviderPublishText(ctx, address)
		if err != nil {
			continue
		}
		hosts = appendUniqueStrings(hosts, parseProviderPublishScriptHosts(script)...)
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("%s 未解析到可用域名", pageURL)
	}
	return hosts, nil
}

func (d *Downloader) fetchProviderPublishText(ctx context.Context, address string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", providerPublishUserAgent)
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	response, err := d.doCatalogRequestWithTimeout(request, 6*time.Second)
	if err != nil {
		return "", publicError(err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, providerPublishPageMaxBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if len(body) > providerPublishPageMaxBytes {
		return "", errors.New("地址发布页响应过大")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("%s 返回 HTTP %d", address, response.StatusCode)
	}
	return string(body), nil
}

func parseProviderPublishHTMLHosts(body string) []string {
	var hosts []string
	for _, match := range rePublishHref.FindAllStringSubmatch(body, -1) {
		if len(match) < 2 {
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(html.UnescapeString(match[1])))
		if err != nil {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if providerPublishHostCandidate(host) {
			hosts = appendUniqueStrings(hosts, "https://"+host)
		}
	}
	return hosts
}

func parseProviderPublishScriptHosts(script string) []string {
	subdomains := providerPublishScriptArray(script, "subdomains")
	bases := providerPublishScriptArray(script, "urls")
	if len(bases) == 0 {
		return nil
	}
	hosts := make([]string, 0, len(subdomains)*len(bases))
	for _, subdomain := range subdomains {
		subdomain = strings.Trim(strings.TrimSpace(subdomain), ".")
		if subdomain == "" {
			continue
		}
		for _, base := range bases {
			base = providerPublishBaseHost(base)
			if host := subdomain + "." + base; providerPublishHostCandidate(host) {
				hosts = appendUniqueStrings(hosts, "https://"+host)
				if len(hosts) >= 24 {
					return hosts
				}
			}
		}
	}
	for _, base := range bases {
		host := providerPublishBaseHost(base)
		if providerPublishHostCandidate(host) {
			hosts = appendUniqueStrings(hosts, "https://"+host)
		}
	}
	if len(hosts) > 24 {
		hosts = hosts[:24]
	}
	return hosts
}

func providerPublishScriptArray(script, name string) []string {
	pattern := regexp.MustCompile(`(?is)\bvar\s+` + regexp.QuoteMeta(name) + `\s*=\s*\[(.*?)\]`)
	match := pattern.FindStringSubmatch(script)
	if len(match) < 2 {
		return nil
	}
	var values []string
	for _, row := range rePublishQuoted.FindAllStringSubmatch(match[1], -1) {
		if len(row) < 2 {
			continue
		}
		value := strings.TrimSpace(row[1])
		if value != "" {
			values = append(values, value)
		}
		if len(values) >= 64 {
			break
		}
	}
	return values
}

func providerPublishBaseHost(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if index := strings.IndexAny(raw, "/?#"); index >= 0 {
		raw = raw[:index]
	}
	return strings.ToLower(strings.Trim(raw, " ."))
}

func providerPublishHostCandidate(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " /:?#[]@") {
		return false
	}
	if net.ParseIP(host) != nil {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) < 3 {
		return false
	}
	prefix := labels[0]
	switch prefix {
	case "pic", "img", "image", "static", "cdn", "assets", "media", "api":
		return false
	}
	for _, suffix := range []string{
		".pages.dev", ".github.io", ".cloudfront.net", ".googleapis.com", ".gstatic.com",
		".googletagmanager.com", ".doubleclick.net", ".sentry.io", ".t.me", ".telegram.org",
		".x.com", ".twitter.com", ".tiktok.com", ".youtube.com", ".facebook.com", ".instagram.com",
	} {
		if strings.HasSuffix(host, suffix) {
			return false
		}
	}
	return true
}

func appendUniqueStrings(values []string, extra ...string) []string {
	seen := make(map[string]bool, len(values)+len(extra))
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range extra {
		if value != "" && !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	return values
}

func providerSourceLabel(source string) string {
	switch canonicalProviderSource(source) {
	case sourceHuangguoAI:
		return "黄果"
	case sourceLanguo:
		return "蓝果"
	case sourceHuangguoVideo:
		return "黄果视频"
	case sourceHuangdou:
		return "黄豆"
	case sourceHongguo:
		return "红果"
	case sourceHuangju:
		return "剧果"
	case sourceYeguo:
		return "野果"
	case sourceJinguoDuanju:
		return "禁果短剧"
	case sourceJinguo:
		return "禁果"
	case sourceDSD:
		return "帝果"
	case sourceSorani:
		return "青空"
	case sourceGuipian:
		return "鬼片"
	case sourceHanxiaoquan:
		return "韩小圈"
	case sourceHeitao:
		return "黑桃"
	case sourceBeabox:
		return "笔盒"
	default:
		return "站源"
	}
}
