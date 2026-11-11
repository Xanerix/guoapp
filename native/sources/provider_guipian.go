package sources

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"duanjuapp/native/core"
)

const (
	guipianSiteBaseURL = "https://guipianwu.com"
	guipianUserAgent   = "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	guipianPageSize    = 24
)

var guipianCategories = []core.Category{
	{ID: "3", Name: "恐怖片"},
	{ID: "6", Name: "大陆鬼片"},
	{ID: "7", Name: "日韩鬼片"},
	{ID: "8", Name: "林正英鬼片"},
	{ID: "9", Name: "港台鬼片"},
	{ID: "10", Name: "泰国鬼片"},
	{ID: "11", Name: "欧美鬼片"},
	{ID: "31", Name: "剧情片"},
	{ID: "12", Name: "国产剧"},
	{ID: "13", Name: "美剧"},
	{ID: "14", Name: "韩剧"},
	{ID: "15", Name: "日剧"},
	{ID: "16", Name: "泰剧"},
	{ID: "17", Name: "港台剧"},
	{ID: "18", Name: "其他剧"},
	{ID: "23", Name: "日韩动漫"},
	{ID: "24", Name: "国产动漫"},
	{ID: "25", Name: "欧美动漫"},
	{ID: "26", Name: "港台动漫"},
}

var (
	reGuipianMainList   = regexp.MustCompile(`(?is)<ul class="row video-list video-film-list[^"]*">(.*?)</ul>`)
	reGuipianCard       = regexp.MustCompile(`(?is)<a class="video-link" href="/nv/(\d+)\.html"[^>]*title="([^"]*)"(.*?)</a>`)
	reGuipianCover      = regexp.MustCompile(`(?is)data-(?:original|background)="([^"]+)"`)
	reGuipianRemark     = regexp.MustCompile(`(?is)<div class="video-duration">(.*?)</div>`)
	reGuipianTitle      = regexp.MustCompile(`(?is)<h1 class="media-title[^"]*">(.*?)</h1>`)
	reGuipianTitleTag   = regexp.MustCompile(`(?is)<title>(.*?)</title>`)
	reGuipianDetailPic  = regexp.MustCompile(`(?is)<div class="detail-img"><img[^>]+src="([^"]+)"`)
	reGuipianDetailPic2 = regexp.MustCompile(`(?is)<div class="detail-cover[^"]*"[^>]*data-original="([^"]+)"`)
	reGuipianContent    = regexp.MustCompile(`(?is)简介：(.*?)</li>`)
	reGuipianYear       = regexp.MustCompile(`(?is)年份：<span>(.*?)</span>`)
	reGuipianDirector   = regexp.MustCompile(`(?is)导演：(.*?)</span>`)
	reGuipianActor      = regexp.MustCompile(`(?is)主演：(.*?)</span>`)
	reGuipianAnchor     = regexp.MustCompile(`(?is)<a[^>]*>([^<]+)</a>`)
	reGuipianArea       = regexp.MustCompile(`(?is)地区：<span><a[^>]*>(.*?)</a>`)
	reGuipianStatus     = regexp.MustCompile(`(?is)状态：(.*?)</span>`)
	reGuipianTab        = regexp.MustCompile(`(?is)<li class="swiper-slide ewave-tab[^"]*" data-target="#(ewave-playlist-\d+)">([^<]+)</li>`)
	reGuipianEpisode    = regexp.MustCompile(`(?is)<li><a title='([^']*)' href='([^']+)'`)
	reGuipianNow        = regexp.MustCompile(`(?is)var\s+now\s*=\s*["']([^"']+)["']`)
	reGuipianMediaAny   = regexp.MustCompile(`(?i)https?://[^"'\s<>\\]+\.(?:m3u8|mp4)(?:\?[^"'\s<>\\]*)?`)
	reGuipianLeadNum    = regexp.MustCompile(`(\d+)`)
	reGuipianRSSItem    = regexp.MustCompile(`(?is)<item>(.*?)</item>`)
	reGuipianRSSTitle   = regexp.MustCompile(`(?is)<title>\s*(?:<!\[CDATA\[)?(.*?)(?:\]\]>)?\s*</title>`)
	reGuipianRSSLink    = regexp.MustCompile(`(?is)/nv/(\d+)\.html`)
	reGuipianTag        = regexp.MustCompile(`<[^>]+>`)
)

