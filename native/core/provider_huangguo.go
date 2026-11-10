package core

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"errors"
	"fmt"
	"html"
	"net/url"

	"regexp"
	"sort"
	"strconv"
	"strings"
)

const sourceHuangguoVideo = "huangguo-video"

var huangguoVideoBaseURL = "https://huangguo.video"

var reVideoCard = regexp.MustCompile(`(?is)<article\b[^>]*class=["'][^"']*\bvideo-card\b[^"']*["'][^>]*>.*?</article>`)

var reVideoCardMeta = regexp.MustCompile(`(?is)<p\b[^>]*class=["'][^"']*\bfont-mono-meta\b[^"']*["'][^>]*>(.*?)</p>`)

var reVideoDetailMeta = regexp.MustCompile(`(?is)</h1>.*?<p\b[^>]*class=["'][^"']*\bfont-mono-meta\b[^"']*["'][^>]*>(.*?)</p>`)

var reAnyArticle = regexp.MustCompile(`(?is)<article\b[^>]*>.*?</article>`)

var reHuangguoLink = regexp.MustCompile(`(?is)<a\b[^>]*href=["']([^"']*/(?:series|video)/([^"'/?#]+)[^"']*)["'][^>]*>.*?</a>`)

var reResolution = regexp.MustCompile(`(?i)RESOLUTION\s*=\s*(\d+)x(\d+)`)

var reBandwidth = regexp.MustCompile(`(?i)(?:AVERAGE-)?BANDWIDTH\s*=\s*(\d+)`)

var reDataHLS = regexp.MustCompile(`(?is)data-hls=["']([^"']+)["']`)

type providerEpisode struct {
	Key   string
	Title string
	URL   string
	Index int
	HLS   string
}

func (p *huangguoVideoProvider) fetchHuangguoVideoDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	sourceID = strings.Trim(strings.TrimSpace(sourceID), "/")
	if sourceID == "" {
		return Drama{}, nil, fmt.Errorf("empty huangguo-video sourceID")
	}
	detailPath := sourceID
	if !strings.HasPrefix(detailPath, "series/") && !strings.HasPrefix(detailPath, "video/") {
		detailPath = "series/" + detailPath
	}
	detailURL := strings.TrimRight(huangguoVideoBaseURL, "/") + "/" + detailPath
	body, err := p.text(ctx, detailURL, huangguoVideoBaseURL+"/")
	if err != nil {
		return Drama{}, nil, err
	}
	drama := huangguoDetailMetadata(body, detailURL, sourceHuangguoVideo, sourceID)
	title := drama.DisplayTitle()
	var episodes []providerEpisode
	if strings.HasPrefix(detailPath, "video/") {
		if hls := parseDataHLS(body, detailURL); hls != "" {
			episodes = []providerEpisode{{Key: "1", Title: title, URL: detailURL, Index: 1, HLS: hls}}
		}
	} else {
		episodes = parseHuangguoVideoEpisodes(body, detailURL)
	}
	if len(episodes) == 0 {
		if hls := parseDataHLS(body, detailURL); hls != "" {
			episodes = []providerEpisode{{Key: "1", Title: title, URL: detailURL, Index: 1, HLS: hls}}
		}
	}
	var chapters []Chapter
	for _, ep := range episodes {
		hlsURL := ep.HLS
		idx := ep.Index
		if idx <= 0 {
			idx = len(chapters) + 1
		}
		chapterTitle := strings.TrimSpace(ep.Title)
		if chapterTitle == "" {
			chapterTitle = fmt.Sprintf("第%d集", idx)
		}
		key := ep.Key
		if key == "" {
			key = strconv.Itoa(idx)
		}
		chapters = append(chapters, Chapter{ID: providerChapterID(sourceHuangguoVideo, sourceID, key), Source: sourceHuangguoVideo, Title: chapterTitle, VideoURL: hlsURL, PageURL: ep.URL, CurrentEpisode: rawEpisode(idx)})
	}
	sortProviderChapters(chapters)
	return drama, uniqueChapters(chapters), nil
}

