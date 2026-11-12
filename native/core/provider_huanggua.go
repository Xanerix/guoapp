package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 黄瓜短剧：站点是 Nuxt SSR，没有可用的公开 JSON 接口；
// 列表/详情/取流直接解析服务端渲染出来的 HTML。
const (
	huangguaNavURL    = "https://hgdja1.com/"
	huangguaHostTTL   = 30 * time.Minute
	huangguaUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/135.0.0.0 Safari/537.36"
)

// 入口页无法解析时的兜底主机；这些域名会随入口页轮换。
var huangguaSeedHosts = []string{
	"https://abc.ywwegfkd.cc",
	"https://qwer.ywwegfkd.cc",
	"https://xyz.ywwegfkd.cc",
	"https://mkqjue.eyrkjyudz.cc",
	"https://porncz.eyrkjyudz.cc",
	"https://rixgqa.eyrkjyudz.cc",
}

var huangguaCategories = []nativeCategory{
	{ID: "", Name: "推荐"},
	{ID: "aiduanju", Name: "AI 短剧"},
	{ID: "manju", Name: "AI 漫剧"},
	{ID: "mogai", Name: "AI 魔改"},
	{ID: "zhenren", Name: "真人短剧"},
	{ID: "yuanchuang", Name: "原创短剧"},
	{ID: "browse", Name: "全部"},
}

var (
	huangguaCardSplit   = regexp.MustCompile(`<article data-xpch="card-drama"`)
	huangguaAlias       = regexp.MustCompile(`href="/drama/([a-zA-Z0-9\-]+)"`)
	huangguaCover       = regexp.MustCompile(`src="(/_img/[A-Za-z0-9+/=]+\.(?:jpeg|jpg|png|webp))"`)
	huangguaAlt         = regexp.MustCompile(`<img[^>]+alt="([^"]+)"`)
	huangguaEpisodeText = regexp.MustCompile(`(?:更新至|全)\s*(\d+)\s*集`)
	huangguaHeatText    = regexp.MustCompile(`([0-9.]+万?)\s*热度`)
	huangguaH1          = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	huangguaHTMLTag     = regexp.MustCompile(`(?s)<[^>]*>`)
	huangguaMetaDesc    = regexp.MustCompile(`(?is)<meta\s+name="description"\s+content="([^"]*)"`)
	huangguaHostLink    = regexp.MustCompile(`https?://[a-zA-Z0-9][a-zA-Z0-9\-]*\.(?:ywwegfkd\.cc|eyrkjyudz\.cc)`)
	huangguaPlayURL     = regexp.MustCompile(`"source_url"\s*:\s*"([^"]+)"`)
	huangguaEscapedM3U8 = regexp.MustCompile(`https?:\\u002F\\u002F[^"]+?\.m3u8[^"]*`)
)

type huangguaHostState struct {
	mu        sync.Mutex
	host      string
	updatedAt time.Time
}

func validHuangguaCategory(category string) bool {
	if category == "" {
		return true
	}
	for _, entry := range huangguaCategories {
		if entry.ID == category {
			return true
		}
	}
	return false
}

func huangguaContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, providerTextUserAgentKey{}, huangguaUserAgent)
}

// huangguaHostForRequest 返回当前可用的站点主机：
// 优先用 30 分钟内缓存，其次读入口页 hgdja1.com 列出的新域名，最后试静态兜底。
func (d *Downloader) huangguaHostForRequest(ctx context.Context) (string, error) {
	state := &d.huangguaHost
	state.mu.Lock()
	cached, fresh := state.host, time.Since(state.updatedAt) < huangguaHostTTL
	state.mu.Unlock()
	if cached != "" && fresh {
		return cached, nil
	}
	candidates := d.huangguaHostCandidates(ctx)
	if cached != "" {
		candidates = append([]string{cached}, candidates...)
	}
	for _, host := range candidates {
		if d.huangguaHostWorks(ctx, host) {
			state.mu.Lock()
			state.host, state.updatedAt = host, time.Now()
			state.mu.Unlock()
			return host, nil
		}
	}
	return "", errors.New("黄瓜短剧当前没有可用线路")
}

