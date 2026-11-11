package sources

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"duanjuapp/native/core"
	"golang.org/x/net/html"
)

var (
	dsdPlayerAssignment = regexp.MustCompile(`\bplayer_[a-zA-Z0-9_]+\s*=\s*`)
	dsdVideoDirectory   = regexp.MustCompile(`^/video[0-9]*/`)
	dsdPosterFilename   = regexp.MustCompile(`(?i)/1\.(?:jpg|jpeg|png|webp)$`)
	dsdScriptBlock      = regexp.MustCompile(`(?is)<script\b[^>]*>(.*?)</script>`)
	dsdVPathAssignment  = regexp.MustCompile(`\bvar\s+vPath\s*=\s*["']([^"']+)["']`)
)

func parseDSDPlayer(document *html.Node) (map[string]any, bool, error) {
	for _, script := range core.ProviderHTMLNodes(document, func(node *html.Node) bool { return node.Data == "script" }) {
		if script.FirstChild == nil {
			continue
		}
		body := script.FirstChild.Data
		for _, location := range dsdPlayerAssignment.FindAllStringIndex(body, 8) {
			decoder := json.NewDecoder(strings.NewReader(body[location[1]:]))
			decoder.UseNumber()
			var player map[string]any
			if decoder.Decode(&player) != nil || player == nil {
				return nil, true, errors.New("帝果播放器参数格式无效")
			}
			if player["id"] != nil && player["url"] != nil {
				return player, true, nil
			}
		}
	}
	return nil, false, nil
}

func dsdPlayerIdentity(player map[string]any, sourceID string) (int, int, error) {
	line, lineValid := core.WebProviderInteger(player["sid"], 1000000)
	episode, episodeValid := core.WebProviderInteger(player["nid"], 1000000)
	if core.MapString(player, "id") != sourceID || !lineValid || line < 1 || !episodeValid || episode < 1 {
		return 0, 0, errors.New("帝果播放器与请求视频不符")
	}
	return line, episode, nil
}

func dsdEpisodePage(site, sourceID string, line, episode int) string {
	return fmt.Sprintf("%s/index.php/vod/play/id/%s/sid/%d/nid/%d.html", site, sourceID, line, episode)
}

func dsdChapter(sourceID string, line, episode int, title, site string, vip bool) core.Chapter {
	key := fmt.Sprintf("%d-%d", line, episode)
	return core.Chapter{ID: core.ProviderChapterID(sourceDSD, sourceID, key), Source: sourceDSD,
		Title: core.Truncate(core.FirstNonEmpty(title, fmt.Sprintf("第 %d 集", episode)), 256), CurrentEpisode: core.RawEpisode(episode),
		PageURL: dsdEpisodePage(site, sourceID, line, episode), Referer: site + "/", VIP: vip}
}

func dsdDetailIdentity(document *html.Node, sourceID string, player map[string]any, found bool) error {
	if found {
		_, _, err := dsdPlayerIdentity(player, sourceID)
		return err
	}
	info := core.ProviderHTMLFirstClass(document, "video-info")
	for _, node := range core.ProviderHTMLNodes(info, func(node *html.Node) bool {
		return core.ProviderHTMLAttr(node, "data-id") == sourceID
	}) {
		if core.ProviderHTMLClass(node, "btn-favs") {
			return nil
		}
	}
	return errors.New("帝果未返回所请求视频的详情")
}