func parseHuangguoVideoCards(rawHTML, pageURL string) []Drama {
	blocks := reVideoCard.FindAllString(rawHTML, -1)
	if len(blocks) == 0 {
		for _, block := range reAnyArticle.FindAllString(rawHTML, -1) {
			if strings.Contains(strings.ToLower(block), "video-card") {
				blocks = append(blocks, block)
			}
		}
	}
	seen := map[string]bool{}
	var out []Drama
	for _, block := range blocks {
		link := reHuangguoLink.FindStringSubmatch(block)
		if len(link) < 3 {
			continue
		}
		path, sourceID := huangguoVideoSourceID(link[1])
		if sourceID == "" || seen[sourceID] {
			continue
		}
		seen[sourceID] = true
		title := firstNonEmpty(extractClosestAttr(block, 0, len(block), "title", "alt", "aria-label"), cleanText(block))
		cover := resolveProviderURL(pageURL, extractClosestAttr(block, 0, len(block), "data-src", "data-original", "src"))
		category := "video"
		if strings.HasPrefix(path, "series/") {
			category = "series"
		}
		desc := firstNonEmpty(extractClosestAttr(block, 0, len(block), "data-description", "description"), extractDescription(block))
		status := ""
		if category == "series" {
			if meta := reVideoCardMeta.FindStringSubmatch(block); len(meta) > 1 {
				status = releaseStatusFromRemark(cleanText(meta[1]))
			}
		}
		out = append(out, Drama{ID: providerDramaID(sourceHuangguoVideo, sourceID), Source: sourceHuangguoVideo, SourceID: sourceID, Title: title, Name: title, Desc: desc, Intro: desc, Cover: cover, CoverURL: cover, CategoryName: category, ChannelName: "huangguo.video", Remark: firstNonEmpty(extractByClassText(block, "bg-black/55"), extractByClassText(block, "text-gold-dim")), ReleaseStatus: status})
	}
	return out
}

func parseHuangguoVideoEpisodes(rawHTML, pageURL string) []providerEpisode {
	blocks := reVideoCard.FindAllString(rawHTML, -1)
	if len(blocks) == 0 {
		blocks = reHuangguoLink.FindAllString(rawHTML, -1)
	}
	positions := map[string]int{}
	var episodes []providerEpisode
	numbered := false
	for _, block := range blocks {
		link := reHuangguoLink.FindStringSubmatch(block)
		if len(link) < 2 {
			continue
		}
		path, key := huangguoVideoSourceID(link[1])
		if key == "" || !strings.HasPrefix(path, "video/") {
			continue
		}
		fullURL := resolveProviderURL(pageURL, link[1])
		title := firstNonEmpty(extractAttr(block, "title"), extractAttr(block, "alt"), cleanText(block))
		index := episodeIndex(cleanText(block), episodeIndex(title, 0))
		numbered = numbered || index > 0
		episode := providerEpisode{Key: key, Title: title, URL: fullURL, Index: index, HLS: parseDataHLS(block, pageURL)}
		if position, found := positions[fullURL]; found {
			if episodes[position].Index <= 0 && index > 0 {
				episodes[position] = episode
			}
		} else {
			positions[fullURL] = len(episodes)
			episodes = append(episodes, episode)
		}
	}
	var result []providerEpisode
	used := map[int]bool{}
	for _, episode := range episodes {
		if numbered && episode.Index <= 0 {
			continue
		}
		episode.Index = nextUnusedEpisodeIndex(episode.Index, used)
		used[episode.Index] = true
		result = append(result, episode)
	}
	sortProviderEpisodes(result)
	return result
}

func parseDataHLS(rawHTML, pageURL string) string {
	if m := reDataHLS.FindStringSubmatch(rawHTML); len(m) > 1 {
		return normalizeProviderMediaURL(pageURL, m[1])
	}
	return ""
}