func (d *Downloader) huangguaHostCandidates(ctx context.Context) []string {
	var out []string
	seen := map[string]bool{}
	add := func(host string) {
		host = strings.TrimRight(strings.TrimSpace(host), "/")
		if host == "" || seen[host] || strings.Contains(host, "hgdja1.com") {
			return
		}
		seen[host] = true
		out = append(out, host)
	}
	if body, err := d.fetchProviderText(huangguaContext(ctx), huangguaNavURL, huangguaNavURL); err == nil {
		for _, host := range huangguaHostLink.FindAllString(body, 12) {
			add(host)
		}
	}
	for _, host := range huangguaSeedHosts {
		add(host)
	}
	return out
}

func (d *Downloader) huangguaHostWorks(ctx context.Context, host string) bool {
	probeCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	body, err := d.fetchProviderText(huangguaContext(probeCtx), host+"/aiduanju", host+"/")
	return err == nil && strings.Contains(body, "黄瓜短剧") && strings.Contains(body, "/drama/")
}

func (d *Downloader) fetchHuangguaCategories() []nativeCategory {
	all := []nativeCategory{{Name: "全部"}}
	return append(all, huangguaCategories...)
}

func (d *Downloader) fetchHuangguaCatalogPage(ctx context.Context, page int, category string) ([]Drama, bool, error) {
	if page > 1 {
		// 站点 SSR 每类只渲染首屏，翻页交给前端接口，这里不重复请求。
		return nil, false, nil
	}
	if !validHuangguaCategory(category) {
		return nil, false, errors.New("黄瓜短剧分类无效")
	}
	host, err := d.huangguaHostForRequest(ctx)
	if err != nil {
		return nil, false, err
	}
	path := "/"
	if category != "" {
		path = "/" + category
	}
	body, err := d.fetchProviderText(huangguaContext(ctx), host+path, host+"/")
	if err != nil {
		return nil, false, err
	}
	return parseHuangguaCards(body, host), false, nil
}

func parseHuangguaCards(body, host string) []Drama {
	segments := huangguaCardSplit.Split(body, -1)
	items := make([]Drama, 0, len(segments))
	seen := map[string]bool{}
	for _, segment := range segments[1:] {
		alias := ""
		if match := huangguaAlias.FindStringSubmatch(segment); len(match) == 2 {
			alias = match[1]
		}
		if alias == "" || seen[alias] {
			continue
		}
		seen[alias] = true
		title := ""
		if match := huangguaAlt.FindStringSubmatch(segment); len(match) == 2 {
			title = strings.TrimSpace(match[1])
		}
		if title == "" {
			continue
		}
		cover := ""
		if match := huangguaCover.FindStringSubmatch(segment); len(match) == 2 {
			// 站点 CDN 上的封面是加密存储的，/_img/ 是站点的解密代理端点，
			// 必须保留站内路径作为封面地址，不能解码出原始 CDN 地址直连。
			cover = host + match[1]
		}
		episodes := 0
		if match := huangguaEpisodeText.FindStringSubmatch(segment); len(match) == 2 {
			episodes, _ = strconv.Atoi(match[1])
		}
		heat := ""
		if match := huangguaHeatText.FindStringSubmatch(segment); len(match) == 2 {
			heat = match[1] + "热度"
		}
		items = append(items, Drama{
			ID: providerDramaID(sourceHuanggua, alias), Source: sourceHuanggua, SourceID: alias,
			Title: title, Name: title, Cover: cover, CoverURL: cover,
			TotalEpisode: strconv.Itoa(episodes), EpisodeCount: strconv.Itoa(episodes),
			Views: heat,
		})
	}
	return items
}

