package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"duanjuapp/native/core"
)

const (
	soraniLineCodeDefault = "anime_jp_m3u8"

	soraniMaxPage     = 100000
	soraniMaxCategory = 128
	soraniMaxKeyword  = 256
	soraniMaxID       = 64
)

var soraniCategories = []core.Category{
	{ID: "1", Name: "TV番剧"},
	{ID: "2", Name: "剧场动画"},
	{ID: "5", Name: "特摄剧场"},
}

func validSoraniCategory(category string) bool {
	if category == "" {
		return true
	}
	if len(category) > soraniMaxCategory || strings.ContainsAny(category, "|/\\\x00\r\n") {
		return false
	}
	for _, entry := range soraniCategories {
		if entry.ID == category {
			return true
		}
	}
	return false
}

func soraniNumericID(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > soraniMaxID {
		return false
	}
	number, err := strconv.Atoi(value)
	return err == nil && number > 0
}

func soraniCover(value any) string {
	return core.ProviderCoverAddress(value, soraniSiteBaseURL+"/")
}

func soraniDramaFromMap(row map[string]any) (core.Drama, error) {
	if row == nil {
		return core.Drama{}, errors.New("青空次元返回的剧集信息不完整")
	}
	id := core.MapString(row, "id", "videoId")
	title := core.MapString(row, "title", "name", "videoTitle")
	if !soraniNumericID(id) || strings.TrimSpace(title) == "" {
		return core.Drama{}, errors.New("青空次元返回的剧集信息不完整")
	}
	description := core.MapString(row, "summary", "description", "intro", "desc")
	cover := soraniCover(firstAnyValue(row, "cover", "coverLarge", "coverSmall", "coverThumb", "backgroundImage"))

	episodes := 0
	for _, key := range []string{"episodeCount", "totalEpisode", "episodes", "total"} {
		if value, ok := row[key]; ok {
			if count, err := strconv.Atoi(core.NativeText(value)); err == nil && count > episodes && count <= 100000 {
				episodes = count
			}
		}
	}

	category := core.MapString(row, "categoryName", "category")
	tags := make([]string, 0, 8)
	seen := map[string]bool{}
	addTag := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] || len([]rune(value)) > 48 || len(tags) >= 16 {
			return
		}
		seen[value] = true
		tags = append(tags, value)
	}
	if name := core.MapString(row, "categoryName"); name != "" && name != category {
		addTag(name)
	}
	for _, value := range core.MapStringSlice(row, "tags", "tagList", "categoryTags") {
		addTag(value)
	}
	if alias := core.MapString(row, "alias"); alias != "" {
		addTag(core.Truncate(alias, 48))
	}
	if area := core.MapString(row, "area"); area != "" {
		addTag(core.Truncate(area, 48))
	}
	if year := core.MapString(row, "year", "publishDate"); year != "" {
		if digits := leadingYear(year); digits != "" {
			addTag(digits + "年")
		}
	}
	if actor := core.MapString(row, "actors", "actor"); actor != "" {
		addTag("声优 " + core.Truncate(actor, 32))
	}
	if category == "" {
		category = "番剧"
	}
	return core.Drama{
		ID: core.ProviderDramaID(sourceSorani, id), Source: sourceSorani, SourceID: id,
		Title: core.Truncate(title, 256), Name: core.Truncate(title, 256),
		Desc: core.Truncate(description, 12000), Intro: core.Truncate(description, 12000),
		Cover: cover, CoverURL: cover,
		TotalEpisode: episodes, EpisodeCount: episodes,
		Category: category, ChannelName: "青空次元",
		Tags: tags, Score: core.MapString(row, "bangumiRating", "score"),
	}, nil
}

func leadingYear(value string) string {
	value = strings.TrimSpace(value)
	if len(value) < 4 {
		return ""
	}
	digits := value[:4]
	for _, r := range digits {
		if r < '0' || r > '9' {
			return ""
		}
	}
	year, err := strconv.Atoi(digits)
	if err != nil || year < 1900 || year > 2200 {
		return ""
	}
	return digits
}

func firstAnyValue(row map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := row[key]; ok && core.NativeText(value) != "" {
			return value
		}
	}
	return nil
}

func soraniPayload(value any) any {
	row, ok := value.(map[string]any)
	if !ok {
		return value
	}
	if data, exists := row["data"]; exists {
		return data
	}
	return value
}