func parseDSDDetail(document *html.Node, pageURL, sourceID, site string) (core.Drama, []core.Chapter, error) {
	player, found, err := parseDSDPlayer(document)
	if err != nil {
		return core.Drama{}, nil, err
	}
	if err := dsdDetailIdentity(document, sourceID, player, found); err != nil {
		return core.Drama{}, nil, err
	}
	line, current := 1, 1
	if found {
		line, current, _ = dsdPlayerIdentity(player, sourceID)
	}
	info := core.ProviderHTMLFirstClass(document, "video-info")
	box := core.ProviderHTMLFirstClass(document, "video-box")
	data, _ := player["vod_data"].(map[string]any)
	title := core.MapString(data, "vod_name")
	if title == "" {
		for _, heading := range core.ProviderHTMLNodes(info, func(node *html.Node) bool {
			return node.Data == "h1" || node.Data == "h2" || node.Data == "h3"
		}) {
			if title = core.ProviderHTMLText(heading); title != "" {
				break
			}
		}
	}
	if title == "" {
		return core.Drama{}, nil, errors.New("帝果详情缺少视频名称")
	}
	cover := core.FirstNonEmpty(core.ProviderCoverAddress(player["poster"], pageURL), dsdCardCover(box, pageURL))
	description := core.Truncate(core.ProviderHTMLText(core.ProviderHTMLFirstClass(info, "desc")), 12000)
	drama := core.Drama{ID: core.ProviderDramaID(sourceDSD, sourceID), Source: sourceDSD, SourceID: sourceID,
		Title: core.Truncate(title, 1024), Name: core.Truncate(title, 1024), Desc: description, Intro: description,
		Cover: cover, CoverURL: cover, ChannelName: "帝果"}
	vip := core.ProviderHTMLFirstClass(box, "noVip", "openVip") != nil
	if vip {
		drama.VIP = &vip
	}
	seenTags := map[string]bool{}
	addTag := func(tag string) {
		tag = strings.TrimSpace(tag)
		if tag != "" && len([]rune(tag)) <= 48 && len(drama.Tags) < 32 && !seenTags[tag] {
			seenTags[tag] = true
			drama.Tags = append(drama.Tags, tag)
		}
	}
	for _, tag := range strings.Split(core.MapString(data, "vod_class"), ",") {
		addTag(tag)
	}
	for _, tag := range core.ProviderHTMLNodes(core.ProviderHTMLFirstClass(info, "labels"), func(node *html.Node) bool { return node.Data == "a" }) {
		addTag(core.ProviderHTMLText(tag))
	}
	chapters := []core.Chapter{dsdChapter(sourceID, line, current, "", site, vip)}
	seen := map[int]bool{current: true}
	addEpisode := func(reference, title string) {
		action, values, valid := dsdRouteParameters(pageURL, reference)
		candidateLine, lineValid := core.WebProviderInteger(values["sid"], 1000000)
		episode, episodeValid := core.WebProviderInteger(values["nid"], 1000000)
		if !valid || action != "play" || values["id"] != sourceID || !lineValid || candidateLine != line || !episodeValid || episode < 1 || seen[episode] {
			return
		}
		seen[episode] = true
		chapters = append(chapters, dsdChapter(sourceID, line, episode, title, site, vip))
	}
	for _, anchor := range core.ProviderHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		addEpisode(core.ProviderHTMLAttr(anchor, "href"), core.ProviderHTMLText(anchor))
	}
	addEpisode(core.MapString(player, "link_pre"), "")
	addEpisode(core.MapString(player, "link_next"), "")
	if len(chapters) > 10000 {
		return core.Drama{}, nil, errors.New("帝果分集目录过大")
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		left, _ := strconv.Atoi(chapters[i].EpisodeString(i + 1))
		right, _ := strconv.Atoi(chapters[j].EpisodeString(j + 1))
		return left < right
	})
	if len(chapters) == 1 {
		chapters[0].Title = "正片"
	}
	drama.TotalEpisode, drama.EpisodeCount = len(chapters), len(chapters)
	return drama, chapters, nil
}

func (p *dSDProvider) fetchDSDDetail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	if !core.WebProviderNumericID.MatchString(sourceID) {
		return core.Drama{}, nil, errors.New("帝果视频 ID 无效")
	}
	site := p.site
	document, address, err := core.FetchHostPage(ctx, p.host, dsdEpisodePage(site, sourceID, 1, 1), site+"/", dsdUserAgent)
	if err != nil {
		return core.Drama{}, nil, err
	}
	return parseDSDDetail(document, address, sourceID, site)
}

