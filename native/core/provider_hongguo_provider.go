package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type hongguoProvider struct{ d *Downloader }

var hongguoDescriptor = SourceDescriptor{
	ID:             sourceHongguo,
	Name:           "红果",
	Description:    "短剧 · 漫剧 · AI 剧",
	Aliases:        []string{"hongguoduanju.com", "www.hongguoduanju.com"},
	BaseURL:        hongguoBaseURL,
	ContinuousFeed: true,
	ValidCategory:  validHongguoCategory,
	Rankings: []RankingBoard{
		{ID: "hongguo-hot", Source: sourceHongguo, Name: "总热播榜", Description: "红果观看、互动等综合热度；每日更新。", path: "hot-drama", upstreamKey: "hongguo"},
		{ID: "hongguo-real", Source: sourceHongguo, Name: "真人剧榜", Description: "红果真人剧热播榜；每日更新。", path: "hot-real-drama", upstreamKey: "real"},
		{ID: "hongguo-comic", Source: sourceHongguo, Name: "漫剧榜", Description: "红果漫剧热播榜；每日更新。", path: "hot-comic-drama", upstreamKey: "comic"},
		{ID: "hongguo-ai", Source: sourceHongguo, Name: "AI剧榜", Description: "红果 AI 剧热播榜；每日更新。", path: "hot-ai-drama", upstreamKey: "ai"},
	},
}

func init() {
	Register(hongguoDescriptor, func(host Host) Provider { return &hongguoProvider{d: host.(*Downloader)} })
}

func validHongguoCategory(category string) bool {
	for _, genre := range hongguoAppGenres {
		if category == genre.key {
			return true
		}
	}
	return hongguoTagPattern.MatchString(category)
}

func hongguoGenreCategories() []Category {
	genres := make([]Category, 0, len(hongguoAppGenres))
	for _, genre := range hongguoAppGenres {
		genres = append(genres, Category{ID: genre.key, Name: genre.name})
	}
	return genres
}

func (p *hongguoProvider) Descriptor() SourceDescriptor { return hongguoDescriptor }

func (p *hongguoProvider) BaseURL() string { return firstNonEmpty(p.d.cfg.HongguoURL, hongguoBaseURL) }

func (p *hongguoProvider) Categories(ctx context.Context, force bool) ([]Category, error) {
	genres := hongguoGenreCategories()
	tags, err := p.d.fetchHongguoTagCategories(ctx)
	if err != nil {
		return genres, errCategoriesIncomplete
	}
	return append(genres, tags...), nil
}

func (p *hongguoProvider) CatalogPage(ctx context.Context, page int, category string) ([]Drama, bool, error) {
	if strings.HasPrefix(category, hongguoTagPrefix) {
		return p.d.fetchHongguoTagPage(ctx, category, page)
	}
	items, err := p.d.fetchHongguoAppCatalogCategory(ctx, category)
	state := p.d.hongguoCatalogSnapshot()
	hasMore := hongguoCatalogHasMore(state)
	if category != "" && state != nil {
		hasMore = !state.Feeds["category:"+category].Exhausted
	}
	if len(items) == 0 && err != nil && ctx.Err() == nil && (category == "" || category == "short_play") {
		var totalPages int
		items, totalPages, err = p.d.fetchHongguoCategoryPage(ctx, "real-drama?page="+strconv.Itoa(page), "真人剧")
		hasMore = page < totalPages
	}
	return items, hasMore, err
}

func (p *hongguoProvider) SearchWithWarning(ctx context.Context, page int, query string) ([]Drama, bool, string, error) {
	var entry hongguoSearchEntry
	var err error
	if page == 1 {
		entry, err = p.d.searchHongguoDramas(ctx, query)
	} else if keyword, keywordErr := hongguoSearchKeyword(query); keywordErr != nil {
		err = keywordErr
	} else {
		entry, err = p.d.fetchHongguoSearchPage(ctx, keyword, page)
	}
	if err != nil {
		return nil, false, "", err
	}
	return entry.Dramas, entry.HasMore, entry.Warning, nil
}

func (p *hongguoProvider) Detail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	title, chapters, err := p.d.fetchHongguoChapters(ctx, sourceID)
	if err != nil {
		return Drama{}, nil, err
	}
	raw := p.d.hongguoCachedDrama(Drama{ID: providerDramaID(sourceHongguo, sourceID), Title: title, Source: sourceHongguo})
	return raw, chapters, nil
}

