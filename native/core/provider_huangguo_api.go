package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

const huangguoAISearchPageSize = 20

func (p *huangguoAIProvider) huangguoAIAPI(ctx context.Context, path string, out any) error {
	envelope, err := json.Marshal(map[string]string{"method": http.MethodGet, "path": path, "body": "", "content_type": ""})
	if err != nil {
		return err
	}
	base := p.site()
	response, err := p.host.FetchText(ctx, TextRequest{URL: base + "/api/edge/request", Referer: base + "/", Body: envelope})
	if err != nil {
		return err
	}
	body := response.Body
	var reply struct {
		Status int             `json:"status"`
		Msg    string          `json:"msg"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil {
		return fmt.Errorf("黄果接口返回格式无效：%w", err)
	}
	if reply.Status != 1 {
		return fmt.Errorf("黄果接口返回失败：%s", firstNonEmpty(reply.Msg, "未知错误"))
	}
	if err := json.Unmarshal(reply.Data, out); err != nil {
		return fmt.Errorf("黄果接口数据格式已变化：%w", err)
	}
	return nil
}

func (p *huangguoAIProvider) fetchHuangguoAIDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	sourceID = strings.TrimPrefix(strings.Trim(strings.TrimSpace(sourceID), "/"), "detail/")
	if !rankingSourceID.MatchString(sourceID) {
		return Drama{}, nil, errors.New("无效的黄果剧集 ID")
	}
	var detail map[string]any
	if err := p.huangguoAIAPI(ctx, "/api/videos/"+url.PathEscape(sourceID), &detail); err != nil {
		return Drama{}, nil, err
	}
	base := p.site()
	drama, ok := dramaFromAIMap(detail, base+"/", "")
	if !ok || drama.SourceID != sourceID {
		return Drama{}, nil, errors.New("黄果详情与请求剧集不符")
	}
	rows, _ := detail["episodes"].([]any)
	var chapters []Chapter
	for _, value := range rows {
		row, _ := value.(map[string]any)
		episode, err := strconv.Atoi(mapString(row, "ep_num"))
		if err != nil || episode < 1 {
			continue
		}
		chapters = append(chapters, huangguoAIChapter(base, sourceID, episode, mapString(row, "title")))
	}
	if len(rows) == 0 {
		chapters = append(chapters, huangguoAIChapter(base, sourceID, 1, drama.DisplayTitle()))
	}
	sortProviderChapters(chapters)
	return drama, uniqueChapters(chapters), nil
}

func huangguoAIChapter(base, sourceID string, episode int, title string) Chapter {
	key, pageURL := "auto-1", base+"/video/"+sourceID+"/"
	if episode > 1 {
		key = "ep-" + strconv.Itoa(episode)
		pageURL += key + "/"
	}
	return Chapter{ID: providerChapterID(sourceHuangguoAI, sourceID, key), Source: sourceHuangguoAI,
		Title: firstNonEmpty(title, fmt.Sprintf("第%d集", episode)), PageURL: pageURL, CurrentEpisode: rawEpisode(episode)}
}

func (p *huangguoAIProvider) fetchHuangguoAIPlayURL(ctx context.Context, sourceID string, episode int) (string, error) {
	if !rankingSourceID.MatchString(sourceID) || episode < 1 {
		return "", errors.New("黄果集数无效")
	}
	var play struct {
		VideoURL string `json:"video_url"`
	}
	path := fmt.Sprintf("/api/videos/%s/play?ep=%d&page_texts=0", url.PathEscape(sourceID), episode)
	if err := p.huangguoAIAPI(ctx, path, &play); err != nil {
		return "", err
	}
	return normalizeProviderMediaURL(p.site()+"/", play.VideoURL), nil
}

func (p *huangguoAIProvider) searchHuangguoAI(ctx context.Context, page int, query string) ([]Drama, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(query) > 256 {
		return nil, false, errors.New("黄果搜索关键词无效")
	}
	var result struct {
		Items      json.RawMessage `json:"items"`
		Pagination struct {
			Pages int `json:"pages"`
		} `json:"pagination"`
	}
	path := "/api/search?" + url.Values{"q": {query}, "page": {strconv.Itoa(page)}, "size": {strconv.Itoa(huangguoAISearchPageSize)}}.Encode()
	if err := p.huangguoAIAPI(ctx, path, &result); err != nil {
		return nil, false, err
	}
	items := parseHuangguoAIJSONCards(result.Items, p.site()+"/", "")
	return items, page < result.Pagination.Pages, nil
}

func parseHuangguoRanking(rows []map[string]any, pageURL string) (RankingPage, error) {
	failure := errors.New("黄果未返回有效榜单")
	if len(rows) == 0 || len(rows) > 100 {
		return RankingPage{}, failure
	}
	result := RankingPage{Items: make([]RankingItem, 0, len(rows)), TotalPages: 1}
	seen := map[string]bool{}
	for index, row := range rows {
		row["id"] = row["video_id"]
		drama, ok := dramaFromAIMap(row, pageURL, mapString(row, "cate_name"))
		if !ok || !rankingSourceID.MatchString(drama.SourceID) || seen[drama.SourceID] {
			return RankingPage{}, failure
		}
		seen[drama.SourceID] = true
		rank, err := strconv.Atoi(mapString(row, "rank"))
		if err != nil || rank < 1 {
			rank = index + 1
		}
		result.Items = append(result.Items, RankingItem{Rank: rank, Drama: drama, Metric: huangguoRankingMetric(row)})
	}
	return result, nil
}

func huangguoRankingMetric(row map[string]any) string {
	label := mapString(row, "metric_label")
	value, err := strconv.ParseFloat(mapString(row, "metric_value"), 64)
	if label == "" || err != nil {
		return ""
	}
	if value >= 10000 {
		return label + " " + strconv.FormatFloat(value/10000, 'f', 1, 64) + "万"
	}
	return label + " " + strconv.FormatFloat(value, 'f', -1, 64)
}

type huangguoAIProvider struct {
	host       Host
	configured string
}

func (p *huangguoAIProvider) site() string {
	return strings.TrimRight(firstNonEmpty(p.configured, huangguoAIBaseURL), "/")
}

func (p *huangguoAIProvider) URLCandidates(rawURL string) []string {
	return providerURLCandidates(rawURL)
}

var huangguoAIDescriptor = SourceDescriptor{
	ID:          sourceHuangguoAI,
	Name:        "黄果 AI",
	Description: "AI 短剧",
	Group:       "huangguo",
	GroupName:   "黄果",
	EntryName:   "AI",
	DefaultOff:  true,
	Aliases:     []string{"huangguo", "huangguoai.com"},
	MatchesHost: func(host string) bool { return host == "huangguoai.com" || isHuangguoAIMirrorHost(host) },
	BaseURL:     huangguoAIBaseURL,
	ValidCategory: func(category string) bool {
		for _, entry := range nativeAICategories {
			if category == entry.ID {
				return true
			}
		}
		return false
	},
	Rankings: []RankingBoard{
		{ID: "huangguo-hot", Source: sourceHuangguoAI, Name: "热播榜", Description: "黄果站点热播 TOP 20；每日更新。", path: "hot"},
		{ID: "huangguo-recommend", Source: sourceHuangguoAI, Name: "推荐榜", Description: "黄果站点推荐 TOP 20。", path: "recommend"},
		{ID: "huangguo-potential", Source: sourceHuangguoAI, Name: "潜力榜", Description: "黄果站点潜力 TOP 20。", path: "potential"},
	},
}

func init() {
	Register(huangguoAIDescriptor, func(host Host) Provider { return &huangguoAIProvider{host: host} })
}

func (p *huangguoAIProvider) Descriptor() SourceDescriptor { return huangguoAIDescriptor }

func (p *huangguoAIProvider) BaseURL() string {
	return firstNonEmpty(p.configured, huangguoAIBaseURL)
}

func (p *huangguoAIProvider) Categories(context.Context, bool) ([]Category, error) {
	return append([]Category(nil), nativeAICategories...), nil
}

func (p *huangguoAIProvider) CatalogPage(ctx context.Context, page int, category string) ([]Drama, bool, error) {
	return p.fetchHuangguoAICatalogPage(ctx, page, category)
}

func (p *huangguoAIProvider) Search(ctx context.Context, page int, query string) ([]Drama, bool, error) {
	return p.searchHuangguoAI(ctx, page, query)
}

func (p *huangguoAIProvider) Detail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	return p.fetchHuangguoAIDetail(ctx, sourceID)
}

func (p *huangguoAIProvider) ResolveMedia(ctx context.Context, task Task) (ProviderMedia, error) {
	chapter := task.Chapter
	_, sourceID, valid := splitProviderDramaID(task.DramaID)
	if !valid {
		return ProviderMedia{}, fmt.Errorf("黄果剧集信息无效")
	}
	address, err := p.fetchHuangguoAIPlayURL(ctx, sourceID, chapterEpisodeNumber(chapter, task.Index))
	if err != nil {
		return ProviderMedia{}, err
	}
	media := ProviderMedia{URL: address, Referer: firstNonEmpty(chapter.Referer, p.site()+"/")}
	return p.host.FinishMedia(ctx, media, sourceHuangguoAI)
}

func (p *huangguoAIProvider) RankingPage(ctx context.Context, board RankingBoard, page int) (RankingPage, error) {
	if page != 1 {
		return RankingPage{}, errors.New("该榜单只有一页")
	}
	var ranking struct {
		Items []map[string]any `json:"items"`
	}
	if err := p.huangguoAIAPI(ctx, "/api/ranks/"+board.path, &ranking); err != nil {
		return RankingPage{}, err
	}
	result, err := parseHuangguoRanking(ranking.Items, p.site()+"/")
	if err != nil {
		return RankingPage{}, fmt.Errorf("%s: %w", board.Name, err)
	}
	return result, nil
}

var nativeAICategories = []Category{
	{ID: "ai-duanju", Name: "AI 短剧"},
	{ID: "ai-manju", Name: "AI 漫剧"},
	{ID: "ai-huanlian", Name: "AI 换脸"},
	{ID: "ai-mogai", Name: "AI 魔改"},
}

func (p *huangguoAIProvider) fetchHuangguoAICatalogPage(ctx context.Context, page int, category string) ([]Drama, bool, error) {
	categories := nativeAICategories
	if category != "" {
		for _, entry := range categories {
			if entry.ID == category {
				categories = []Category{entry}
				break
			}
		}
	}
	type categoryResult struct {
		items []Drama
		err   error
	}
	results := make([]categoryResult, len(categories))
	slots := make(chan struct{}, 2)
	var group sync.WaitGroup
	for index, entry := range categories {
		group.Add(1)
		go func(index int, entry Category) {
			defer group.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				results[index].err = ctx.Err()
				return
			}
			path := fmt.Sprintf("/api/videos/category/%s?sort=hot&page=%d&size=24", url.PathEscape(entry.ID), page)
			var body json.RawMessage
			err := p.huangguoAIAPI(ctx, path, &body)
			if err == nil {
				results[index].items = parseHuangguoAIJSONCards(body, p.site()+"/", entry.Name)
			}
			if err != nil {
				results[index].err = fmt.Errorf("%s: %w", entry.Name, err)
			}
		}(index, entry)
	}
	group.Wait()
	var items []Drama
	var failures []error
	more := false
	for _, result := range results {
		items = append(items, result.items...)
		more = more || len(result.items) >= 24 || result.err != nil
		if result.err != nil {
			failures = append(failures, result.err)
		}
	}
	return items, more, errors.Join(failures...)
}

var huangguoAIBaseURL = "https://huangguoai.com"

var huangguoAIBaseMirrors = []string{"https://huangguoai.com", "https://thu.lbvrhyir.cc", "https://pku.xxbgwypx.cc", "https://ttvoij.ediayikma.cc", "https://thu.ediayikma.cc", "https://pku.ediayikma.cc", "https://fdu.ediayikma.cc", "https://thu.agdkczeyx.cc"}

var huangguoAIMirrorDomains = []string{"lbvrhyir.cc", "xxbgwypx.cc", "ediayikma.cc", "agdkczeyx.cc"}

var huangguoAISlugs = []string{"recommend", "newest", "ai-duanju", "ai-manju", "ai-huanlian", "ai-mogai", "ranks/hot"}

func providerURLCandidates(rawURL string) []string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() != "huangguoai.com" {
		return []string{rawURL}
	}
	out := make([]string, 0, len(huangguoAIBaseMirrors)+8)
	seen := map[string]bool{}
	add := func(candidate string) {
		if candidate == "" || seen[candidate] {
			return
		}
		seen[candidate] = true
		out = append(out, candidate)
	}
	for i, mirror := range huangguoAIBaseMirrors {
		if i == 1 {
			for _, generated := range generatedHuangguoAIMirrors(rawURL, 8) {
				add(rehostProviderURL(u, generated))
			}
		}
		add(rehostProviderURL(u, mirror))
	}
	if len(out) == 0 {
		return []string{rawURL}
	}
	return out
}

func generatedHuangguoAIMirrors(seed string, n int) []string {
	if n <= 0 {
		return nil
	}
	out := make([]string, 0, n)
	seen := map[string]bool{}
	for i := 0; len(out) < n && i < n*4; i++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", seed, i)))
		hexText := fmt.Sprintf("%x", sum[:])
		sub := strings.TrimLeft(hexText[:6], "0123456789")
		if len(sub) < 4 {
			sub = hexText[6:12]
		}
		if len(sub) > 6 {
			sub = sub[:6]
		}
		if len(sub) < 3 || seen[sub] {
			continue
		}
		seen[sub] = true
		out = append(out, "https://"+sub+"."+huangguoAIMirrorDomains[len(out)%len(huangguoAIMirrorDomains)])
	}
	return out
}

func parseHuangguoAIJSONCards(raw []byte, pageURL, category string) []Drama {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []Drama
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				walk(item)
			}
		case map[string]any:
			if dr, ok := dramaFromAIMap(x, pageURL, category); ok {
				if !seen[dr.SourceID] {
					seen[dr.SourceID] = true
					out = append(out, dr)
				}
				return
			}
			for _, child := range x {
				walk(child)
			}
		}
	}
	walk(decoded)
	return out
}

func dramaFromAIMap(m map[string]any, pageURL, category string) (Drama, bool) {
	sourceID := firstNonEmpty(mapString(m, "id"), mapString(m, "videoId"), mapString(m, "video_id"), mapString(m, "vid"), mapString(m, "slug"))
	if sourceID == "" {
		for _, v := range m {
			if s, ok := v.(string); ok {
				if id := detailIDFromString(s); id != "" {
					sourceID = id
					break
				}
			}
		}
	}
	if detailID := detailIDFromString(sourceID); detailID != "" {
		sourceID = detailID
	}
	sourceID = cleanID(sourceID)
	if sourceID == "" {
		return Drama{}, false
	}
	title := firstNonEmpty(mapString(m, "title"), mapString(m, "name"), mapString(m, "videoTitle"), mapString(m, "video_title"), mapString(m, "videoName"), mapString(m, "video_name"))
	if huangguoTitleNeedsRepair(title, sourceID) {
		return Drama{}, false
	}
	cover := resolveProviderURL(pageURL, firstNonEmpty(mapString(m, "cover"), mapString(m, "coverUrl"), mapString(m, "cover_url"), mapString(m, "coverImage"), mapString(m, "cover_image"), mapString(m, "image"), mapString(m, "imageUrl"), mapString(m, "thumbnail"), mapString(m, "poster"), mapString(m, "posterUrl")))
	desc := firstNonEmpty(mapString(m, "desc"), mapString(m, "description"), mapString(m, "intro"), mapString(m, "summary"))
	eps := huangguoMapEpisodes(m)
	status := ""
	switch strings.ToLower(mapString(m, "is_finished")) {
	case "true", "1":
		status = "finished"
	case "false", "0":
		status = "ongoing"
	}
	remark := firstNonEmpty(mapString(m, "vod_remarks"), mapString(m, "remark"), mapString(m, "remarks"))
	if remark == "" && eps != "" {
		if status == "finished" {
			remark = "全" + eps + "集"
		} else {
			remark = "更新至 " + eps + " 集"
		}
	}
	score := firstNonEmpty(mapString(m, "score"), mapString(m, "rating"))
	views := normalizeViews(firstNonEmpty(mapString(m, "views"), mapString(m, "view_count"), mapString(m, "viewCount"), mapString(m, "view_num"), mapString(m, "viewNum"), mapString(m, "views_text"), mapString(m, "view_count_text"), mapString(m, "play_count"), mapString(m, "playCount"), mapString(m, "play_num"), mapString(m, "playNum"), mapString(m, "play_count_text"), mapString(m, "hits"), mapString(m, "hit_count"), mapString(m, "plays"), mapString(m, "play")))
	online := providerReleaseDate(firstNonEmpty(mapString(m, "onlineDate"), mapString(m, "online_date"), mapString(m, "online_time"), mapString(m, "onlineTime"), mapString(m, "publish_date"), mapString(m, "publishDate"), mapString(m, "publish_time"), mapString(m, "publishTime"), mapString(m, "release_date"), mapString(m, "releaseDate")))
	tags := mapStringSlice(m, "tags", "tag", "categories", "category")
	return Drama{ID: providerDramaID(sourceHuangguoAI, sourceID), Source: sourceHuangguoAI, SourceID: sourceID, Title: title, Name: title, Desc: desc, Intro: desc, Cover: cover, CoverURL: cover, TotalEpisode: eps, EpisodeCount: eps, CategoryName: category, ChannelName: "huangguoai.com", Remark: remark, Score: score, Views: views, OnlineDate: online, Tags: tags, ReleaseStatus: status}, true
}

const sourceHuangguoAI = "huangguoai"

func isHuangguoAIMirrorHost(host string) bool {
	for _, domain := range huangguoAIMirrorDomains {
		if strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}