func soraniRecordList(payload any) ([]any, bool) {
	switch value := payload.(type) {
	case []any:
		return value, true
	case map[string]any:
		for _, key := range []string{"records", "list", "items", "rows"} {
			if rows, ok := value[key].([]any); ok {
				return rows, true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}

func (p *soraniProvider) soraniRequest(ctx context.Context, route string, query url.Values, limit int64) (any, error) {
	return p.client.get(ctx, route, query, limit)
}

func (p *soraniProvider) fetchSoraniCategories() []core.Category {
	return append([]core.Category(nil), soraniCategories...)
}

func (p *soraniProvider) fetchSoraniCatalogPage(ctx context.Context, page int, category, query string) ([]core.Drama, bool, error) {
	if page < 1 || page > soraniMaxPage {
		return nil, false, errors.New("青空次元目录页码无效")
	}
	if !validSoraniCategory(category) {
		return nil, false, errors.New("青空次元内容分类无效")
	}
	if len(query) > soraniMaxKeyword {
		return nil, false, errors.New("青空次元搜索关键词过长")
	}
	const pageSize = 24
	values := url.Values{"page": {strconv.Itoa(page)}, "size": {strconv.Itoa(pageSize)}}
	route := "/video"
	switch {
	case query != "":
		route = "/video/search"
		values.Set("keyword", query)
	case category != "":
		values.Set("categoryId", category)
	}
	value, err := p.soraniRequest(ctx, route, values, 4<<20)
	if err != nil {
		return nil, false, err
	}
	payload := soraniPayload(value)
	rows, ok := soraniRecordList(payload)
	if !ok || len(rows) > 500 {
		return nil, false, errors.New("青空次元目录数据格式无效")
	}
	hasMore := len(rows) >= pageSize
	if meta, ok := payload.(map[string]any); ok {
		if total, err := strconv.Atoi(core.NativeText(meta["total"])); err == nil && total >= 0 && total <= 100000000 {
			hasMore = page*pageSize < total
		}
	}
	if hasMore && len(rows) == 0 {
		return nil, false, errors.New("青空次元未返回应有的目录页，请重试")
	}
	items := make([]core.Drama, 0, len(rows))
	seen := map[string]bool{}
	for _, entry := range rows {
		row, _ := entry.(map[string]any)
		drama, err := soraniDramaFromMap(row)
		if err != nil {
			return nil, false, err
		}
		if seen[drama.ID] {
			return nil, false, errors.New("青空次元目录包含重复剧集，请重试")
		}
		seen[drama.ID] = true
		items = append(items, drama)
	}
	return items, hasMore, nil
}

func (p *soraniProvider) fetchSoraniDetail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	if !soraniNumericID(sourceID) {
		return core.Drama{}, nil, errors.New("青空次元剧集 ID 无效")
	}
	value, err := p.soraniRequest(ctx, "/video/"+url.PathEscape(sourceID), nil, 8<<20)
	if err != nil {
		return core.Drama{}, nil, err
	}
	row, _ := soraniPayload(value).(map[string]any)
	if row == nil {
		return core.Drama{}, nil, errors.New("青空次元详情数据格式无效")
	}
	drama, err := soraniDramaFromMap(row)
	if err != nil {
		return core.Drama{}, nil, err
	}
	if drama.SourceID != sourceID {
		return core.Drama{}, nil, errors.New("青空次元详情与请求剧集不符")
	}
	rows, ok := row["episodes"].([]any)
	if !ok || len(rows) > 10000 {
		return core.Drama{}, nil, errors.New("青空次元分集目录格式无效")
	}
	chapters := make([]core.Chapter, 0, len(rows))
	seenIDs, seenOrders := map[string]bool{}, map[int]bool{}
	for index, entry := range rows {
		episode, _ := entry.(map[string]any)
		if episode == nil {
			continue
		}
		if enabled, ok := episode["enabled"].(bool); ok && !enabled {
			continue
		}
		episodeID := core.MapString(episode, "episodeId", "id")
		if !soraniNumericID(episodeID) || seenIDs[episodeID] {
			return core.Drama{}, nil, errors.New("青空次元分集目录包含无效或重复分集")
		}
		order := index + 1
		if episode["episodeOrder"] != nil {
			if parsed, err := strconv.Atoi(core.NativeText(episode["episodeOrder"])); err == nil && parsed > 0 && parsed <= 100000 {
				order = parsed
			}
		}
		if seenOrders[order] {
			return core.Drama{}, nil, errors.New("青空次元分集编号重复，请刷新目录")
		}
		seenIDs[episodeID], seenOrders[order] = true, true
		title := core.MapString(episode, "episodeLabel", "title", "displayTitle")
		if title == "" {
			title = fmt.Sprintf("第 %d 集", order)
		}
		chapters = append(chapters, core.Chapter{
			ID: core.ProviderChapterID(sourceSorani, sourceID, episodeID), Source: sourceSorani,
			Title: core.Truncate(title, 128), CurrentEpisode: json.RawMessage(strconv.Itoa(order)),
			VideoURL: "sorani-play://" + url.PathEscape(episodeID),
			Referer:  soraniSiteBaseURL + "/",
		})
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		left, _ := strconv.Atoi(chapters[i].EpisodeString(i + 1))
		right, _ := strconv.Atoi(chapters[j].EpisodeString(j + 1))
		if left == right {
			return chapters[i].ID < chapters[j].ID
		}
		return left < right
	})
	return drama, chapters, nil
}

func (client *soraniAPIClient) play(ctx context.Context, episodeID, lineCode, token string) (map[string]any, error) {
	value, err := client.call(ctx, http.MethodGet, "/video/episode/"+url.PathEscape(episodeID)+"/play",
		url.Values{"lineCode": {lineCode}}, nil, token, 64<<10)
	if err != nil {
		return nil, err
	}
	row, _ := soraniPayload(value).(map[string]any)
	return row, nil
}

func soraniLoginRejected(row map[string]any) bool {
	canPlay, ok := row["canPlay"].(bool)
	return ok && !canPlay && strings.Contains(core.MapString(row, "message"), "登录")
}

func (p *soraniProvider) resolveSoraniMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	source, sourceID, valid := core.SplitProviderDramaID(task.DramaID)
	prefix := core.ProviderChapterID(sourceSorani, sourceID, "")
	if !valid || source != sourceSorani || !soraniNumericID(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return core.ProviderMedia{}, errors.New("青空次元播放分集信息无效，请刷新详情")
	}
	episodeID := strings.TrimPrefix(task.Chapter.ID, prefix)
	if !soraniNumericID(episodeID) {
		return core.ProviderMedia{}, errors.New("青空次元分集 ID 无效")
	}
	lineCode := soraniLineCodeDefault
	client := p.client
	token, err := client.accessToken(ctx)
	if err != nil {
		return core.ProviderMedia{}, err
	}
	row, err := client.play(ctx, episodeID, lineCode, token)
	if err == nil && token != "" && soraniLoginRejected(row) {
		if token, err = client.renew(ctx, token); err == nil {
			row, err = client.play(ctx, episodeID, lineCode, token)
		}
	}
	if err != nil {
		return core.ProviderMedia{}, err
	}
	if canPlay, ok := row["canPlay"].(bool); ok && !canPlay {
		switch {
		case soraniLoginRejected(row) && token == "":
			return core.ProviderMedia{}, errSoraniLoginRequired
		case soraniLoginRejected(row):
			return core.ProviderMedia{}, errSoraniLoginExpired
		case core.MapString(row, "message") != "":
			return core.ProviderMedia{}, errors.New("青空次元：" + core.Truncate(core.MapString(row, "message"), 120))
		default:
			return core.ProviderMedia{}, errors.New("青空次元该分集暂不可播放")
		}
	}
	address := core.MapString(row, "playUrl", "url")
	if address == "" {
		return core.ProviderMedia{}, fmt.Errorf("青空次元该分集暂无可用播放地址（线路 %s）", lineCode)
	}
	media := core.ProviderMedia{URL: address, Referer: soraniSiteBaseURL + "/"}
	parsed, err := url.Parse(address)
	if err != nil || !core.IsProviderHTTPMediaURL(address) {
		return core.ProviderMedia{}, errors.New("青空次元播放地址无效")
	}
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		playlist, finalURL, err := p.host.FetchPlaylist(ctx, media.URL, media.Referer, nil)
		if err != nil {
			return core.ProviderMedia{}, fmt.Errorf("获取青空次元播放列表失败：%w", err)
		}
		if !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(playlist, "\ufeff")), "#EXTM3U") {
			return core.ProviderMedia{}, errors.New("青空次元未返回有效 M3U8，链接可能已失效")
		}
		media.Playlist, media.URL = playlist, finalURL
		media.Duration = core.M3u8Duration(playlist)
	}
	return media, nil
}