func sortProviderEpisodes(episodes []providerEpisode) {
	sort.SliceStable(episodes, func(i, j int) bool {
		if episodes[i].Index != episodes[j].Index {
			return episodes[i].Index < episodes[j].Index
		}
		return episodes[i].Key < episodes[j].Key
	})
}

func nextUnusedEpisodeIndex(preferred int, used map[int]bool) int {
	if preferred <= 0 {
		preferred = 1
	}
	if !used[preferred] {
		return preferred
	}
	for i := 1; ; i++ {
		if !used[i] {
			return i
		}
	}
}

func huangguoVideoSourceID(href string) (path, sourceID string) {
	u, err := url.Parse(html.UnescapeString(href))
	if err == nil {
		path = strings.Trim(u.Path, "/")
	} else {
		path = strings.Trim(href, "/")
	}
	parts := strings.Split(path, "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "series" || parts[i] == "video" {
			code := cleanID(parts[i+1])
			if code == "" {
				return "", ""
			}
			return parts[i] + "/" + code, parts[i] + "/" + code
		}
	}
	return "", ""
}

func detailIDFromString(s string) string {
	u, err := url.Parse(html.UnescapeString(s))
	path := s
	if err == nil {
		path = u.Path
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "detail" {
			return cleanID(parts[i+1])
		}
	}
	return ""
}

func cleanID(s string) string {
	s = strings.TrimSpace(html.UnescapeString(s))
	s = strings.Trim(s, " /\t\r\n\"'")
	if q := strings.IndexAny(s, "?#"); q >= 0 {
		s = s[:q]
	}
	return s
}

func extractByClassText(raw, className string) string {
	if className == "" {
		return ""
	}
	re := regexp.MustCompile(`(?is)<[^>]+class=["'][^"']*` + regexp.QuoteMeta(className) + `[^"']*["'][^>]*>(.*?)</[^>]+>`)
	if m := re.FindStringSubmatch(raw); len(m) > 1 {
		return cleanText(m[1])
	}
	return ""
}

func contextBlock(s string, start, end, padding int) string {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	lo := start - padding
	if lo < 0 {
		lo = 0
	}
	hi := end + padding
	if hi > len(s) {
		hi = len(s)
	}
	return s[lo:hi]
}

func extractClosestAttr(raw string, start, end int, names ...string) string {
	if start < 0 {
		start = 0
	}
	if end < start {
		end = start
	}
	if end > len(raw) {
		end = len(raw)
	}
	for _, value := range []string{raw[start:end], contextBlock(raw, start, end, 600)} {
		if value == "" {
			continue
		}
		if attr := extractAttr(value, names...); attr != "" {
			return attr
		}
	}
	return ""
}

func titleNearDetail(raw, sourceID string) string {
	needle := "/detail/" + sourceID
	idx := strings.Index(raw, needle)
	if idx < 0 {
		return ""
	}
	return extractByClassText(contextBlock(raw, idx, idx+len(needle), 1800), "hg-drama-card__title")
}

type huangguoPreviewSession struct {
	mu      sync.Mutex
	token   string
	expires time.Time
	pending chan struct{}
}

type huangguoVideoProvider struct {
	host            Host
	site            string
	previewMu       sync.Mutex
	previewSessions map[string]*huangguoPreviewSession
}

func (p *huangguoVideoProvider) AuthorizesMedia(address *url.URL) bool {
	if address == nil || !strings.HasPrefix(address.Path, "/api/hls_key/") {
		return false
	}
	configured, err := url.Parse(p.site)
	return strings.EqualFold(address.Hostname(), "huangguo.video") || err == nil && strings.EqualFold(address.Host, configured.Host)
}

func (p *huangguoVideoProvider) MediaAuthorization(ctx context.Context, address *url.URL, referer string) (string, string, error) {
	token, err := p.previewToken(ctx, address.Scheme+"://"+address.Host, referer)
	return "X-Preview-Token", token, err
}

func (p *huangguoVideoProvider) RejectMediaAuthorization(address *url.URL, value string) {
	session := p.previewSession(address.Scheme + "://" + address.Host)
	session.mu.Lock()
	if session.token == value {
		session.token = ""
	}
	session.mu.Unlock()
}

