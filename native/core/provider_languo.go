package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var languoCategories = []nativeCategory{
	{ID: "duanju-hot", Name: "短剧热播"},
	{ID: "duanju-original", Name: "蓝果原创"},
	{ID: "duanju-ai", Name: "AI 短剧"},
	{ID: "duanju-urban", Name: "都市"},
	{ID: "duanju-youth", Name: "青春"},
	{ID: "gv", Name: "男同GV"},
	{ID: "gv-xiaoxianrou", Name: "GV 小鲜肉"},
	{ID: "gv-zhunudiaojiao", Name: "GV 主奴调教"},
}

var languoCategoryPaths = map[string]string{
	"":                 "/duanju/",
	"duanju":           "/duanju/",
	"duanju-hot":       "/duanju/hot/",
	"duanju-original":  "/duanju/tags/original/",
	"duanju-ai":        "/duanju/tags/ai-drama/",
	"duanju-urban":     "/duanju/tags/urban/",
	"duanju-youth":     "/duanju/tags/youth/",
	"gv":               "/gv/",
	"gv-xiaoxianrou":   "/gv/tags/xiaoxianrou/",
	"gv-zhunudiaojiao": "/gv/tags/zhunudiaojiao/",
}

var (
	reLanguoCard        = regexp.MustCompile(`(?is)<article\b[^>]*class=["'][^"']*\bll-card\b[^"']*["'][^>]*>.*?</article>`)
	reLanguoVideoHref   = regexp.MustCompile(`(?is)href=["'](?:https?://[^"']+)?/video/([0-9]{1,18})/?["']`)
	reLanguoH3          = regexp.MustCompile(`(?is)<h3[^>]*>(.*?)</h3>`)
	reLanguoTagBox      = regexp.MustCompile(`(?is)<div\b[^>]*class=["'][^"']*\bll-card__tags\b[^"']*["'][^>]*>(.*?)</div>`)
	reLanguoAnchor      = regexp.MustCompile(`(?is)<a\b[^>]*>(.*?)</a>`)
	reLanguoMainTag     = regexp.MustCompile(`(?is)<main\b[^>]*\bdata-vid=["'][^"']*["'][^>]*>`)
	reLanguoInitialData = regexp.MustCompile(`(?is)<script\b[^>]*id=["']videoInitialData["'][^>]*>(.*?)</script>`)
	reLanguoEpisodeHref = regexp.MustCompile(`(?is)/video/[0-9]{1,18}/ep-([0-9]{1,6})/`)
	reLanguoCrumb       = regexp.MustCompile(`(?is)<nav\b[^>]*class=["'][^"']*\blw-crumb\b[^"']*["'][^>]*>(.*?)</nav>`)
	reLanguoOgTitle     = regexp.MustCompile(`(?is)<meta\b[^>]*property=["']og:title["'][^>]*content=["']([^"']*)["']`)
	reLanguoNextPage    = regexp.MustCompile(`(?is)<a\b[^>]*(?:aria-label=["']下一页["']|rel=["']next["'])[^>]*>`)
)

func validLanguoCategory(category string) bool {
	if category == "" {
		return true
	}
	if len(category) > 32 || strings.ContainsAny(category, "|/\\\x00\r\n") {
		return false
	}
	_, found := languoCategoryPaths[category]
	return found
}

func languoCategoryName(category string) string {
	for _, entry := range languoCategories {
		if entry.ID == category {
			return entry.Name
		}
	}
	return "男同短剧"
}

func languoCatalogPath(category string, page int) (string, bool) {
	path, found := languoCategoryPaths[category]
	if !found {
		return "", false
	}
	if page <= 1 {
		return path, true
	}
	if category == "gv" || strings.HasPrefix(category, "gv-") {
		return "", false
	}
	return strings.TrimRight(path, "/") + "/" + strconv.Itoa(page) + "/", true
}