func dsdIssuedMedia(player map[string]any, pageURL string) (string, error) {
	address := core.MapString(player, "url")
	if address == "" {
		return "", nil
	}
	switch core.MapString(player, "encrypt") {
	case "", "0":
	case "1":
		decoded, err := url.PathUnescape(address)
		if err != nil {
			return "", errors.New("帝果播放地址编码无效")
		}
		address = decoded
	case "2":
		decoded, err := base64.StdEncoding.DecodeString(address)
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(address)
		}
		if err != nil {
			return "", errors.New("帝果播放地址编码无效")
		}
		address, err = url.PathUnescape(string(decoded))
		if err != nil {
			return "", errors.New("帝果播放地址编码无效")
		}
	default:
		return "", errors.New("帝果播放地址编码已变化")
	}
	address = core.ResolveProviderURL(pageURL, address)
	parsed, err := url.Parse(address)
	if err != nil || !core.IsProviderHTTPMediaURL(address) || parsed.User != nil {
		return "", errors.New("帝果播放地址无效")
	}
	path := strings.ToLower(parsed.Path)
	if !strings.HasSuffix(path, ".m3u8") && !strings.HasSuffix(path, ".mp4") {
		return "", errors.New("帝果暂未提供可直接播放的媒体")
	}
	return address, nil
}

func dsdSplitTopLevel(value string) []string {
	var parts []string
	depth, start := 0, 0
	for index, char := range value {
		switch char {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '+':
			if depth == 0 {
				if part := strings.TrimSpace(value[start:index]); part != "" {
					parts = append(parts, part)
				}
				start = index + 1
			}
		}
	}
	if part := strings.TrimSpace(value[start:]); part != "" {
		parts = append(parts, part)
	}
	return parts
}

type dsdExpressionParser struct {
	text  string
	index int
	depth int
}

func (parser *dsdExpressionParser) skipSpaces() {
	for parser.index < len(parser.text) && strings.ContainsRune(" \t\r\n", rune(parser.text[parser.index])) {
		parser.index++
	}
}

func (parser *dsdExpressionParser) parseExpression() (int, bool) {
	value, ok := parser.parseTerm()
	if !ok {
		return 0, false
	}
	for {
		parser.skipSpaces()
		if parser.index >= len(parser.text) || parser.text[parser.index] != '+' && parser.text[parser.index] != '-' {
			return value, true
		}
		operator := parser.text[parser.index]
		parser.index++
		right, ok := parser.parseTerm()
		if !ok {
			return 0, false
		}
		if operator == '+' {
			value += right
		} else {
			value -= right
		}
	}
}

func (parser *dsdExpressionParser) parseTerm() (int, bool) {
	value, ok := parser.parseFactor()
	if !ok {
		return 0, false
	}
	for {
		parser.skipSpaces()
		if parser.index >= len(parser.text) || !strings.ContainsRune("*/%", rune(parser.text[parser.index])) {
			return value, true
		}
		operator := parser.text[parser.index]
		parser.index++
		right, ok := parser.parseFactor()
		if !ok || right == 0 && operator != '*' {
			return 0, false
		}
		switch operator {
		case '*':
			value *= right
		case '/':
			if value%right != 0 {
				return 0, false
			}
			value /= right
		case '%':
			value %= right
		}
	}
}

