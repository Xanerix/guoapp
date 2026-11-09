package core

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	xvideosSiteBaseURL = "https://www.xvideos.com"
	xvideosUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	xvideosPageSize    = 24
	xvideosMaxPages    = 10000
)

var (
	reXvideosCard        = regexp.MustCompile(`(?is)<div id="video_([a-z0-9]+)"`)
	reXvideosCardNumeric = regexp.MustCompile(`(?is)data-id="(\d+)"`)
	reXvideosCardTitle   = regexp.MustCompile(`(?is)<p class="title">(.*?)</p>`)
	reXvideosAttrTitle   = regexp.MustCompile(`(?is)title="([^"]*)"`)
	reXvideosCardCover   = regexp.MustCompile(`(?is)data-src="([^"]+)"`)
	reXvideosCardTime    = regexp.MustCompile(`(?is)<span class="duration">([^<]*)</span>`)
	reXvideosCardHD      = regexp.MustCompile(`(?is)<span class="video-hd-mark">([^<]*)</span>`)
	reXvideosPageTitle   = regexp.MustCompile(`(?is)<h2 class="page-title">(.*?)</h2>`)
	reXvideosOGTitle     = regexp.MustCompile(`(?is)property="og:title"\s+content="([^"]*)"`)
	reXvideosOGImage     = regexp.MustCompile(`(?is)property="og:image"\s+content="([^"]*)"`)
	reXvideosOGDesc      = regexp.MustCompile(`(?is)property="og:description"\s+content="([^"]*)"`)
	reXvideosHLS         = regexp.MustCompile(`(?is)html5player\.setVideoHLS\(\s*'([^']+)'`)
	reXvideosHigh        = regexp.MustCompile(`(?is)html5player\.setVideoUrlHigh\(\s*'([^']+)'`)
	reXvideosLow         = regexp.MustCompile(`(?is)html5player\.setVideoUrlLow\(\s*'([^']+)'`)
	reXvideosJSONName    = regexp.MustCompile(`(?is)"name":\s*"([^"]*)"`)
	reXvideosJSONDesc    = regexp.MustCompile(`(?is)"description":\s*"([^"]*)"`)
	reXvideosJSONTime    = regexp.MustCompile(`(?is)"duration":\s*"PT(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?"`)
	reXvideosMetaTag     = regexp.MustCompile(`(?is)<a[^>]*class="btn btn-default label[^"]*"[^>]*>(.*?)</a>`)
	reXvideosCategoryNav = regexp.MustCompile(`(?is)<a[^>]+href="/c/([A-Za-z0-9_.%\-]+-\d{1,6})"[^>]*>(.*?)</a>`)
	reXvideosTextTag     = regexp.MustCompile(`<[^>]+>`)
	reXvideosSourceID    = regexp.MustCompile(`^[a-z0-9]{4,24}-\d{4,12}$`)
	reXvideosCategoryID  = regexp.MustCompile(`^[A-Za-z0-9_.%\-]{1,64}-\d{1,6}$`)
	reXvideosCategoryNum = regexp.MustCompile(`(?is)/c/[A-Za-z0-9_.%\-]+-\d{1,6}/(\d+)`)
	reXvideosNewNum      = regexp.MustCompile(`(?is)/new/(\d+)`)
	reXvideosSearchNum   = regexp.MustCompile(`(?is)[?&]p=(\d+)`)
)

func validXvideosCategory(category string) bool {
	if category == "" {
		return true
	}
	return reXvideosCategoryID.MatchString(category)
}

func validXvideosSourceID(sourceID string) bool {
	return reXvideosSourceID.MatchString(strings.TrimSpace(sourceID))
}

func xvideosClean(text string) string {
	text = reXvideosTextTag.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u3000", " ")
	return strings.Join(strings.Fields(text), " ")
}

func xvideosDetailPath(sourceID string) (string, bool) {
	sourceID = strings.TrimSpace(sourceID)
	parts := strings.SplitN(sourceID, "-", 2)
	if len(parts) != 2 || !validXvideosSourceID(sourceID) {
		return "", false
	}
	return "/video." + parts[0] + "/" + parts[1] + "/0/", true
}

func xvideosFixURL(raw string) string {
	raw = strings.TrimSpace(html.UnescapeString(raw))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	if !strings.HasPrefix(raw, "http") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || !validNativeCoverURL(parsed) {
		return ""
	}
	return parsed.String()
}

func (d *Downloader) xvideosGet(ctx context.Context, path string) (string, error) {
	site := d.providerBaseURL(sourceXvideos)
	address := path
	if !strings.HasPrefix(address, "http") {
		address = site + path
	}
	pageContext := context.WithValue(ctx, providerTextUserAgentKey{}, xvideosUserAgent)
	return d.fetchProviderText(pageContext, address, site+"/")
}