func (d *Downloader) fetchLanguoCatalogPage(ctx context.Context, page int, category string) ([]Drama, bool, error) {
	path, found := languoCatalogPath(category, page)
	if !found {
		return nil, false, nil
	}
	site := d.providerBaseURL(sourceLanguo)
	address := site + path
	body, err := d.fetchProviderText(ctx, address, site+"/")
	if err != nil {
		return nil, false, err
	}
	items := parseLanguoCards(body, address, languoCategoryName(category))
	return items, languoHasNextPage(body), nil
}

func (d *Downloader) fetchLanguoSearchPage(ctx context.Context, page int, query string) ([]Drama, bool, error) {
	query = strings.TrimSpace(query)
	if page > 1 || query == "" {
		return nil, false, nil
	}
	site := d.providerBaseURL(sourceLanguo)
	escaped := url.PathEscape(query)
	channels := []struct {
		path string
		name string
	}{
		{"/search/video/" + escaped + "/", "男同短剧"},
		{"/search/gv/" + escaped + "/", "男同GV"},
	}
	var items []Drama
	var failures []error
	succeeded := false
	for _, channel := range channels {
		address := site + channel.path
		body, err := d.fetchProviderText(ctx, address, site+"/")
		if err != nil {
			failures = append(failures, err)
			continue
		}
		succeeded = true
		items = append(items, parseLanguoCards(body, address, channel.name)...)
	}
	if !succeeded {
		return nil, false, errors.Join(failures...)
	}
	seen := map[string]bool{}
	result := make([]Drama, 0, len(items))
	for _, drama := range items {
		if drama.ID == "" || seen[drama.ID] {
			continue
		}
		seen[drama.ID] = true
		result = append(result, drama)
	}
	return result, false, nil
}

func parseLanguoCards(rawHTML, pageURL, category string) []Drama {
	positions := map[string]int{}
	var out []Drama
	add := func(drama Drama) {
		if drama.ID == "" || drama.SourceID == "" {
			return
		}
		if position, found := positions[drama.SourceID]; found {
			out[position] = mergeDramaMetadata(out[position], drama)
			return
		}
		positions[drama.SourceID] = len(out)
		out = append(out, drama)
	}
	for _, block := range reLanguoCard.FindAllString(rawHTML, -1) {
		if strings.Contains(block, "ll-card--skeleton") {
			continue
		}
		sourceID := cleanID(extractAttr(block, "data-track-id"))
		if sourceID == "" {
			if match := reLanguoVideoHref.FindStringSubmatch(block); len(match) > 1 {
				sourceID = cleanID(match[1])
			}
		}
		if !webProviderNumericID.MatchString(sourceID) {
			continue
		}
		title := cleanText(extractAttr(block, "data-track-title"))
		if title == "" {
			if match := reLanguoH3.FindStringSubmatch(block); len(match) > 1 {
				title = cleanText(match[1])
			}
		}
		if title == "" {
			title = sourceID
		}
		cover := resolveProviderURL(pageURL, firstNonEmpty(extractAttr(block, "data-src"), extractAttr(block, "data-original"), extractAttr(block, "src")))
		score := extractByClassText(block, "ll-card__score")
		episode := extractByClassText(block, "ll-card__ep")
		remark := firstNonEmpty(episode, extractByClassText(block, "ll-card__badge"), "在线观看")
		tags := languoCardTags(block)
		drama := Drama{
			ID: providerDramaID(sourceLanguo, sourceID), Source: sourceLanguo, SourceID: sourceID,
			Title: title, Name: title, Cover: cover, CoverURL: cover,
			CategoryName: category, ChannelName: "蓝果短剧", Remark: truncate(remark, 64),
			Score: score, Tags: tags,
		}
		if count := episodeIndex(episode, 0); count > 0 {
			drama.TotalEpisode, drama.EpisodeCount = count, count
		}
		add(drama)
	}
	return out
}

func languoCardTags(block string) []string {
	match := reLanguoTagBox.FindStringSubmatch(block)
	if len(match) < 2 {
		return nil
	}
	var tags []string
	seen := map[string]bool{}
	for _, anchor := range reLanguoAnchor.FindAllStringSubmatch(match[1], -1) {
		if len(anchor) < 2 {
			continue
		}
		tag := cleanText(anchor[1])
		if tag != "" && !seen[tag] {
			seen[tag] = true
			tags = append(tags, tag)
		}
	}
	return tags
}