func (parser *dsdExpressionParser) parseFactor() (int, bool) {
	parser.skipSpaces()
	sign := 1
	for parser.index < len(parser.text) && (parser.text[parser.index] == '+' || parser.text[parser.index] == '-') {
		if parser.text[parser.index] == '-' {
			sign = -sign
		}
		parser.index++
		parser.skipSpaces()
	}
	if parser.index >= len(parser.text) {
		return 0, false
	}
	if parser.text[parser.index] == '(' {
		if parser.depth >= 32 {
			return 0, false
		}
		parser.depth++
		parser.index++
		value, ok := parser.parseExpression()
		parser.depth--
		parser.skipSpaces()
		if !ok || parser.index >= len(parser.text) || parser.text[parser.index] != ')' {
			return 0, false
		}
		parser.index++
		return sign * value, true
	}
	start := parser.index
	for parser.index < len(parser.text) && parser.text[parser.index] >= '0' && parser.text[parser.index] <= '9' {
		parser.index++
	}
	if start == parser.index {
		return 0, false
	}
	value, err := strconv.Atoi(parser.text[start:parser.index])
	return sign * value, err == nil
}

func dsdEvaluateDigitExpression(value string) (int, bool) {
	if len(value) > 4096 || strings.Trim(value, "0123456789()+-*/% \t\r\n") != "" {
		return 0, false
	}
	parser := &dsdExpressionParser{text: value}
	result, ok := parser.parseExpression()
	parser.skipSpaces()
	return result, ok && parser.index == len(parser.text)
}

func dsdAaDigit(part string) (string, bool) {
	part = strings.TrimSpace(part)
	if part == "" {
		return "", false
	}
	if strings.Contains(part, "(oﾟｰﾟo)") {
		return "u", true
	}
	if strings.Contains(part, "(ﾟДﾟ).ﾟΘﾟﾉ") {
		return "b", true
	}
	replacements := []struct {
		old string
		new string
	}{
		{"(c^_^o)", "0"},
		{"(ﾟΘﾟ)", "1"},
		{"(o^_^o)", "3"},
		{"(ﾟｰﾟ)", "4"},
	}
	for _, replacement := range replacements {
		part = strings.ReplaceAll(part, replacement.old, replacement.new)
	}
	value, ok := dsdEvaluateDigitExpression(part)
	if !ok {
		return "", false
	}
	return strconv.Itoa(value), true
}

func decodeDSDAaencode(raw string) (string, bool) {
	if len(raw) > 1<<20 {
		return "", false
	}
	const marker = "(ﾟДﾟ) ['_'] ( (ﾟДﾟ) ['_']"
	const tailMarker = "+ (ﾟДﾟ)[ﾟoﾟ]"
	start := strings.Index(raw, marker)
	if start < 0 {
		return "", false
	}
	body := raw[start:]
	end := strings.LastIndex(body, tailMarker)
	if end < 0 {
		return "", false
	}
	tokens := strings.Split(body[:end], "(ﾟДﾟ)[ﾟεﾟ]+")
	var decoded strings.Builder
	for _, token := range tokens[1:] {
		if decoded.Len() > 64<<10 {
			return "", false
		}
		var digits strings.Builder
		for _, part := range dsdSplitTopLevel(token) {
			digit, ok := dsdAaDigit(part)
			if !ok {
				return "", false
			}
			digits.WriteString(digit)
		}
		value := digits.String()
		if strings.HasPrefix(value, "u") && len(value) == 5 {
			code, err := strconv.ParseInt(value[1:], 16, 32)
			if err != nil {
				return "", false
			}
			decoded.WriteRune(rune(code))
		} else if value != "" && strings.Trim(value, "01234567") == "" {
			code, err := strconv.ParseInt(value, 8, 32)
			if err != nil {
				return "", false
			}
			decoded.WriteRune(rune(code))
		}
	}
	return decoded.String(), true
}

func dsdSignedPathFromVplayer(body string) (string, bool) {
	for _, match := range dsdScriptBlock.FindAllStringSubmatch(body, -1) {
		if !strings.Contains(match[1], "(ﾟДﾟ)") {
			continue
		}
		decoded, ok := decodeDSDAaencode(match[1])
		if !ok {
			continue
		}
		if found := dsdVPathAssignment.FindStringSubmatch(decoded); len(found) > 1 {
			return strings.TrimSpace(found[1]), true
		}
	}
	return "", false
}