func validGuipianCategory(category string) bool {
	if category == "" {
		return true
	}
	if len(category) > 16 || strings.ContainsAny(category, "|/\\\x00\r\n") {
		return false
	}
	for _, entry := range guipianCategories {
		if entry.ID == category {
			return true
		}
	}
	return false
}

func guipianNumericID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 12 {
		return false
	}
	number, err := strconv.Atoi(value)
	return err == nil && number > 0
}

func guipianClean(text string) string {
	if text == "" {
		return ""
	}
	text = reGuipianTag.ReplaceAllString(text, "")
	text = guipianUnescape(text)
	text = strings.ReplaceAll(text, "\u3000", " ")
	return strings.Join(strings.Fields(text), " ")
}

func guipianUnescape(text string) string {
	replacer := strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"",
		"&apos;", "'", "&#39;", "'", "&nbsp;", " ",
	)
	for i := 0; i < 3; i++ {
		next := replacer.Replace(text)
		next = regexp.MustCompile(`&#x([0-9a-fA-F]+);`).ReplaceAllStringFunc(next, func(m string) string {
			hex := m[3 : len(m)-1]
			if code, err := strconv.ParseInt(hex, 16, 32); err == nil {
				return string(rune(code))
			}
			return m
		})
		next = regexp.MustCompile(`&#(\d+);`).ReplaceAllStringFunc(next, func(m string) string {
			if code, err := strconv.Atoi(m[2 : len(m)-1]); err == nil {
				return string(rune(code))
			}
			return m
		})
		if next == text {
			break
		}
		text = next
	}
	return text
}

func guipianFixURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	} else if strings.HasPrefix(raw, "/") {
		raw = guipianSiteBaseURL + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || !core.ValidNativeCoverURL(parsed) {
		return ""
	}
	return parsed.String()
}

func (p *guipianProvider) fetchGuipianCategories() []core.Category {
	return append([]core.Category(nil), guipianCategories...)
}

func (p *guipianProvider) guipianGet(ctx context.Context, path string) (string, error) {
	address := path
	if !strings.HasPrefix(address, "http") {
		address = guipianSiteBaseURL + path
	}
	response, err := p.host.FetchText(ctx, core.TextRequest{URL: address, Referer: guipianSiteBaseURL + "/", UserAgent: guipianUserAgent})
	return response.Body, err
}

func parseGuipianCards(body string) []core.Drama {
	segment := body
	if match := reGuipianMainList.FindStringSubmatch(body); len(match) > 1 {
		segment = match[1]
	}
	items := make([]core.Drama, 0, guipianPageSize)
	seen := map[string]bool{}
	for _, match := range reGuipianCard.FindAllStringSubmatch(segment, -1) {
		id := match[1]
		if !guipianNumericID(id) || seen[id] {
			continue
		}
		title := guipianClean(match[2])
		chunk := match[3]
		if title == "" {
			continue
		}
		seen[id] = true
		cover := ""
		if coverMatch := reGuipianCover.FindStringSubmatch(chunk); len(coverMatch) > 1 {
			cover = guipianFixURL(coverMatch[1])
		}
		remark := ""
		if remarkMatch := reGuipianRemark.FindStringSubmatch(chunk); len(remarkMatch) > 1 {
			remark = guipianClean(remarkMatch[1])
		}
		items = append(items, core.Drama{
			ID: core.ProviderDramaID(sourceGuipian, id), Source: sourceGuipian, SourceID: id,
			Title: core.Truncate(title, 512), Name: core.Truncate(title, 512),
			Cover: cover, CoverURL: cover, ChannelName: "鬼片网",
			CategoryName: "影视", Remark: core.Truncate(remark, 64),
		})
		if len(items) > 500 {
			break
		}
	}
	return items
}

func guipianPageCount(body, category string, page, count int) int {
	pattern := regexp.MustCompile(fmt.Sprintf(`(?is)/list/%s_(\d+)\.html`, regexp.QuoteMeta(category)))
	maxPage := page
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		if value, err := strconv.Atoi(match[1]); err == nil && value > maxPage && value < 100000 {
			maxPage = value
		}
	}
	if maxPage > page {
		return maxPage
	}
	if count >= guipianPageSize {
		return page + 1
	}
	return page
}