func (d *Downloader) fetchHuangguaDetail(ctx context.Context, alias string) (Drama, []Chapter, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" || strings.ContainsAny(alias, "/?#") {
		return Drama{}, nil, errors.New("黄瓜短剧剧集 ID 无效")
	}
	host, err := d.huangguaHostForRequest(ctx)
	if err != nil {
		return Drama{}, nil, err
	}
	pageURL := host + "/drama/" + alias
	body, err := d.fetchProviderText(huangguaContext(ctx), pageURL, host+"/")
	if err != nil {
		return Drama{}, nil, err
	}
	title := ""
	if match := huangguaH1.FindStringSubmatch(body); len(match) == 2 {
		title = strings.TrimSpace(huangguaHTMLTag.ReplaceAllString(match[1], ""))
	}
	if title == "" {
		return Drama{}, nil, errors.New("黄瓜短剧未返回剧集信息")
	}
	desc := ""
	if match := huangguaMetaDesc.FindStringSubmatch(body); len(match) == 2 {
		desc = strings.TrimSpace(match[1])
	}
	cover := ""
	if match := huangguaCover.FindStringSubmatch(body); len(match) == 2 {
		cover = host + match[1]
	}
	chapters := huangguaChapters(body, host, alias)
	drama := Drama{
		ID: providerDramaID(sourceHuanggua, alias), Source: sourceHuanggua, SourceID: alias,
		Title: title, Name: title, Desc: desc, Intro: desc, Cover: cover, CoverURL: cover,
		ChannelName: "短剧", TotalEpisode: strconv.Itoa(len(chapters)), EpisodeCount: strconv.Itoa(len(chapters)),
	}
	return drama, chapters, nil
}

func huangguaChapters(body, host, alias string) []Chapter {
	pattern := regexp.MustCompile(`/play/` + regexp.QuoteMeta(alias) + `/(\d+)`)
	numbers := map[int]bool{}
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		if number, err := strconv.Atoi(match[1]); err == nil && number > 0 && number <= 9999 {
			numbers[number] = true
		}
	}
	if len(numbers) == 0 {
		numbers[1] = true
	}
	ordered := make([]int, 0, len(numbers))
	for number := range numbers {
		ordered = append(ordered, number)
	}
	sort.Ints(ordered)
	chapters := make([]Chapter, 0, len(ordered))
	for _, number := range ordered {
		chapters = append(chapters, Chapter{
			ID:             providerChapterID(sourceHuanggua, alias, "ep-"+strconv.Itoa(number)),
			Source:         sourceHuanggua,
			Title:          fmt.Sprintf("第%d集", number),
			PageURL:        host + "/play/" + url.PathEscape(alias) + "/" + strconv.Itoa(number),
			CurrentEpisode: rawEpisode(number),
		})
	}
	return chapters
}

// resolveHuangguaMedia 从播放页的 SSR 数据里取带签名的 m3u8 地址。
func (d *Downloader) resolveHuangguaMedia(ctx context.Context, task Task) (providerMedia, error) {
	chapter := task.Chapter
	pageURL := chapter.PageURL
	if pageURL == "" {
		if _, sourceID, valid := splitProviderDramaID(task.DramaID); valid {
			episode := chapterEpisodeNumber(chapter, task.Index)
			host, err := d.huangguaHostForRequest(ctx)
			if err != nil {
				return providerMedia{}, err
			}
			pageURL = host + "/play/" + url.PathEscape(sourceID) + "/" + strconv.Itoa(episode)
		}
	}
	if pageURL == "" {
		return providerMedia{}, errors.New("黄瓜短剧播放地址无效")
	}
	body, err := d.fetchProviderText(huangguaContext(ctx), pageURL, pageURL)
	if err != nil {
		return providerMedia{}, err
	}
	address := parseHuangguaPlayURL(body)
	if address == "" {
		return providerMedia{}, errors.New("黄瓜短剧没有返回播放地址")
	}
	return providerMedia{URL: address, Referer: pageURL}, nil
}

func parseHuangguaPlayURL(body string) string {
	if match := huangguaPlayURL.FindStringSubmatch(body); len(match) == 2 && strings.HasPrefix(match[1], "http") {
		return unescapeHuangguaURL(match[1])
	}
	if match := huangguaEscapedM3U8.FindString(body); match != "" {
		return unescapeHuangguaURL(match)
	}
	if match := regexp.MustCompile(`https?://[^"'\s]+\.m3u8[^"'\s]*`).FindString(body); match != "" {
		return unescapeHuangguaURL(match)
	}
	return ""
}

func unescapeHuangguaURL(raw string) string {
	replaced := strings.NewReplacer(`\u002F`, "/", `\u0026`, "&", `\/`, "/").Replace(raw)
	if unquoted, err := strconv.Unquote(`"` + strings.ReplaceAll(replaced, `"`, `\"`) + `"`); err == nil {
		replaced = unquoted
	}
	return replaced
}