func dsdVplayerMediaPath(address string) string {
	if parsed, err := url.Parse(address); err == nil && parsed.IsAbs() {
		path := parsed.EscapedPath()
		if path == "" {
			path = "/"
		}
		if parsed.RawQuery != "" {
			path += "?" + parsed.RawQuery
		}
		return path
	}
	return address
}

func dsdVplayerReferer(site string) string {
	return strings.TrimRight(site, "/") + "/addons/vplayer/"
}

func (p *dSDProvider) signDSDMedia(ctx context.Context, address, pageURL, site string) (string, bool, error) {
	if strings.Contains(address, "sign=") {
		return address, false, nil
	}
	mediaPath := dsdVplayerMediaPath(address)
	vplayerURL := strings.TrimRight(site, "/") + "/addons/vplayer/?url=" + url.QueryEscape(mediaPath) + "&jump="
	response, err := p.host.FetchText(ctx, core.TextRequest{URL: vplayerURL, Referer: site + "/", UserAgent: dsdUserAgent})
	if err != nil {
		return "", false, err
	}
	signed, found := dsdSignedPathFromVplayer(response.Body)
	if !found || signed == "" {
		return address, false, nil
	}
	resolved := core.ResolveProviderURL(dsdVplayerReferer(site), signed)
	parsed, err := url.Parse(resolved)
	if err != nil || !core.IsProviderHTTPMediaURL(resolved) || parsed.User != nil {
		return "", false, errors.New("帝果播放签名地址无效")
	}
	path := strings.ToLower(parsed.Path)
	if !strings.HasSuffix(path, ".m3u8") && !strings.HasSuffix(path, ".mp4") {
		return "", false, errors.New("帝果签名后未返回可播放媒体")
	}
	if core.ProviderMediaOrigin(parsed) == "" || pageURL == "" {
		return "", false, errors.New("帝果播放签名缺少来源信息")
	}
	return resolved, true, nil
}

func dsdMediaFromPagePath(cover, pageURL string) string {
	address, err := url.Parse(cover)
	page, pageErr := url.Parse(pageURL)
	if err != nil || pageErr != nil || !core.IsProviderHTTPMediaURL(cover) || address.User != nil ||
		core.ProviderMediaOrigin(address) != core.ProviderMediaOrigin(page) || !dsdVideoDirectory.MatchString(address.Path) || !dsdPosterFilename.MatchString(address.Path) {
		return ""
	}
	address.Path = dsdPosterFilename.ReplaceAllString(address.Path, "/1000k/index.m3u8")
	address.RawPath, address.RawQuery, address.Fragment = "", "", ""
	return address.String()
}