func (p *huangguoVideoProvider) previewSession(origin string) *huangguoPreviewSession {
	p.previewMu.Lock()
	defer p.previewMu.Unlock()
	if p.previewSessions == nil {
		p.previewSessions = map[string]*huangguoPreviewSession{}
	}
	if p.previewSessions[origin] == nil {
		p.previewSessions[origin] = &huangguoPreviewSession{}
	}
	return p.previewSessions[origin]
}

func (p *huangguoVideoProvider) previewToken(ctx context.Context, origin, referer string) (string, error) {
	session := p.previewSession(origin)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		session.mu.Lock()
		if session.token != "" && time.Now().Add(15*time.Second).Before(session.expires) {
			token := session.token
			session.mu.Unlock()
			return token, nil
		}
		if pending := session.pending; pending != nil {
			session.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		pending := make(chan struct{})
		session.pending = pending
		session.mu.Unlock()
		token, expiry, err := p.fetchPreviewToken(ctx, origin, referer)
		session.mu.Lock()
		if err == nil {
			session.token, session.expires = token, expiry
		}
		session.pending = nil
		close(pending)
		session.mu.Unlock()
		return token, err
	}
}

func (p *huangguoVideoProvider) fetchPreviewToken(ctx context.Context, origin, referer string) (string, time.Time, error) {
	header := map[string]string{
		"User-Agent": userAgent, "Referer": referer, "Accept": "application/json", "Accept-Language": "zh-CN,zh;q=0.9",
		"Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty",
	}
	if page, err := url.Parse(referer); err == nil && page.Host != "" {
		header["Origin"] = page.Scheme + "://" + page.Host
	}
	response, err := p.host.Fetch(ctx, HTTPRequest{Label: "黄果视频", Method: http.MethodGet, URL: origin + "/api/preview-token", Header: header, Limit: 16 << 10, Timeout: providerTimeout, FollowRedirects: true})
	if err != nil {
		return "", time.Time{}, err
	}
	var result struct {
		OK        bool   `json:"ok"`
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
		ExpiresIn int64  `json:"expires_in"`
	}
	if response.Status != http.StatusOK || json.Unmarshal(response.Body, &result) != nil || !result.OK || result.Token == "" || !validLegacyToken(result.Token) {
		return "", time.Time{}, errors.New("黄果视频未返回有效播放凭证，请稍后重试")
	}
	expiry := time.Now().Add(time.Duration(max(30, min(result.ExpiresIn, 1800))) * time.Second)
	if result.ExpiresAt > 0 {
		expiry = time.Unix(result.ExpiresAt, 0)
	}
	if !expiry.After(time.Now()) {
		return "", time.Time{}, errors.New("黄果视频播放凭证已过期，请检查设备时间")
	}
	return result.Token, expiry, nil
}

func (p *huangguoVideoProvider) text(ctx context.Context, address, referer string) (string, error) {
	response, err := p.host.FetchText(ctx, TextRequest{URL: address, Referer: referer})
	return response.Body, err
}

var huangguoVideoDescriptor = SourceDescriptor{
	ID:                 sourceHuangguoVideo,
	Name:               "黄果视频",
	Description:        "视频剧集",
	Group:              "huangguo",
	GroupName:          "黄果",
	EntryName:          "视频",
	DefaultOff:         true,
	Aliases:            []string{"huangguo.video"},
	BrowserFingerprint: true,
	BaseURL:            huangguoVideoBaseURL,
	ValidCategory: func(category string) bool {
		id, err := strconv.Atoi(category)
		return err == nil && id > 0
	},
}

func init() {
	Register(huangguoVideoDescriptor, func(host Host) Provider { return &huangguoVideoProvider{host: host, site: huangguoVideoBaseURL} })
}

func (p *huangguoVideoProvider) Descriptor() SourceDescriptor { return huangguoVideoDescriptor }