func languoHasNextPage(body string) bool {
	return reLanguoNextPage.MatchString(body)
}

func (d *Downloader) fetchLanguoDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	sourceID = languoVideoID(sourceID)
	if sourceID == "" {
		return Drama{}, nil, errors.New("无效的蓝果剧集 ID")
	}
	detailURL := languoEpisodeURL(sourceID, 1)
	body, err := d.fetchProviderText(ctx, detailURL, languoBaseURL+"/")
	if err != nil {
		return Drama{}, nil, err
	}
	if data := languoInitialData(body); data != nil {
		if id := mapString(data, "id"); id != "" && id != sourceID {
			return Drama{}, nil, errors.New("蓝果详情与请求剧集不符")
		}
	}
	drama := languoDetailMetadata(body, detailURL, sourceID)
	chapters := languoChapters(body, sourceID)
	if len(chapters) == 0 {
		return drama, nil, nil
	}
	return drama, chapters, nil
}

func languoVideoID(raw string) string {
	raw = strings.TrimSpace(html.UnescapeString(raw))
	if match := regexp.MustCompile(`/video/([0-9]{1,18})`).FindStringSubmatch(raw); len(match) > 1 {
		return match[1]
	}
	raw = strings.Trim(raw, "/")
	raw = strings.TrimPrefix(raw, "video/")
	raw = strings.Trim(raw, "/")
	if !webProviderNumericID.MatchString(raw) {
		return ""
	}
	return raw
}

func languoEpisodeURL(sourceID string, episode int) string {
	base := strings.TrimRight(languoBaseURL, "/") + "/video/" + sourceID + "/"
	if episode <= 1 {
		return base
	}
	return base + "ep-" + strconv.Itoa(episode) + "/"
}

func languoDetailMetadata(body, pageURL, sourceID string) Drama {
	data := languoInitialData(body)
	title := languoCleanTitle(firstNonEmpty(mapString(data, "title"), languoTitleFromPage(body)))
	description := firstNonEmpty(mapString(data, "description"), extractDescription(body))
	cover := resolveProviderURL(pageURL, firstNonEmpty(mapString(data, "coverSrc", "posterSrc"), languoCoverFromPage(body)))
	views := normalizeViews(mapString(data, "views"))
	online := providerReleaseDate(mapString(data, "time"))
	tags := mapStringSlice(data, "tags")
	category := languoBreadcrumbCategory(body)
	total := languoEpisodeTotal(body)
	if total <= 0 {
		if sources, ok := data["epPlaySrcs"].(map[string]any); ok && len(sources) > 0 {
			total = len(sources)
		}
	}
	drama := Drama{
		ID: providerDramaID(sourceLanguo, sourceID), Source: sourceLanguo, SourceID: sourceID,
		Title: title, Name: title, Desc: description, Intro: description,
		Cover: cover, CoverURL: cover, Views: views, OnlineDate: online, Tags: tags,
		CategoryName: category, ChannelName: "蓝果短剧",
	}
	if title == "" {
		drama.Title, drama.Name = "蓝果短剧", "蓝果短剧"
	}
	if total > 0 {
		drama.TotalEpisode, drama.EpisodeCount = total, total
		drama.Remark = fmt.Sprintf("更新至 %d 集", total)
	}
	return drama
}

func languoInitialData(body string) map[string]any {
	match := reLanguoInitialData.FindStringSubmatch(body)
	if len(match) < 2 {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(match[1])), &data); err != nil {
		return nil
	}
	return data
}

func languoTitleFromPage(body string) string {
	if match := reLanguoOgTitle.FindStringSubmatch(body); len(match) > 1 {
		return cleanText(match[1])
	}
	return extractPageTitle(body)
}

func languoCleanTitle(title string) string {
	title = cleanText(title)
	for _, suffix := range []string{" - 蓝果短剧", "-蓝果短剧", "_蓝果短剧"} {
		title = strings.TrimSuffix(title, suffix)
	}
	return strings.TrimSpace(title)
}