func (p *dSDProvider) resolveDSDMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	source, sourceID, valid := core.SplitProviderDramaID(task.DramaID)
	prefix := core.ProviderChapterID(sourceDSD, sourceID, "")
	if !valid || source != sourceDSD || !core.WebProviderNumericID.MatchString(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return core.ProviderMedia{}, errors.New("帝果播放信息无效，请刷新详情")
	}
	parts := strings.Split(strings.TrimPrefix(task.Chapter.ID, prefix), "-")
	if len(parts) != 2 {
		return core.ProviderMedia{}, errors.New("帝果播放分集无效")
	}
	line, lineValid := core.WebProviderInteger(parts[0], 1000000)
	episode, episodeValid := core.WebProviderInteger(parts[1], 1000000)
	if !lineValid || line < 1 || !episodeValid || episode < 1 {
		return core.ProviderMedia{}, errors.New("帝果播放分集无效")
	}
	site := p.site
	document, pageURL, err := core.FetchHostPage(ctx, p.host, dsdEpisodePage(site, sourceID, line, episode), site+"/", dsdUserAgent)
	if err != nil {
		return core.ProviderMedia{}, err
	}
	player, found, err := parseDSDPlayer(document)
	if err != nil {
		return core.ProviderMedia{}, err
	}
	if err := dsdDetailIdentity(document, sourceID, player, found); err != nil {
		return core.ProviderMedia{}, err
	}
	address := ""
	if found {
		actualLine, actualEpisode, _ := dsdPlayerIdentity(player, sourceID)
		if line != actualLine || episode != actualEpisode {
			return core.ProviderMedia{}, errors.New("帝果返回的播放地址与请求分集不符")
		}
		if preview, valid := core.WebProviderInteger(player["trysee"], 86400); valid && preview > 0 {
			return core.ProviderMedia{}, errors.New("帝果仅提供试看，暂未取得正片播放地址")
		}
		address, err = dsdIssuedMedia(player, pageURL)
		if err != nil {
			return core.ProviderMedia{}, err
		}
	}
	if address == "" {
		if line != 1 || episode != 1 {
			return core.ProviderMedia{}, errors.New("帝果未提供该分集的播放地址")
		}
		box := core.ProviderHTMLFirstClass(document, "video-box")
		cover := core.FirstNonEmpty(core.ProviderCoverAddress(player["poster"], pageURL), dsdCardCover(box, pageURL))
		address = dsdMediaFromPagePath(cover, pageURL)
	}
	if address == "" {
		return core.ProviderMedia{}, errors.New("帝果未提供播放地址，请重试或确认站源权限")
	}
	signed, signedByVplayer, err := p.signDSDMedia(ctx, address, pageURL, site)
	if err != nil {
		return core.ProviderMedia{}, err
	}
	address = signed
	parsed, _ := url.Parse(address)
	referer := pageURL
	if signedByVplayer || strings.Contains(address, "sign=") {
		referer = dsdVplayerReferer(site)
	}
	media := core.ProviderMedia{URL: address, Referer: referer, Credentials: &core.MediaCredentials{
		Origin: core.ProviderMediaOrigin(parsed), Referer: referer, UserAgent: dsdUserAgent,
	}}
	return p.host.PrepareWebMedia(ctx, media, "帝果")
}

type dSDProvider struct {
	host    core.Host
	site    string
	catalog dsdCatalogState
}

var dSDDescriptor = core.SourceDescriptor{
	ID:                sourceDSD,
	Name:              "帝果",
	Description:       "分类视频 · 在线搜索",
	DefaultOff:        true,
	SkipVIPPrompt:     true,
	Aliases:           []string{"dsd.com.se", "www.dsd.com.se"},
	BaseURL:           dsdBaseURL,
	AllowEmptyCatalog: true,
	ValidCategory:     validDSDCategory,
}

func init() {
	core.Register(dSDDescriptor, func(host core.Host) core.Provider { return &dSDProvider{host: host, site: dsdBaseURL} })
}

func (p *dSDProvider) Descriptor() core.SourceDescriptor { return dSDDescriptor }

func (p *dSDProvider) BaseURL() string { return p.site }

func (p *dSDProvider) Categories(ctx context.Context, force bool) ([]core.Category, error) {
	return p.fetchDSDCategories(ctx, force)
}

func (p *dSDProvider) CatalogPage(ctx context.Context, page int, category string) ([]core.Drama, bool, error) {
	return p.fetchDSDCatalogPage(ctx, page, category, "")
}

func (p *dSDProvider) Search(ctx context.Context, page int, query string) ([]core.Drama, bool, error) {
	return p.fetchDSDCatalogPage(ctx, page, "", query)
}

func (p *dSDProvider) Detail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	return p.fetchDSDDetail(ctx, sourceID)
}

func (p *dSDProvider) ResolveMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	return p.resolveDSDMedia(ctx, task)
}

const sourceDSD = "dsd"