func (p *guipianProvider) fetchGuipianCatalogPage(ctx context.Context, page int, category, query string) ([]core.Drama, bool, error) {
	if page < 1 || page > 100000 {
		return nil, false, errors.New("鬼片网目录页码无效")
	}
	if query != "" {
		return p.searchGuipian(ctx, page, query)
	}
	if !validGuipianCategory(category) {
		return nil, false, errors.New("鬼片网内容分类无效")
	}
	if category == "" {
		category = guipianCategories[0].ID
	}
	path := "/list/" + category + ".html"
	if page > 1 {
		path = fmt.Sprintf("/list/%s_%d.html", category, page)
	}
	body, err := p.guipianGet(ctx, path)
	if err != nil {
		return nil, false, err
	}
	items := parseGuipianCards(body)
	if len(items) == 0 {
		return []core.Drama{}, false, nil
	}
	pageCount := guipianPageCount(body, category, page, len(items))
	return items, page < pageCount, nil
}

func guipianExtractField(body, label string, re *regexp.Regexp) string {
	if match := re.FindStringSubmatch(body); len(match) > 1 {
		return guipianClean(match[1])
	}
	return ""
}

func guipianActors(body string) string {
	match := reGuipianActor.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	names := make([]string, 0, 8)
	for _, anchor := range reGuipianAnchor.FindAllStringSubmatch(match[1], -1) {
		if name := guipianClean(anchor[1]); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return guipianClean(match[1])
	}
	return strings.Join(names, " ")
}

func guipianPickPlaylist(body string) (string, []core.Chapter, error) {
	tabs := reGuipianTab.FindAllStringSubmatch(body, -1)
	if len(tabs) == 0 {
		return "", nil, errors.New("鬼片网未找到可用播放线路")
	}
	site := guipianSiteBaseURL
	bestName := ""
	var best []core.Chapter
	seen := map[string]bool{}
	for _, tab := range tabs {
		anchor, name := tab[1], strings.TrimSpace(tab[2])
		if anchor == "" || seen[anchor] {
			continue
		}
		seen[anchor] = true
		marker := fmt.Sprintf(`id="%s"`, anchor)
		start := strings.Index(body, marker)
		if start < 0 {
			continue
		}
		segment := body[start+len(marker):]
		if end := strings.Index(segment, `<div class="playlist-slide`); end >= 0 {
			segment = segment[:end]
		} else if end := strings.Index(segment, `</section>`); end >= 0 {
			segment = segment[:end]
		}
		var chapters []core.Chapter
		seenPath := map[string]bool{}
		for index, ep := range reGuipianEpisode.FindAllStringSubmatch(segment, -1) {
			epTitle := guipianClean(ep[1])
			href := strings.TrimSpace(ep[2])
			if href == "" {
				continue
			}
			playURL := guipianPlayURL(href)
			if playURL == "" || seenPath[playURL] {
				continue
			}
			seenPath[playURL] = true
			order := index + 1
			if num := reGuipianLeadNum.FindString(epTitle); num != "" {
				if parsed, err := strconv.Atoi(num); err == nil && parsed > 0 && parsed <= 100000 {
					order = parsed
				}
			}
			if epTitle == "" {
				epTitle = fmt.Sprintf("第 %d 集", order)
			}
			chapters = append(chapters, core.Chapter{
				ID:             core.ProviderChapterID(sourceGuipian, "", playURL),
				Source:         sourceGuipian,
				Title:          core.Truncate(epTitle, 128),
				CurrentEpisode: core.RawEpisode(order),
				VideoURL:       "guipian-play://" + playURL,
				PageURL:        playURL,
				Referer:        site + "/",
			})
		}
		if len(chapters) > len(best) {
			best, bestName = chapters, name
		}
	}
	if len(best) == 0 {
		return "", nil, errors.New("鬼片网线路暂无可播放分集")
	}
	sort.SliceStable(best, func(i, j int) bool {
		left, _ := strconv.Atoi(best[i].EpisodeString(i + 1))
		right, _ := strconv.Atoi(best[j].EpisodeString(j + 1))
		return left < right
	})
	return bestName, best, nil
}

