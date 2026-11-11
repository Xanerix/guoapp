package core

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
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
	// 黄果视频与黄果 AI 是同一套站点的两个入口：旧域名 huangguo.video 已不可用，
	// 统一跟随黄果 AI 的镜像域名，取流也改走新播放接口。
	if source == sourceHuangguoVideo && strings.TrimSpace(d.cfg.HuangguoVideoURL) == "" {
		if hosts := d.huangguoAIMirrorHosts(); len(hosts) > 0 {
			return hosts[0]
		}
		return huangguoAIBaseMirrors[1]
	}
	if source == sourceHuanggua {
		d.huangguaHost.mu.Lock()
		host := d.huangguaHost.host
		d.huangguaHost.mu.Unlock()
		if host != "" {
			return host
		}
		return huangguaSeedHosts[0]
	}
	var configured, fallback string
	switch source {
	case sourceHuangguoAI:
		configured, fallback = d.cfg.HuangguoAIURL, huangguoAIBaseURL
	case sourceHuangguoVideo:
		configured, fallback = d.cfg.HuangguoVideoURL, huangguoVideoBaseURL
	case sourceHuangdou:
		configured, fallback = d.cfg.HuangdouURL, huangdouBaseURL
	case sourceHongguo:
		configured, fallback = d.cfg.HongguoURL, hongguoBaseURL
	case sourceHuangju:
		configured, fallback = d.cfg.HuangjuURL, huangjuBaseURL
	case sourceYeguo:
		configured, fallback = d.cfg.YeguoURL, yeguoBaseURL
	case sourceJinguoDuanju:
		configured, fallback = jinguoDuanjuContentSite(d.cfg.JinguoDuanjuURL), jinguoDuanjuBaseURL
	case sourceDSD:
		configured, fallback = d.cfg.DSDURL, dsdBaseURL
	case sourceSorani:
		configured, fallback = d.cfg.SoraniURL, soraniSiteBaseURL
	case sourceGuipian:
		configured, fallback = d.cfg.GuipianURL, guipianSiteBaseURL
	case sourceHanxiaoquan:
		configured, fallback = d.cfg.HanxiaoquanURL, hanxiaoquanSiteBaseURL
	case sourceHeitao:
		configured, fallback = d.cfg.HeitaoURL, heitaoBaseURL
	case sourceBeabox:
		configured, fallback = d.cfg.BeaboxURL, beaboxBaseURL
	case sourceZuid:
		fallback = maccmsSites[sourceZuid].SiteBase
	case sourceFfzy:
		fallback = maccmsSites[sourceFfzy].SiteBase
	case sourceRycj:
		fallback = maccmsSites[sourceRycj].SiteBase
	case sourceXvideos:
		fallback = xvideosSiteBaseURL
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
	switch {
	case host == "huangguoai.com" || isHuangguoAIMirrorHost(host):
		return sourceHuangguoAI
	case host == "huangguo.video":
		return sourceHuangguoVideo
	case isHuangdouHost(host):
		return sourceHuangdou
	case host == "hongguoduanju.com" || host == "www.hongguoduanju.com":
		return sourceHongguo
	case host == "huangju.net" || host == "www.huangju.net" || host == "api.huangju.net":
		return sourceHuangju
	case host == "ygdj7.com" || host == "www.ygdj7.com" || host == "ygdj15.com" || host == "www.ygdj15.com" ||
		host == "analyze.buxefaex.cc" || strings.HasSuffix(host, ".buxefaex.cc") ||
		strings.HasSuffix(host, ".fzchosdi.cc") || strings.HasSuffix(host, ".lwblvxvy.cc") || strings.HasSuffix(host, ".vgbyziuz.cc") ||
		strings.HasSuffix(host, ".ygrwdsgt.cc") || strings.HasSuffix(host, ".zxcrirxx.cc") || strings.HasSuffix(host, ".ocdjlxow.cc") ||
		host == "yeguodj.com" || host == "www.yeguodj.com":
		return sourceYeguo
	case host == "jinguoduanju.app" || host == "www.jinguoduanju.app" || host == "play.jinguoduanju.app" ||
		host == "jgdj.app" || host == "www.jgdj.app":
		return sourceJinguoDuanju
	case host == "dsd.com.se" || host == "www.dsd.com.se" || host == "dsd.lol" || host == "dsd901.lol" || host == "www.dsd901.lol":
		return sourceDSD
	case host == "hgdja1.com" || host == "www.hgdja1.com" || host == "hgdju.com" || host == "www.hgdju.com" ||
		strings.HasSuffix(host, ".ywwegfkd.cc") || strings.HasSuffix(host, ".eyrkjyudz.cc"):
		return sourceHuanggua
	case host == "sorani.net" || host == "www.sorani.net" || host == "api.sorani.cc" || host == "sorani.cc":
		return sourceSorani
	case host == "guipianwu.com" || host == "www.guipianwu.com":
		return sourceGuipian
	case host == "jennyhow.com" || host == "www.jennyhow.com":
		return sourceHanxiaoquan
	case host == "heitaodj.com" || host == "www.heitaodj.com":
		return sourceHeitao
	case host == "xvideos.com" || host == "www.xvideos.com":
		return sourceXvideos
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
	if source == "" || source == sourceHuangju || source == sourceYeguo || source == sourceHuanggua {
		// 黄瓜短剧的域名由自己的入口页轮换，这里不再按站源重写主机。
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
	if source == sourceHuangguoAI {
		// 官方发布页解析出的域名是最新可用的，排在最前；旧镜像只做兜底。
		for _, host := range d.huangguoAIMirrorHostsForRequest() {
			add(rehostProviderURL(parsed, host))
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
	// 候选过多时逐个 5 秒超时会把首个请求拖到一分钟以上，只保留最可能命中的前几个。
	if len(candidates) > huangguoAIMaxCandidates {
		candidates = candidates[:huangguoAIMaxCandidates]
	}
	return candidates
}

func (d *Downloader) resolveProviderMedia(ctx context.Context, task Task) (providerMedia, error) {
	chapter := task.Chapter
	chapter.Source = canonicalProviderSource(chapter.Source)
	if chapter.Source == "" {
		chapter.Source = sourceFromDramaID(task.DramaID)
	}
	if chapter.Source == sourceCloudFront {
		return d.resolveLegacyMedia(ctx, task)
	}
	if chapter.Source == sourceHuangju {
		return d.resolveHuangjuMedia(ctx, task)
	}
	if chapter.Source == sourceYeguo {
		return d.resolveYeguoMedia(ctx, task)
	}
	if chapter.Source == sourceJinguoDuanju {
		return d.resolveJinguoDuanjuMedia(ctx, task)
	}
	if chapter.Source == sourceJinguo {
		return d.resolveJinguoMedia(ctx, task)
	}
	if chapter.Source == sourceDSD {
		return d.resolveDSDMedia(ctx, task)
	}
	if chapter.Source == sourceSorani {
		return d.resolveSoraniMedia(ctx, task)
	}
	if chapter.Source == sourceGuipian {
		return d.resolveGuipianMedia(ctx, task)
	}
	if chapter.Source == sourceHanxiaoquan {
		return d.resolveHanxiaoquanMedia(ctx, task)
	}
	if chapter.Source == sourceHeitao {
		return d.resolveHeitaoMedia(ctx, task)
	}
	if chapter.Source == sourceBeabox {
		return d.resolveBeaboxMedia(ctx, task)
	}
	if chapter.Source == sourceXvideos {
		return d.resolveXvideosMedia(ctx, task)
	}
	if chapter.Source == sourceHuanggua {
		return d.resolveHuangguaMedia(ctx, task)
	}
	if strings.HasPrefix(chapter.VideoURL, "hongguo-cenc://") {
		return d.resolveHongguoMedia(ctx, task)
	}
	// 黄果视频已并入黄果 AI 站点，数字 ID 直接走新版播放接口，
	// 取到地址后清掉旧页地址，避免再走已失效的 data-hls 解析。
	videoAPIResolved := false
	if chapter.Source == sourceHuangguoVideo {
		if _, sourceID, valid := splitProviderDramaID(task.DramaID); valid && rankingSourceID.MatchString(sourceID) {
			if address, err := d.fetchHuangguoAIPlayURL(ctx, sourceID, chapterEpisodeNumber(chapter, task.Index)); err == nil && address != "" {
				chapter.VideoURL = address
				chapter.PageURL = ""
				chapter.Referer = d.providerBaseURL(sourceHuangguoVideo) + "/"
				videoAPIResolved = true
			}
		}
	}
	if chapter.PageURL == "" && chapter.Source == sourceHuangguoVideo && !videoAPIResolved {
		if source, sourceID, valid := splitProviderDramaID(task.DramaID); valid {
			_, chapters, err := d.GetHuangguoChapters(ctx, source, sourceID)
			if err != nil {
				return providerMedia{}, fmt.Errorf("刷新旧任务播放地址失败: %w", err)
			}
			matched := false
			for _, fresh := range chapters {
				if fresh.ID == chapter.ID {
					chapter = fresh
					matched = true
					break
				}
			}
			if !matched {
				return providerMedia{}, fmt.Errorf("原章节已变化，请更新该合集后重新下载")
			}
		}
	}
	media := providerMedia{URL: chapter.VideoURL, Referer: firstNonEmpty(chapter.Referer, d.providerBaseURL(chapter.Source)+"/")}
	if chapter.Source == sourceHuangdou {
		_, sourceID, valid := splitProviderDramaID(task.DramaID)
		if valid {
			sequence, err := strconv.Atoi(chapter.EpisodeString(task.Index))
			if err != nil || sequence < 1 {
				return providerMedia{}, fmt.Errorf("黄豆集数无效")
			}
			client := newHuangdouAPIClient(d)
			media, err = d.resolveHuangdouPlayback(ctx, client, sourceID, sequence)
			if err != nil {
				return providerMedia{}, err
			}
			media.Referer = client.host + "/home"
		}
	}
	if chapter.Source == sourceHuangguoAI {
		_, sourceID, valid := splitProviderDramaID(task.DramaID)
		if !valid {
			return providerMedia{}, fmt.Errorf("黄果剧集信息无效")
		}
		address, err := d.fetchHuangguoAIPlayURL(ctx, sourceID, chapterEpisodeNumber(chapter, task.Index))
		if err != nil {
			return providerMedia{}, err
		}
		media.URL = address
	}
	if chapter.PageURL != "" && chapter.Source == sourceHuangguoVideo {
		responses := &playbackResponseURLs{}
		pageContext := context.WithValue(ctx, playbackResponseURLsKey{}, responses)
		body, err := d.fetchProviderText(pageContext, chapter.PageURL, media.Referer)
		if err != nil {
			return providerMedia{}, err
		}
		pageURL := chapter.PageURL
		if actual, ok := responses.values.Load(chapter.PageURL); ok {
			pageURL = actual.(string)
		}
		media.URL = parseDataHLS(body, pageURL)
		media.Referer = pageURL
	}
	if !isProviderHTTPMediaURL(media.URL) {
		return providerMedia{}, fmt.Errorf("%s 未返回有效播放地址，请刷新章节或确认站点访问权限", chapter.Source)
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