func (p *huangguoVideoProvider) BaseURL() string {
	return p.site
}

func (p *huangguoVideoProvider) Categories(ctx context.Context, force bool) ([]Category, error) {
	address := p.site + "/videos"
	body, err := p.text(ctx, address, p.site+"/")
	if err != nil {
		return nil, err
	}
	return parseHuangguoVideoCategories(body), nil
}

func (p *huangguoVideoProvider) CatalogPage(ctx context.Context, page int, category string) ([]Drama, bool, error) {
	address := fmt.Sprintf("%s/videos?page=%d", p.site, page)
	if category != "" {
		address += "&category=" + url.QueryEscape(category)
	}
	body, err := p.text(ctx, address, p.site+"/")
	if err != nil {
		return nil, false, err
	}
	items := parseHuangguoVideoCards(body, address)
	return items, len(items) >= 20, nil
}

func (p *huangguoVideoProvider) Detail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	return p.fetchHuangguoVideoDetail(ctx, sourceID)
}

func (p *huangguoVideoProvider) ResolveMedia(ctx context.Context, task Task) (ProviderMedia, error) {
	chapter := task.Chapter
	if chapter.PageURL == "" {
		if _, sourceID, valid := splitProviderDramaID(task.DramaID); valid {
			_, chapters, err := p.Detail(ctx, sourceID)
			if err != nil {
				return ProviderMedia{}, fmt.Errorf("刷新旧任务播放地址失败: %w", err)
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
				return ProviderMedia{}, fmt.Errorf("原章节已变化，请更新该合集后重新下载")
			}
		}
	}
	media := ProviderMedia{URL: chapter.VideoURL, Referer: firstNonEmpty(chapter.Referer, p.site+"/")}
	if chapter.PageURL != "" {
		response, err := p.host.FetchText(ctx, TextRequest{URL: chapter.PageURL, Referer: media.Referer})
		if err != nil {
			return ProviderMedia{}, err
		}
		media.URL = parseDataHLS(response.Body, response.URL)
		media.Referer = response.URL
	}
	return p.host.FinishMedia(ctx, media, sourceHuangguoVideo)
}

func (p *huangguoVideoProvider) CoverAddress(ctx context.Context, drama LibraryDrama, id string) (string, error) {
	parts := strings.Split(id, "/")
	if len(parts) == 1 {
		parts = []string{"series", id}
	}
	if len(parts) != 2 || (parts[0] != "series" && parts[0] != "video") || !rankingSourceID.MatchString(parts[1]) {
		return "", errors.New("无效的黄果剧集 ID")
	}
	base := p.site
	pageURL := base + "/" + parts[0] + "/" + url.PathEscape(parts[1])
	body, err := p.text(ctx, pageURL, base+"/")
	if err != nil {
		return "", err
	}
	fresh, err := parseHuangguoSortDetail(body, pageURL, Drama{ID: drama.ID, Source: sourceHuangguoVideo, SourceID: id})
	return nativeNormalize(fresh).Cover, err
}

var categoryAnchorPattern = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`)

var categoryTextPattern = regexp.MustCompile(`(?s)<[^>]*>`)

func parseHuangguoVideoCategories(body string) []Category {
	var categories []Category
	seen := map[string]bool{}
	for _, match := range categoryAnchorPattern.FindAllStringSubmatch(body, -1) {
		href := html.UnescapeString(extractAttr("<a "+match[1]+">", "href"))
		address, err := url.Parse(href)
		if err != nil || address.Path != "" && strings.TrimRight(address.Path, "/") != "/videos" {
			continue
		}
		id := address.Query().Get("category")
		name := strings.TrimSpace(html.UnescapeString(categoryTextPattern.ReplaceAllString(match[2], "")))
		if !validNativeCategory(sourceHuangguoVideo, id) || id == "" || seen[id] || name == "" || len([]rune(name)) > 24 {
			continue
		}
		seen[id] = true
		categories = append(categories, Category{ID: id, Name: name})
	}
	return categories
}
