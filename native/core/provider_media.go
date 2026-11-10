package core

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type providerMedia struct {
	credentials *providerMediaCredentials
	URL         string
	Referer     string
	Duration    time.Duration
	Playlist    string
	HLSKey      []byte
	CENCKey     []byte
	Quality     int
	Variants    []providerMedia
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
	var configured, fallback string
	switch source {
	case sourceHongguo:
		configured, fallback = d.cfg.HongguoURL, hongguoBaseURL
	default:
		fallback = "https://d2pypzndaqisk.cloudfront.net"
	}
	return strings.TrimRight(firstNonEmpty(configured, fallback), "/")
}

func providerSourceForURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if id, ok := registeredSource(host); ok {
		return id
	}
	switch {
	case host == "hongguoduanju.com" || host == "www.hongguoduanju.com":
		return sourceHongguo
	default:
		return ""
	}
}

func isHuangguoAIMirrorHost(host string) bool {
	for _, domain := range huangguoAIMirrorDomains {
		if strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func (d *Downloader) providerURLCandidates(raw string) []string {
	source := providerSourceForURL(raw)
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
	add(rehostProviderURL(parsed, preferred))
	configured := d.providerBaseURL(source)
	add(rehostProviderURL(parsed, configured))
	if providerSourceForURL(configured) == "" {
		return candidates
	}
	for _, candidate := range providerURLCandidates(raw) {
		add(candidate)
	}
	return candidates
}

func (d *Downloader) resolveProviderMedia(ctx context.Context, task Task) (providerMedia, error) {
	chapter := task.Chapter
	chapter.Source = canonicalProviderSource(chapter.Source)
	if chapter.Source == "" {
		chapter.Source = sourceFromDramaID(task.DramaID)
	}
	if p, ok := d.provider(chapter.Source); ok {
		return p.ResolveMedia(ctx, task)
	}
	if strings.HasPrefix(chapter.VideoURL, "hongguo-cenc://") {
		return d.resolveHongguoMedia(ctx, task)
	}
	media := providerMedia{URL: chapter.VideoURL, Referer: firstNonEmpty(chapter.Referer, d.providerBaseURL(chapter.Source)+"/")}
	return d.finishProviderMedia(ctx, media, chapter.Source)
}

func (d *Downloader) finishProviderMedia(ctx context.Context, media providerMedia, source string) (providerMedia, error) {
	if !isProviderHTTPMediaURL(media.URL) {
		return providerMedia{}, fmt.Errorf("%s 未返回有效播放地址，请刷新章节或确认站点访问权限", source)
	}
	parsed, _ := url.Parse(media.URL)
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		playlist, finalURL, err := d.fetchMediaPlaylist(ctx, media.URL, media.Referer)
		if err != nil {
			return providerMedia{}, fmt.Errorf("获取播放列表失败: %w", err)
		}
		if !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(playlist, "\ufeff")), "#EXTM3U") {
			return providerMedia{}, fmt.Errorf("站点未返回有效 M3U8，可能需要登录或链接已失效")
		}
		if duration := m3u8Duration(playlist); duration > 0 {
			media.Duration = duration
		}
		media.Playlist = playlist
		media.URL = finalURL
	}
	return media, nil
}
