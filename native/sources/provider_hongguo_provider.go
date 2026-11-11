package sources

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"duanjuapp/native/core"
)

type hongguoProvider struct {
	host   core.Host
	client *hongguoAppClient
}

func (p *hongguoProvider) text(ctx context.Context, address, referer string) (string, error) {
	response, err := p.host.FetchText(ctx, core.TextRequest{URL: address, Referer: referer})
	return response.Body, err
}

var hongguoDescriptor = core.SourceDescriptor{
	ID:             sourceHongguo,
	Name:           "红果",
	Description:    "短剧 · 漫剧 · AI 剧",
	Aliases:        []string{"hongguoduanju.com", "www.hongguoduanju.com"},
	BaseURL:        hongguoBaseURL,
	ContinuousFeed: true,
	ValidCategory:  validHongguoCategory,
	Rankings: []core.RankingBoard{
		{ID: "hongguo-hot", Source: sourceHongguo, Name: "总热播榜", Description: "红果观看、互动等综合热度；每日更新。", Path: "hot-drama", UpstreamKey: "hongguo"},
		{ID: "hongguo-real", Source: sourceHongguo, Name: "真人剧榜", Description: "红果真人剧热播榜；每日更新。", Path: "hot-real-drama", UpstreamKey: "real"},
		{ID: "hongguo-comic", Source: sourceHongguo, Name: "漫剧榜", Description: "红果漫剧热播榜；每日更新。", Path: "hot-comic-drama", UpstreamKey: "comic"},
		{ID: "hongguo-ai", Source: sourceHongguo, Name: "AI剧榜", Description: "红果 AI 剧热播榜；每日更新。", Path: "hot-ai-drama", UpstreamKey: "ai"},
	},
}

func init() {
	core.Register(hongguoDescriptor, func(host core.Host) core.Provider { return &hongguoProvider{host: host, client: newHongguoAppClient()} })
}

func validHongguoCategory(category string) bool {
	for _, genre := range hongguoAppGenres {
		if category == genre.key {
			return true
		}
	}
	return hongguoTagPattern.MatchString(category)
}

func hongguoGenreCategories() []core.Category {
	genres := make([]core.Category, 0, len(hongguoAppGenres))
	for _, genre := range hongguoAppGenres {
		genres = append(genres, core.Category{ID: genre.key, Name: genre.name})
	}
	return genres
}

func (p *hongguoProvider) Descriptor() core.SourceDescriptor { return hongguoDescriptor }

func (p *hongguoProvider) BaseURL() string { return hongguoBaseURL }

func (p *hongguoProvider) Categories(ctx context.Context, force bool) ([]core.Category, error) {
	genres := hongguoGenreCategories()
	tags, err := p.fetchHongguoTagCategories(ctx)
	if err != nil {
		return genres, core.ErrCategoriesIncomplete
	}
	return append(genres, tags...), nil
}

func (p *hongguoProvider) CatalogPage(ctx context.Context, page int, category string) ([]core.Drama, bool, error) {
	if strings.HasPrefix(category, hongguoTagPrefix) {
		return p.fetchHongguoTagPage(ctx, category, page)
	}
	items, err := p.fetchHongguoAppCatalogCategory(ctx, category)
	state := p.hongguoCatalogSnapshot()
	hasMore := hongguoCatalogHasMore(state)
	if category != "" && state != nil {
		hasMore = !state.Feeds["category:"+category].Exhausted
	}
	if len(items) == 0 && err != nil && ctx.Err() == nil && (category == "" || category == "short_play") {
		var totalPages int
		items, totalPages, err = p.fetchHongguoCategoryPage(ctx, "real-drama?page="+strconv.Itoa(page), "真人剧")
		hasMore = page < totalPages
	}
	return items, hasMore, err
}

func (p *hongguoProvider) SearchWithWarning(ctx context.Context, page int, query string) ([]core.Drama, bool, string, error) {
	var entry hongguoSearchEntry
	var err error
	if page == 1 {
		entry, err = p.searchHongguoDramas(ctx, query)
	} else if keyword, keywordErr := hongguoSearchKeyword(query); keywordErr != nil {
		err = keywordErr
	} else {
		entry, err = p.fetchHongguoSearchPage(ctx, keyword, page)
	}
	if err != nil {
		return nil, false, "", err
	}
	return entry.Dramas, entry.HasMore, entry.Warning, nil
}

func (p *hongguoProvider) Detail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	title, chapters, err := p.fetchHongguoChapters(ctx, sourceID)
	if err != nil {
		return core.Drama{}, nil, err
	}
	raw := p.hongguoCachedDrama(core.Drama{ID: core.ProviderDramaID(sourceHongguo, sourceID), Title: title, Source: sourceHongguo})
	return raw, chapters, nil
}

func (p *hongguoProvider) ResolveMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	chapter := task.Chapter
	if strings.HasPrefix(chapter.VideoURL, "hongguo-cenc://") {
		return p.resolveHongguoMedia(ctx, task)
	}
	media := core.ProviderMedia{URL: chapter.VideoURL, Referer: core.FirstNonEmpty(chapter.Referer, hongguoBaseURL+"/")}
	return p.host.FinishMedia(ctx, media, sourceHongguo)
}

