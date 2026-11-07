package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const (
	heitaoBaseURL   = "https://heitaodj.com"
	heitaoUserAgent = "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	heitaoAgeCookie = "heitao_age_confirmed=1"
)

var (
	reHeitaoEpisodeHref  = regexp.MustCompile(`(?i)/index\.php/vod/play/id/(\d+)/sid/(\d+)/nid/(\d+)\.html`)
	reHeitaoPlayerRoute  = regexp.MustCompile(`(?i)/index\.php/vod/player/id/\d+/nid/(\d+)/sid/(\d+)\.html`)
	reHeitaoEpisodeCount = regexp.MustCompile(`^(\d+)集$`)
	reHeitaoSearchPage   = regexp.MustCompile(`(?i)/vod/search/page/(\d+)/wd/`)
)

type heitaoSeriesMeta struct {
	Type        string `json:"@type"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Image       any    `json:"image"`
}

type heitaoPlayerEntry struct {
	URL     string `json:"url"`
	Encrypt any    `json:"encrypt"`
}

func validHeitaoCategory(category string) bool {
	if category == "" {
		return true
	}
	return len(category) <= 6 && webProviderNumericID.MatchString(category)
}

func heitaoScriptText(node *html.Node) string {
	var builder strings.Builder
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode {
			builder.WriteString(child.Data)
		}
	}
	return strings.TrimSpace(builder.String())
}

func heitaoSeriesMetadata(document *html.Node) heitaoSeriesMeta {
	for _, script := range providerHTMLNodes(document, func(node *html.Node) bool {
		return node.Type == html.ElementNode && node.Data == "script" &&
			strings.Contains(strings.ToLower(providerHTMLAttr(node, "type")), "ld+json")
	}) {
		text := heitaoScriptText(script)
		if text == "" {
			continue
		}
		var list []heitaoSeriesMeta
		if json.Unmarshal([]byte(text), &list) == nil {
			for _, item := range list {
				if strings.EqualFold(item.Type, "TVSeries") {
					return item
				}
			}
			continue
		}
		var single heitaoSeriesMeta
		if json.Unmarshal([]byte(text), &single) == nil && strings.EqualFold(single.Type, "TVSeries") {
			return single
		}
	}
	return heitaoSeriesMeta{}
}

func heitaoCategoryName(document *html.Node) string {
	for _, node := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "li" }) {
		text := strings.TrimSpace(providerHTMLText(node))
		if strings.HasPrefix(text, "分类：") {
			if name := strings.TrimSpace(strings.TrimPrefix(text, "分类：")); name != "" {
				return name
			}
		}
	}
	return ""
}

func heitaoTags(document *html.Node) []string {
	block := providerHTMLFirstClass(document, "detail-tags")
	if block == nil {
		return nil
	}
	var tags []string
	seen := map[string]bool{}
	for _, anchor := range providerHTMLNodes(block, func(node *html.Node) bool { return node.Data == "a" }) {
		value := strings.TrimSpace(providerHTMLText(anchor))
		if value == "" || len([]rune(value)) > 48 || seen[value] {
			continue
		}
		seen[value] = true
		tags = append(tags, value)
		if len(tags) >= 32 {
			break
		}
	}
	return tags
}

func heitaoEpisodeCount(remark string) int {
	match := reHeitaoEpisodeCount.FindStringSubmatch(strings.TrimSpace(remark))
	if len(match) < 2 {
		return 0
	}
	value, err := strconv.Atoi(match[1])
	if err != nil || value < 1 || value > 100000 {
		return 0
	}
	return value
}

func (d *Downloader) fetchHeitaoCategories(ctx context.Context) ([]nativeCategory, error) {
	site := d.providerBaseURL(sourceHeitao)
	document, pageURL, err := d.fetchProviderPage(ctx, site+"/", site+"/", heitaoUserAgent)
	if err != nil {
		return nil, err
	}
	var categories []nativeCategory
	seen := map[string]bool{}
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		action, values, valid := dsdRouteParameters(pageURL, providerHTMLAttr(anchor, "href"))
		id, name := values["id"], strings.TrimSpace(providerHTMLText(anchor))
		if !valid || action != "type" || !validHeitaoCategory(id) || id == "" || seen[id] || name == "" || len([]rune(name)) > 48 {
			continue
		}
		seen[id] = true
		categories = append(categories, nativeCategory{ID: id, Name: name})
		if len(categories) >= 32 {
			break
		}
	}
	if len(categories) == 0 {
		return nil, errors.New("黑桃未返回有效的内容分类")
	}
	return categories, nil
}

func parseHeitaoCards(document *html.Node, pageURL string) []Drama {
	items := []Drama{}
	seen := map[string]bool{}
	for _, card := range providerHTMLNodes(document, func(node *html.Node) bool {
		return node.Data == "a" && providerHTMLClass(node, "thumb")
	}) {
		action, values, valid := dsdRouteParameters(pageURL, providerHTMLAttr(card, "href"))
		id := values["id"]
		if !valid || action != "detail" || !webProviderNumericID.MatchString(id) || seen[id] {
			continue
		}
		title := strings.TrimSpace(providerHTMLAttr(card, "title"))
		for _, image := range providerHTMLNodes(card, func(node *html.Node) bool { return node.Data == "img" }) {
			if title == "" {
				title = strings.TrimSpace(providerHTMLAttr(image, "alt"))
			}
			if title != "" {
				break
			}
		}
		if title == "" {
			title = strings.TrimSpace(providerHTMLText(card))
		}
		if title == "" {
			continue
		}
		seen[id] = true
		cover := ""
		for _, image := range providerHTMLNodes(card, func(node *html.Node) bool { return node.Data == "img" }) {
			for _, attribute := range []string{"src", "data-src", "data-original"} {
				if address := providerCoverAddress(providerHTMLAttr(image, attribute), pageURL); address != "" {
					cover = address
					break
				}
			}
			if cover != "" {
				break
			}
		}
		remark := ""
		if label := providerHTMLFirstClass(card, "label"); label != nil {
			remark = truncate(providerHTMLText(label), 64)
		}
		drama := Drama{
			ID: providerDramaID(sourceHeitao, id), Source: sourceHeitao, SourceID: id,
			Title: truncate(title, 512), Name: truncate(title, 512),
			Cover: cover, CoverURL: cover, ChannelName: "黑桃", CategoryName: "短剧", Remark: remark, Badge: remark,
		}
		if count := heitaoEpisodeCount(remark); count > 0 {
			drama.EpisodeCount, drama.TotalEpisode = count, count
		}
		items = append(items, drama)
		if len(items) >= 500 {
			break
		}
	}
	return items
}

func heitaoPageCount(document *html.Node, pageURL, category, query string, page int) int {
	pages := page
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		href, value := providerHTMLAttr(anchor, "href"), ""
		if query != "" {
			if match := reHeitaoSearchPage.FindStringSubmatch(href); len(match) == 2 {
				value = match[1]
			}
		} else if action, values, valid := dsdRouteParameters(pageURL, href); valid && (action == "show" || action == "type") && values["id"] == category {
			value = values["page"]
		}
		if number, err := strconv.Atoi(value); err == nil && number > pages && number < 100000 {
			pages = number
		}
	}
	return pages
}

func heitaoCatalogAddress(site string, page int, category, query string) string {
	if query != "" {
		escaped := url.PathEscape(query)
		if page <= 1 {
			return site + "/index.php/vod/search/wd/" + escaped + ".html"
		}
		return fmt.Sprintf("%s/index.php/vod/search/page/%d/wd/%s.html", site, page, escaped)
	}
	show := site + "/index.php/vod/show"
	if category != "" {
		show += "/id/" + category
	}
	if page <= 1 {
		return show + ".html"
	}
	return fmt.Sprintf("%s/page/%d.html", show, page)
}

func (d *Downloader) fetchHeitaoCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	query = strings.TrimSpace(query)
	if page < 1 || page > 100000 || len(query) > 256 || !validHeitaoCategory(category) {
		return nil, false, errors.New("黑桃目录查询参数无效")
	}
	site := d.providerBaseURL(sourceHeitao)
	address := heitaoCatalogAddress(site, page, category, query)
	document, pageURL, err := d.fetchProviderPage(ctx, address, site+"/", heitaoUserAgent)
	if err != nil {
		return nil, false, err
	}
	items := parseHeitaoCards(document, pageURL)
	pages := heitaoPageCount(document, pageURL, category, query, page)
	return items, pages > page, nil
}

func heitaoChapters(document *html.Node, site, sourceID string) []Chapter {
	type heitaoEpisode struct {
		sid   int
		nid   int
		title string
	}
	groups := map[int][]heitaoEpisode{}
	seen := map[string]bool{}
	for _, anchor := range providerHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		match := reHeitaoEpisodeHref.FindStringSubmatch(providerHTMLAttr(anchor, "href"))
		if len(match) < 4 || match[1] != sourceID {
			continue
		}
		sid, sidErr := strconv.Atoi(match[2])
		nid, nidErr := strconv.Atoi(match[3])
		if sidErr != nil || nidErr != nil || sid < 1 || nid < 1 {
			continue
		}
		key := fmt.Sprintf("%d:%d", sid, nid)
		if seen[key] {
			continue
		}
		seen[key] = true
		title := strings.TrimSpace(providerHTMLText(anchor))
		if title == "" {
			title = fmt.Sprintf("第 %d 集", nid)
		}
		groups[sid] = append(groups[sid], heitaoEpisode{sid: sid, nid: nid, title: title})
	}
	var best []heitaoEpisode
	bestSid := 0
	for sid, items := range groups {
		if len(items) > len(best) || (len(items) == len(best) && sid < bestSid) {
			best, bestSid = items, sid
		}
	}
	chapters := make([]Chapter, 0, len(best))
	for _, item := range best {
		key := fmt.Sprintf("%d:%d", item.sid, item.nid)
		chapters = append(chapters, Chapter{
			ID:             providerChapterID(sourceHeitao, sourceID, key),
			Source:         sourceHeitao,
			Title:          truncate(item.title, 128),
			CurrentEpisode: rawEpisode(item.nid),
			VideoURL:       "heitao-player://" + key,
			PageURL:        fmt.Sprintf("%s/index.php/vod/player/id/%s/nid/%d/sid/%d.html", site, sourceID, item.nid, item.sid),
			Referer:        site + "/",
		})
	}
	sortProviderChapters(chapters)
	return chapters
}

func (d *Downloader) fetchHeitaoDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	if !webProviderNumericID.MatchString(sourceID) {
		return Drama{}, nil, errors.New("黑桃视频 ID 无效")
	}
	site := d.providerBaseURL(sourceHeitao)
	address := site + "/index.php/vod/detail/id/" + sourceID + ".html"
	document, pageURL, err := d.fetchProviderPage(ctx, address, site+"/", heitaoUserAgent)
	if err != nil {
		return Drama{}, nil, err
	}
	meta := heitaoSeriesMetadata(document)
	name := strings.TrimSpace(meta.Name)
	if name == "" {
		if heading := providerHTMLFirstClass(document, "title"); heading != nil {
			name = strings.TrimSpace(providerHTMLText(heading))
		}
	}
	if name == "" {
		return Drama{}, nil, errors.New("黑桃详情缺少剧名")
	}
	chapters := heitaoChapters(document, site, sourceID)
	if len(chapters) == 0 {
		return Drama{}, nil, errors.New("黑桃该剧暂无可播放分集")
	}
	cover := providerCoverAddress(meta.Image, pageURL)
	if cover == "" {
		if block := providerHTMLFirstClass(document, "albumDetailImg"); block != nil {
			for _, image := range providerHTMLNodes(block, func(node *html.Node) bool { return node.Data == "img" }) {
				if address := providerCoverAddress(providerHTMLAttr(image, "src"), pageURL); address != "" {
					cover = address
					break
				}
			}
		}
	}
	description := truncate(meta.Description, 12000)
	drama := Drama{
		ID: providerDramaID(sourceHeitao, sourceID), Source: sourceHeitao, SourceID: sourceID,
		Title: truncate(name, 512), Name: truncate(name, 512),
		Desc: description, Intro: description,
		Cover: cover, CoverURL: cover, ChannelName: "黑桃",
		CategoryName: firstNonEmpty(heitaoCategoryName(document), "短剧"),
		Tags:         heitaoTags(document),
		TotalEpisode: len(chapters), EpisodeCount: len(chapters),
	}
	return drama, chapters, nil
}

func heitaoSeparateEpisode(key string) (int, int, bool) {
	parts := strings.Split(key, ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	sid, sidErr := strconv.Atoi(parts[0])
	nid, nidErr := strconv.Atoi(parts[1])
	if sidErr != nil || nidErr != nil || sid < 1 || nid < 1 {
		return 0, 0, false
	}
	return sid, nid, true
}

func heitaoPlayerID(raw string) (int, int, bool) {
	match := reHeitaoPlayerRoute.FindStringSubmatch(raw)
	if len(match) < 3 {
		return 0, 0, false
	}
	nid, nidErr := strconv.Atoi(match[1])
	sid, sidErr := strconv.Atoi(match[2])
	if nidErr != nil || sidErr != nil || sid < 1 || nid < 1 {
		return 0, 0, false
	}
	return sid, nid, true
}

func heitaoReadPlayerConfig(body string) (heitaoPlayerEntry, error) {
	var entry heitaoPlayerEntry
	index := strings.Index(body, "player_aaaa")
	if index < 0 {
		return entry, errors.New("黑桃播放器未返回配置")
	}
	text := body[index:]
	open := strings.Index(text, "{")
	if open < 0 {
		return entry, errors.New("黑桃播放器配置无效")
	}
	depth, inString, escaped, end := 0, false, false, -1
scan:
	for i := open; i < len(text); i++ {
		value := text[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if value == '\\' {
				escaped = true
				continue
			}
			if value == '"' {
				inString = false
			}
			continue
		}
		switch value {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
				break scan
			}
		}
	}
	if end < 0 {
		return entry, errors.New("黑桃播放器配置不完整")
	}
	if err := json.Unmarshal([]byte(text[open:end+1]), &entry); err != nil {
		return entry, fmt.Errorf("黑桃播放器配置解析失败：%w", err)
	}
	if strings.TrimSpace(entry.URL) == "" {
		return entry, errors.New("黑桃该集暂无可用播放地址")
	}
	return entry, nil
}

func heitaoDecryptMediaURL(raw string, encrypt int) (string, error) {
	raw = strings.TrimSpace(raw)
	switch encrypt {
	case 0:
		return raw, nil
	case 1:
		unescaped, err := url.PathUnescape(raw)
		if err != nil {
			return "", errors.New("黑桃播放地址解码失败")
		}
		return unescaped, nil
	case 2:
		decoded, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(raw)
		}
		if err != nil {
			return "", errors.New("黑桃播放地址解码失败")
		}
		unescaped, err := url.PathUnescape(string(decoded))
		if err != nil {
			return "", errors.New("黑桃播放地址解码失败")
		}
		return unescaped, nil
	default:
		return "", errors.New("黑桃播放地址加密方式暂不支持")
	}
}

func (d *Downloader) resolveHeitaoMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, sourceID, valid := splitProviderDramaID(task.DramaID)
	prefix := providerChapterID(sourceHeitao, sourceID, "")
	if !valid || source != sourceHeitao || !webProviderNumericID.MatchString(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return providerMedia{}, errors.New("黑桃播放分集信息无效，请刷新详情")
	}
	sid, nid, ok := heitaoSeparateEpisode(strings.TrimPrefix(task.Chapter.ID, prefix))
	if !ok {
		sid, nid, ok = heitaoPlayerID(task.Chapter.PageURL)
	}
	if !ok {
		return providerMedia{}, errors.New("黑桃播放分集信息无效，请刷新详情")
	}
	site := d.providerBaseURL(sourceHeitao)
	playerURL := fmt.Sprintf("%s/index.php/vod/player/id/%s/nid/%d/sid/%d.html", site, sourceID, nid, sid)
	playURL := fmt.Sprintf("%s/index.php/vod/play/id/%s/sid/%d/nid/%d.html", site, sourceID, sid, nid)
	pageContext := context.WithValue(ctx, providerTextUserAgentKey{}, heitaoUserAgent)
	pageContext = context.WithValue(pageContext, providerTextCookieKey{}, heitaoAgeCookie)
	body, err := d.fetchProviderText(pageContext, playerURL, playURL)
	if err != nil {
		return providerMedia{}, err
	}
	entry, err := heitaoReadPlayerConfig(body)
	if err != nil {
		return providerMedia{}, err
	}
	encrypt := 0
	switch value := entry.Encrypt.(type) {
	case float64:
		encrypt = int(value)
	case string:
		encrypt, _ = strconv.Atoi(strings.TrimSpace(value))
	case bool:
		if value {
			encrypt = 1
		}
	}
	address, err := heitaoDecryptMediaURL(entry.URL, encrypt)
	if err != nil {
		return providerMedia{}, err
	}
	address = resolveProviderURL(playerURL, address)
	if !isProviderHTTPMediaURL(address) {
		return providerMedia{}, errors.New("黑桃播放地址无效")
	}
	return d.prepareWebProviderMedia(ctx, providerMedia{URL: address, Referer: site + "/"}, "黑桃")
}