func guipianPlayURL(href string) string {
	href = strings.TrimSpace(href)
	if href == "" {
		return ""
	}
	if !strings.HasPrefix(href, "http") {
		href = guipianSiteBaseURL + "/" + strings.TrimPrefix(href, "/")
	}
	parsed, err := url.Parse(href)
	if err != nil || parsed.User != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "guipianwu.com" && host != "www.guipianwu.com" {
		return ""
	}
	if !strings.HasSuffix(strings.ToLower(parsed.Path), ".html") || !strings.Contains(parsed.Path, "/play/") {
		return ""
	}
	return parsed.String()
}

func (p *guipianProvider) fetchGuipianDetail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	if !guipianNumericID(sourceID) {
		return core.Drama{}, nil, errors.New("鬼片网视频 ID 无效")
	}
	body, err := p.guipianGet(ctx, "/nv/"+sourceID+".html")
	if err != nil {
		return core.Drama{}, nil, err
	}
	name := guipianExtractField(body, "title", reGuipianTitle)
	if name == "" {
		if match := reGuipianTitleTag.FindStringSubmatch(body); len(match) > 1 {
			raw := guipianClean(match[1])
			if strings.Contains(raw, "提示信息") || strings.Contains(raw, "影片不存在") {
				return core.Drama{}, nil, errors.New("鬼片网影片不存在")
			}
			name = strings.TrimSpace(regexp.MustCompile(`《(.*?)》.*`).ReplaceAllString(raw, "$1"))
		}
	}
	if name == "" {
		return core.Drama{}, nil, errors.New("鬼片网详情缺少视频名称")
	}
	cover := ""
	if match := reGuipianDetailPic.FindStringSubmatch(body); len(match) > 1 {
		cover = guipianFixURL(match[1])
	}
	if cover == "" {
		if match := reGuipianDetailPic2.FindStringSubmatch(body); len(match) > 1 {
			cover = guipianFixURL(match[1])
		}
	}
	description := guipianExtractField(body, "content", reGuipianContent)
	year := guipianExtractField(body, "year", reGuipianYear)
	director := guipianExtractField(body, "director", reGuipianDirector)
	area := guipianExtractField(body, "area", reGuipianArea)
	status := guipianExtractField(body, "status", reGuipianStatus)
	actors := guipianActors(body)

	drama := core.Drama{
		ID: core.ProviderDramaID(sourceGuipian, sourceID), Source: sourceGuipian, SourceID: sourceID,
		Title: core.Truncate(name, 512), Name: core.Truncate(name, 512),
		Desc: core.Truncate(description, 12000), Intro: core.Truncate(description, 12000),
		Cover: cover, CoverURL: cover, ChannelName: "鬼片网", CategoryName: "影视",
		Remark: core.Truncate(status, 64),
	}
	tags := make([]string, 0, 8)
	seenTag := map[string]bool{}
	addTag := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seenTag[value] && len([]rune(value)) <= 48 && len(tags) < 16 {
			seenTag[value] = true
			tags = append(tags, value)
		}
	}
	if year != "" {
		addTag(year + "年")
	}
	if area != "" {
		addTag(area)
	}
	if director != "" {
		addTag("导演 " + core.Truncate(director, 32))
	}
	if actors != "" {
		addTag("主演 " + core.Truncate(actors, 48))
	}
	drama.Tags = tags

	_, chapters, err := guipianPickPlaylist(body)
	if err != nil {
		return core.Drama{}, nil, err
	}
	for index := range chapters {
		chapters[index].ID = core.ProviderChapterID(sourceGuipian, sourceID, chapters[index].PageURL)
	}
	drama.TotalEpisode, drama.EpisodeCount = len(chapters), len(chapters)
	return drama, chapters, nil
}