func parseXvideosCards(body string) []Drama {
	items := make([]Drama, 0, xvideosPageSize)
	seen := map[string]bool{}
	matches := reXvideosCard.FindAllStringSubmatchIndex(body, -1)
	for index, match := range matches {
		eid := body[match[2]:match[3]]
		end := len(body)
		if index+1 < len(matches) {
			end = matches[index+1][0]
		}
		chunk := body[match[1]:end]
		numeric := ""
		if value := reXvideosCardNumeric.FindStringSubmatch(chunk); len(value) > 1 {
			numeric = value[1]
		}
		if eid == "" || numeric == "" {
			continue
		}
		sourceID := eid + "-" + numeric
		if !validXvideosSourceID(sourceID) || seen[sourceID] {
			continue
		}
		title := ""
		if block := reXvideosCardTitle.FindStringSubmatch(chunk); len(block) > 1 {
			if attribute := reXvideosAttrTitle.FindStringSubmatch(block[1]); len(attribute) > 1 {
				title = xvideosClean(attribute[1])
			}
			if title == "" {
				title = xvideosClean(reXvideosCardTime.ReplaceAllString(block[1], " "))
			}
		}
		if title == "" {
			continue
		}
		seen[sourceID] = true
		cover := ""
		if value := reXvideosCardCover.FindStringSubmatch(chunk); len(value) > 1 {
			cover = xvideosFixURL(value[1])
		}
		remark := ""
		if value := reXvideosCardTime.FindStringSubmatch(chunk); len(value) > 1 {
			remark = xvideosClean(value[1])
		}
		if hd := reXvideosCardHD.FindStringSubmatch(chunk); len(hd) > 1 && strings.TrimSpace(hd[1]) != "" {
			remark = strings.TrimSpace(remark + " " + xvideosClean(hd[1]))
		}
		items = append(items, Drama{
			ID: providerDramaID(sourceXvideos, sourceID), Source: sourceXvideos, SourceID: sourceID,
			Title: truncate(title, 512), Name: truncate(title, 512),
			Cover: cover, CoverURL: cover, ChannelName: "XVideos", CategoryName: "视频",
			Remark: truncate(remark, 64),
		})
		if len(items) >= 800 {
			break
		}
	}
	return items
}

func xvideosPageCount(body, category string, search bool, page, count int) int {
	maxPage := page
	add := func(value string) {
		number, err := strconv.Atoi(value)
		if err == nil && number > maxPage && number < xvideosMaxPages {
			maxPage = number
		}
	}
	if search {
		for _, match := range reXvideosSearchNum.FindAllStringSubmatch(body, -1) {
			add(match[1])
		}
	} else if category == "" {
		for _, match := range reXvideosNewNum.FindAllStringSubmatch(body, -1) {
			add(match[1])
		}
	} else {
		for _, match := range reXvideosCategoryNum.FindAllStringSubmatch(body, -1) {
			add(match[1])
		}
	}
	if maxPage > page {
		return maxPage
	}
	if count >= xvideosPageSize {
		return page + 1
	}
	return page
}

func (d *Downloader) fetchXvideosCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	if page < 1 || page > xvideosMaxPages {
		return nil, false, errors.New("XVideos 目录页码无效")
	}
	query = strings.TrimSpace(query)
	path := ""
	search := query != ""
	switch {
	case search:
		if len(query) > 256 {
			return nil, false, errors.New("XVideos 搜索关键词无效")
		}
		path = "/?k=" + url.QueryEscape(query)
		if page > 1 {
			path += "&p=" + strconv.Itoa(page)
		}
	case !validXvideosCategory(category):
		return nil, false, errors.New("XVideos 内容分类无效")
	case category != "":
		path = "/c/" + category
		if page > 1 {
			path += "/" + strconv.Itoa(page)
		}
	case page > 1:
		path = "/new/" + strconv.Itoa(page)
	default:
		path = "/"
	}
	body, err := d.xvideosGet(ctx, path)
	if err != nil {
		return nil, false, err
	}
	items := parseXvideosCards(body)
	if len(items) == 0 {
		return []Drama{}, false, nil
	}
	return items, page < xvideosPageCount(body, category, search, page, len(items)), nil
}

func (d *Downloader) fetchXvideosCategories(ctx context.Context) []nativeCategory {
	body, err := d.xvideosGet(ctx, "/")
	if err != nil {
		return nil
	}
	var categories []nativeCategory
	seen := map[string]bool{}
	for _, match := range reXvideosCategoryNav.FindAllStringSubmatch(body, -1) {
		id := match[1]
		name := xvideosClean(match[2])
		if !validXvideosCategory(id) || name == "" || seen[id] || len([]rune(name)) > 24 {
			continue
		}
		seen[id] = true
		categories = append(categories, nativeCategory{ID: id, Name: truncate(name, 32)})
		if len(categories) >= 48 {
			break
		}
	}
	return categories
}

func xvideosPageTitle(body string) string {
	if match := reXvideosPageTitle.FindStringSubmatch(body); len(match) > 1 {
		inner := reXvideosCardTime.ReplaceAllString(match[1], " ")
		inner = reXvideosCardHD.ReplaceAllString(inner, " ")
		if title := xvideosClean(inner); title != "" {
			return title
		}
	}
	for _, re := range []*regexp.Regexp{reXvideosOGTitle, reXvideosJSONName} {
		if match := re.FindStringSubmatch(body); len(match) > 1 {
			if title := xvideosClean(match[1]); title != "" {
				return title
			}
		}
	}
	return ""
}