func languoCoverFromPage(body string) string {
	if match := regexp.MustCompile(`(?is)<meta\b[^>]*property=["']og:image["'][^>]*content=["']([^"']+)["']`).FindStringSubmatch(body); len(match) > 1 {
		return match[1]
	}
	return ""
}

func languoBreadcrumbCategory(body string) string {
	match := reLanguoCrumb.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	anchors := reLanguoAnchor.FindAllStringSubmatch(match[1], -1)
	for index := len(anchors) - 1; index >= 0; index-- {
		if len(anchors[index]) < 2 {
			continue
		}
		name := cleanText(anchors[index][1])
		if name != "" && name != "首页" {
			return name
		}
	}
	return ""
}

func languoEpisodeTotal(body string) int {
	if match := reLanguoMainTag.FindStringSubmatch(body); len(match) > 0 {
		if value, ok := webProviderInteger(extractAttr(match[0], "data-total"), 5000); ok && value > 0 {
			return value
		}
	}
	maxEpisode := 0
	for _, match := range reLanguoEpisodeHref.FindAllStringSubmatch(body, -1) {
		if len(match) > 1 {
			if value, err := strconv.Atoi(match[1]); err == nil && value > maxEpisode {
				maxEpisode = value
			}
		}
	}
	return maxEpisode
}

func languoChapters(body, sourceID string) []Chapter {
	total := languoEpisodeTotal(body)
	if total <= 0 {
		total = 1
	}
	chapters := make([]Chapter, 0, total)
	for episode := 1; episode <= total; episode++ {
		key := strconv.Itoa(episode)
		chapters = append(chapters, Chapter{
			ID: providerChapterID(sourceLanguo, sourceID, key), Source: sourceLanguo,
			Title: fmt.Sprintf("第%d集", episode), PageURL: languoEpisodeURL(sourceID, episode),
			CurrentEpisode: rawEpisode(episode),
		})
	}
	sortProviderChapters(chapters)
	return chapters
}

var (
	reLanguoPlaySrc     = regexp.MustCompile(`(?is)data-play-src=["']([^"']+)["']`)
	reLanguoMediaField  = regexp.MustCompile(`(?is)["']?(?:videoSrc|videoUrl|playUrl|src|url)["']?\s*[:=]\s*["']([^"']+\.(?:m3u8|mp4)(?:[^"']*)?)["']`)
	reLanguoEpisodePath = regexp.MustCompile(`/ep-(\d+)(?:/|$)`)
)

func languoPlaybackURL(rawHTML, pageURL string) string {
	if data := languoInitialData(rawHTML); data != nil {
		if media := normalizeProviderMediaURL(pageURL, mapString(data, "videoSrc", "videoUrl", "playUrl")); media != "" {
			return media
		}
		if episodes, ok := data["epPlaySrcs"].(map[string]any); ok && len(episodes) > 0 {
			preferred := firstNonEmpty(mapString(data, "ep", "episode"))
			if match := reLanguoEpisodePath.FindStringSubmatch(pageURL); len(match) > 1 {
				preferred = match[1]
			}
			if preferred != "" {
				if media := normalizeProviderMediaURL(pageURL, fmt.Sprint(episodes[preferred])); media != "" {
					return media
				}
			}
			keys := make([]string, 0, len(episodes))
			for key := range episodes {
				keys = append(keys, key)
			}
			sort.SliceStable(keys, func(i, j int) bool {
				return episodeIndex(keys[i], i+1) < episodeIndex(keys[j], j+1)
			})
			for _, key := range keys {
				if media := normalizeProviderMediaURL(pageURL, fmt.Sprint(episodes[key])); media != "" {
					return media
				}
			}
		}
	}
	if match := reLanguoPlaySrc.FindStringSubmatch(rawHTML); len(match) > 1 {
		if media := normalizeProviderMediaURL(pageURL, match[1]); media != "" {
			return media
		}
	}
	for _, match := range reLanguoMediaField.FindAllStringSubmatch(rawHTML, -1) {
		if len(match) > 1 {
			if media := normalizeProviderMediaURL(pageURL, match[1]); media != "" {
				return media
			}
		}
	}
	return ""
}