type soraniProvider struct {
	host   core.Host
	client *soraniAPIClient
}

var soraniDescriptor = core.SourceDescriptor{
	ID:                sourceSorani,
	Name:              "青空",
	Description:       "番剧 · 剧场动画 · 特摄",
	Aliases:           []string{"sorani.net", "www.sorani.net", "api.sorani.cc", "sorani.cc"},
	BaseURL:           soraniSiteBaseURL,
	AllowEmptyCatalog: true,
	ValidCategory:     validSoraniCategory,
}

func init() {
	core.Register(soraniDescriptor, func(host core.Host) core.Provider {
		return &soraniProvider{host: host, client: &soraniAPIClient{host: host, base: soraniAPIBaseURL, site: soraniSiteBaseURL}}
	})
}

func (p *soraniProvider) Descriptor() core.SourceDescriptor { return soraniDescriptor }

func (p *soraniProvider) Categories(ctx context.Context, force bool) ([]core.Category, error) {
	return p.fetchSoraniCategories(), nil
}

func (p *soraniProvider) CatalogPage(ctx context.Context, page int, category string) ([]core.Drama, bool, error) {
	return p.fetchSoraniCatalogPage(ctx, page, category, "")
}

func (p *soraniProvider) Search(ctx context.Context, page int, query string) ([]core.Drama, bool, error) {
	return p.fetchSoraniCatalogPage(ctx, page, "", query)
}

func (p *soraniProvider) Detail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	return p.fetchSoraniDetail(ctx, sourceID)
}

func (p *soraniProvider) ResolveMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	return p.resolveSoraniMedia(ctx, task)
}

func (p *soraniProvider) Account(ctx context.Context, command string, payload json.RawMessage) (any, error) {
	var input soraniAccountInput
	if len(payload) > 0 && string(payload) != "null" {
		if err := json.Unmarshal(payload, &input); err != nil {
			return nil, errors.New("账号参数无效")
		}
	}
	return p.soraniAccount(ctx, command, input)
}

const sourceSorani = "sorani"