func (p *hongguoProvider) RankingPage(ctx context.Context, board core.RankingBoard, page int) (core.RankingPage, error) {
	return p.fetchHongguoRankingPage(ctx, board, page)
}

func (p *hongguoProvider) CoverAddress(ctx context.Context, drama core.LibraryDrama, id string) (string, error) {
	if !hongguoNumericID.MatchString(id) {
		return "", errors.New("无效的红果剧集 ID")
	}
	result, err := p.hongguoAppRequest(ctx, http.MethodPost, "/novel/player/video_detail/v1/", nil, map[string]any{"series_id": id})
	if err != nil {
		return "", err
	}
	row := nestedMap(result, "data", "video_data")
	if core.MapString(row, "series_id_str", "series_id") != id {
		return "", errors.New("红果详情与请求剧集不符")
	}
	return hongguoCoverAddress(core.MapString(row, "series_cover", "cover")), nil
}

func (p *hongguoProvider) NeedsExtraMetadata(drama core.LibraryDrama) bool {
	return drama.OnlineDate == "" || drama.Score == ""
}

func (p *hongguoProvider) ExtraMetadata(ctx context.Context, drama core.LibraryDrama, id string) (core.LibraryDrama, error) {
	if !hongguoNumericID.MatchString(id) {
		return drama, errors.New("红果剧集 ID 无效")
	}
	if drama.OnlineDate == "" {
		body, err := p.text(ctx, hongguoBaseURL+"/detail?series_id="+url.QueryEscape(id), hongguoBaseURL+"/")
		if err != nil {
			return drama, err
		}
		raw, err := parseHongguoSortDetail(body, id)
		if err != nil {
			return drama, err
		}
		if raw.ID == drama.ID {
			drama = core.MergeNativeDrama(drama, core.NativeNormalize(raw))
		}
	}
	if drama.Score == "" {
		drama.Score = core.NativeDramaScore(p.hongguoSearchScore(ctx, drama.ID, drama.Title))
	}
	return drama, ctx.Err()
}

func (p *hongguoProvider) CatalogState() json.RawMessage {
	body, err := json.Marshal(p.hongguoCatalogSnapshot())
	if err != nil {
		return nil
	}
	return body
}

func (p *hongguoProvider) RestoreCatalogState(raw json.RawMessage) {
	var state hongguoCatalogState
	if len(raw) == 0 || json.Unmarshal(raw, &state) != nil {
		return
	}
	p.restoreHongguoCatalog(&state)
}

func hongguoCoverAddress(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 8192 {
			continue
		}
		if strings.HasPrefix(value, "//") {
			value = "https:" + value
		}
		parsed, err := url.Parse(value)
		if err == nil && core.ValidNativeCoverURL(parsed) {
			return value
		}
	}
	return ""
}

func (p *hongguoProvider) Suggestions(ctx context.Context, query string) ([]string, error) {
	items, err := p.hongguoSearchSuggestions(ctx, query)
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names, err
}

func (p *hongguoProvider) DanmakuIDs(task core.Task) (string, string, bool) {
	return hongguoPlaybackIDs(task)
}

func (p *hongguoProvider) Danmaku(ctx context.Context, series, video string, startMS, durationMS int64) (core.DanmakuPage, error) {
	return p.hongguoDanmaku(ctx, series, video, startMS, durationMS)
}

func (p *hongguoProvider) Devices(ctx context.Context, register bool) (bool, error) {
	client := p.client
	if register {
		client.deviceMu.Lock()
		client.registerPausedUntil = time.Time{}
		client.deviceMu.Unlock()
		for _, helper := range []bool{false, true} {
			if err := p.ensureHongguoDevice(ctx, helper, ""); err != nil {
				return false, err
			}
		}
	}
	_, _, primary := client.device(false)
	_, _, helper := client.device(true)
	return primary && helper, nil
}

func hongguoRecommendationCursor(genre string, cursor json.RawMessage, seen []string) (hongguoRecommendationQuery, error) {
	query := hongguoRecommendationQuery{Genre: genre}
	if len(cursor) > 0 && (json.Unmarshal(cursor, &query) != nil || query.Genre != genre) {
		return hongguoRecommendationQuery{}, errors.New("推荐分类或分页参数无效，请重新获取")
	}
	if seen != nil {
		query.Seen = seen
	}
	return query, query.validate()
}

func (p *hongguoProvider) ValidRecommendation(genre string, cursor json.RawMessage) error {
	_, err := hongguoRecommendationCursor(genre, cursor, nil)
	return err
}

func (p *hongguoProvider) Recommendations(ctx context.Context, genre string, cursor json.RawMessage, seen []string) (core.RecommendationPage, error) {
	query, err := hongguoRecommendationCursor(genre, cursor, seen)
	if err != nil {
		return core.RecommendationPage{}, err
	}
	page, err := p.fetchHongguoRecommendations(ctx, query)
	if err != nil {
		return core.RecommendationPage{}, err
	}
	session := page.SessionID
	if session == "" {
		session = query.SessionID
	}
	next, err := json.Marshal(hongguoRecommendationQuery{Genre: genre, Offset: page.NextOffset, SessionID: session, CellID: page.CellID, ShowType: page.ShowType})
	return core.RecommendationPage{Dramas: page.Dramas, Cursor: next, HasMore: page.HasMore}, err
}

const sourceHongguo = "hongguo"