func (p *guipianProvider) resolveGuipianMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	source, sourceID, valid := core.SplitProviderDramaID(task.DramaID)
	prefix := core.ProviderChapterID(sourceGuipian, sourceID, "")
	if !valid || source != sourceGuipian || !guipianNumericID(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return core.ProviderMedia{}, errors.New("鬼片网播放分集信息无效，请刷新详情")
	}
	playURL := strings.TrimPrefix(task.Chapter.ID, prefix)
	if playURL == "" {
		playURL = task.Chapter.PageURL
	}
	if guipianPlayURL(playURL) == "" {
		return core.ProviderMedia{}, errors.New("鬼片网播放页地址无效")
	}
	body, err := p.guipianGet(ctx, playURL)
	if err != nil {
		return core.ProviderMedia{}, err
	}
	address := ""
	if match := reGuipianNow.FindStringSubmatch(body); len(match) > 1 {
		address = strings.TrimSpace(match[1])
	}
	if address == "" {
		address = reGuipianMediaAny.FindString(body)
	}
	if address == "" {
		return core.ProviderMedia{}, errors.New("鬼片网该集暂无可用播放地址")
	}
	if strings.HasPrefix(address, "//") {
		address = "https:" + address
	}
	if !core.IsProviderHTTPMediaURL(address) {
		return core.ProviderMedia{}, errors.New("鬼片网播放地址无效")
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return core.ProviderMedia{}, errors.New("鬼片网播放地址无效")
	}
	referer := guipianSiteBaseURL + "/"
	media := core.ProviderMedia{URL: address, Referer: referer}
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		return p.host.PrepareWebMedia(ctx, media, "鬼片网")
	}
	return media, nil
}

func (p *guipianProvider) searchGuipian(ctx context.Context, page int, query string) ([]core.Drama, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 256 {
		return nil, false, errors.New("鬼片网搜索关键词无效")
	}
	body, err := p.guipianGet(ctx, "/xml/rss.xml")
	if err != nil {
		return nil, false, err
	}
	needle := strings.ToLower(query)
	var matched []core.Drama
	seen := map[string]bool{}
	for _, item := range reGuipianRSSItem.FindAllStringSubmatch(body, -1) {
		segment := item[1]
		titleMatch := reGuipianRSSTitle.FindStringSubmatch(segment)
		idMatch := reGuipianRSSLink.FindStringSubmatch(segment)
		if len(titleMatch) < 2 || len(idMatch) < 2 {
			continue
		}
		title := guipianClean(titleMatch[1])
		id := idMatch[1]
		if title == "" || !guipianNumericID(id) || seen[id] {
			continue
		}
		if !strings.Contains(strings.ToLower(title), needle) {
			continue
		}
		seen[id] = true
		matched = append(matched, core.Drama{
			ID: core.ProviderDramaID(sourceGuipian, id), Source: sourceGuipian, SourceID: id,
			Title: core.Truncate(title, 512), Name: core.Truncate(title, 512),
			ChannelName: "鬼片网", CategoryName: "影视",
		})
	}
	start := (page - 1) * guipianPageSize
	if start >= len(matched) {
		return []core.Drama{}, false, nil
	}
	end := start + guipianPageSize
	if end > len(matched) {
		end = len(matched)
	}
	return matched[start:end], end < len(matched), nil
}

type guipianProvider struct{ host core.Host }

var guipianDescriptor = core.SourceDescriptor{
	ID:                sourceGuipian,
	Name:              "鬼片",
	Description:       "鬼片 · 电视剧 · 动漫",
	Aliases:           []string{"guipianwu.com", "www.guipianwu.com"},
	BaseURL:           guipianSiteBaseURL,
	AllowEmptyCatalog: true,
	ValidCategory:     validGuipianCategory,
}

func init() {
	core.Register(guipianDescriptor, func(host core.Host) core.Provider { return &guipianProvider{host: host} })
}

func (p *guipianProvider) Descriptor() core.SourceDescriptor { return guipianDescriptor }

func (p *guipianProvider) Categories(context.Context, bool) ([]core.Category, error) {
	return p.fetchGuipianCategories(), nil
}

func (p *guipianProvider) CatalogPage(ctx context.Context, page int, category string) ([]core.Drama, bool, error) {
	return p.fetchGuipianCatalogPage(ctx, page, category, "")
}

func (p *guipianProvider) Search(ctx context.Context, page int, query string) ([]core.Drama, bool, error) {
	return p.fetchGuipianCatalogPage(ctx, page, "", query)
}

func (p *guipianProvider) Detail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	return p.fetchGuipianDetail(ctx, sourceID)
}

func (p *guipianProvider) ResolveMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	return p.resolveGuipianMedia(ctx, task)
}

const sourceGuipian = "guipian"