func xvideosDurationText(body string) string {
	if match := reXvideosCardTime.FindStringSubmatch(body); len(match) > 1 {
		if value := xvideosClean(match[1]); value != "" {
			return value
		}
	}
	match := reXvideosJSONTime.FindStringSubmatch(body)
	if len(match) < 4 {
		return ""
	}
	hours, _ := strconv.Atoi(match[1])
	minutes, _ := strconv.Atoi(match[2])
	seconds, _ := strconv.Atoi(match[3])
	total := hours*3600 + minutes*60 + seconds
	if total <= 0 {
		return ""
	}
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

func xvideosTags(body string, title string) []string {
	tags := make([]string, 0, 12)
	seen := map[string]bool{title: true}
	for _, match := range reXvideosMetaTag.FindAllStringSubmatch(body, -1) {
		name := xvideosClean(match[1])
		if name == "" || len([]rune(name)) > 48 || seen[name] {
			continue
		}
		seen[name] = true
		tags = append(tags, name)
		if len(tags) >= 16 {
			break
		}
	}
	return tags
}

func xvideosMediaURL(body string) string {
	for _, re := range []*regexp.Regexp{reXvideosHLS, reXvideosHigh, reXvideosLow} {
		if match := re.FindStringSubmatch(body); len(match) > 1 {
			address := strings.TrimSpace(html.UnescapeString(match[1]))
			if strings.HasPrefix(address, "//") {
				address = "https:" + address
			}
			if isProviderHTTPMediaURL(address) {
				return address
			}
		}
	}
	return ""
}

func (d *Downloader) fetchXvideosDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	path, ok := xvideosDetailPath(sourceID)
	if !ok {
		return Drama{}, nil, errors.New("XVideos 视频 ID 无效")
	}
	body, err := d.xvideosGet(ctx, path)
	if err != nil {
		return Drama{}, nil, err
	}
	title := xvideosPageTitle(body)
	if title == "" {
		return Drama{}, nil, errors.New("XVideos 详情缺少视频名称")
	}
	cover := ""
	if match := reXvideosOGImage.FindStringSubmatch(body); len(match) > 1 {
		cover = xvideosFixURL(match[1])
	}
	description := ""
	for _, re := range []*regexp.Regexp{reXvideosOGDesc, reXvideosJSONDesc} {
		if match := re.FindStringSubmatch(body); len(match) > 1 {
			description = xvideosClean(match[1])
			if description != "" && description != title {
				break
			}
			description = ""
		}
	}
	remark := xvideosDurationText(body)
	pageURL := xvideosSiteBaseURL + path
	drama := Drama{
		ID: providerDramaID(sourceXvideos, sourceID), Source: sourceXvideos, SourceID: sourceID,
		Title: truncate(title, 512), Name: truncate(title, 512),
		Desc: truncate(description, 12000), Intro: truncate(description, 12000),
		Cover: cover, CoverURL: cover, ChannelName: "XVideos", CategoryName: "视频",
		Remark: truncate(remark, 64),
		Tags:   xvideosTags(body, title),
	}
	address := xvideosMediaURL(body)
	if address == "" {
		return Drama{}, nil, errors.New("XVideos 该视频暂无可用播放地址")
	}
	chapters := []Chapter{{
		ID:             providerChapterID(sourceXvideos, sourceID, "1"),
		Source:         sourceXvideos,
		Title:          "正片",
		CurrentEpisode: rawEpisode(1),
		VideoURL:       address,
		PageURL:        pageURL,
		Referer:        xvideosSiteBaseURL + "/",
	}}
	drama.TotalEpisode, drama.EpisodeCount = 1, 1
	return drama, chapters, nil
}

func (d *Downloader) resolveXvideosMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, sourceID, valid := splitProviderDramaID(task.DramaID)
	prefix := providerChapterID(sourceXvideos, sourceID, "")
	if !valid || source != sourceXvideos || !validXvideosSourceID(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return providerMedia{}, errors.New("XVideos 播放分集信息无效，请刷新详情")
	}
	medias := []string{}
	if address := strings.TrimSpace(task.Chapter.VideoURL); isProviderHTTPMediaURL(address) {
		medias = append(medias, address)
	}
	if len(medias) == 0 {
		path, ok := xvideosDetailPath(sourceID)
		if !ok {
			return providerMedia{}, errors.New("XVideos 播放页地址无效")
		}
		body, err := d.xvideosGet(ctx, path)
		if err != nil {
			return providerMedia{}, err
		}
		if address := xvideosMediaURL(body); address != "" {
			medias = append(medias, address)
		}
	}
	if len(medias) == 0 {
		return providerMedia{}, errors.New("XVideos 该视频暂无可用播放地址")
	}
	referer := firstNonEmpty(task.Chapter.Referer, xvideosSiteBaseURL+"/")
	media := providerMedia{URL: medias[0], Referer: referer}
	if strings.Contains(strings.ToLower(medias[0]), ".m3u8") {
		return d.prepareWebProviderMedia(ctx, media, "XVideos")
	}
	return media, nil
}