func (p *hongguoProvider) ResolveMedia(ctx context.Context, task Task) (ProviderMedia, error) {
	chapter := task.Chapter
	if strings.HasPrefix(chapter.VideoURL, "hongguo-cenc://") {
		return p.d.resolveHongguoMedia(ctx, task)
	}
	media := ProviderMedia{URL: chapter.VideoURL, Referer: firstNonEmpty(chapter.Referer, p.d.providerBaseURL(sourceHongguo)+"/")}
	return p.d.finishProviderMedia(ctx, media, sourceHongguo)
}

func (p *hongguoProvider) RankingPage(ctx context.Context, board RankingBoard, page int) (RankingPage, error) {
	return p.d.fetchHongguoRankingPage(ctx, board, page)
}

func (p *hongguoProvider) CoverAddress(ctx context.Context, drama LibraryDrama, id string) (string, error) {
	if !hongguoNumericID.MatchString(id) {
		return "", errors.New("无效的红果剧集 ID")
	}
	result, err := p.d.hongguoAppRequest(ctx, http.MethodPost, "/novel/player/video_detail/v1/", nil, map[string]any{"series_id": id})
	if err != nil {
		return "", err
	}
	row := nestedMap(result, "data", "video_data")
	if mapString(row, "series_id_str", "series_id") != id {
		return "", errors.New("红果详情与请求剧集不符")
	}
	return hongguoCoverAddress(mapString(row, "series_cover", "cover")), nil
}

func (p *hongguoProvider) NeedsExtraMetadata(drama LibraryDrama) bool {
	return drama.OnlineDate == "" || drama.Score == ""
}

func (p *hongguoProvider) ExtraMetadata(ctx context.Context, drama LibraryDrama, id string) (LibraryDrama, error) {
	if !hongguoNumericID.MatchString(id) {
		return drama, errors.New("红果剧集 ID 无效")
	}
	if drama.OnlineDate == "" {
		body, err := p.d.fetchProviderText(ctx, hongguoBaseURL+"/detail?series_id="+url.QueryEscape(id), hongguoBaseURL+"/")
		if err != nil {
			return drama, err
		}
		raw, err := parseHongguoSortDetail(body, id)
		if err != nil {
			return drama, err
		}
		if raw.ID == drama.ID {
			drama = mergeNativeDrama(drama, nativeNormalize(raw))
		}
	}
	if drama.Score == "" {
		drama.Score = nativeDramaScore(p.d.hongguoSearchScore(ctx, drama.ID, drama.Title))
	}
	return drama, ctx.Err()
}

func (p *hongguoProvider) CatalogState() json.RawMessage {
	body, err := json.Marshal(p.d.hongguoCatalogSnapshot())
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
	p.d.restoreHongguoCatalog(&state)
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
		if err == nil && validNativeCoverURL(parsed) {
			return value
		}
	}
	return ""
}

func (p *hongguoProvider) Suggestions(ctx context.Context, query string) ([]string, error) {
	items, err := p.d.hongguoSearchSuggestions(ctx, query)
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names, err
}

func (p *hongguoProvider) DanmakuIDs(task Task) (string, string, bool) {
	return hongguoPlaybackIDs(task)
}

func (p *hongguoProvider) Danmaku(ctx context.Context, series, video string, startMS, durationMS int64) (DanmakuPage, error) {
	return p.d.hongguoDanmaku(ctx, series, video, startMS, durationMS)
}

func (p *hongguoProvider) Devices(ctx context.Context, register bool) (bool, error) {
	client := p.d.hongguoClient()
	if register {
		client.deviceMu.Lock()
		client.registerPausedUntil = time.Time{}
		client.deviceMu.Unlock()
		for _, helper := range []bool{false, true} {
			if err := p.d.ensureHongguoDevice(ctx, helper, ""); err != nil {
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

func (p *hongguoProvider) Recommendations(ctx context.Context, genre string, cursor json.RawMessage, seen []string) (RecommendationPage, error) {
	query, err := hongguoRecommendationCursor(genre, cursor, seen)
	if err != nil {
		return RecommendationPage{}, err
	}
	page, err := p.d.fetchHongguoRecommendations(ctx, query)
	if err != nil {
		return RecommendationPage{}, err
	}
	session := page.SessionID
	if session == "" {
		session = query.SessionID
	}
	next, err := json.Marshal(hongguoRecommendationQuery{Genre: genre, Offset: page.NextOffset, SessionID: session, CellID: page.CellID, ShowType: page.ShowType})
	return RecommendationPage{Dramas: page.Dramas, Cursor: next, HasMore: page.HasMore}, err
}

const sourceHongguo = "hongguo"
