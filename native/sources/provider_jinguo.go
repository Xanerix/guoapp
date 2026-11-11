package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"duanjuapp/native/core"
)

const (
	jinguoChooseCategory = "choose"
	jinguoNewCategory    = "new"
	jinguoTabPrefix      = "tab:"
)

var jinguoID = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

type jinguoRow struct {
	core.Drama
}

func (row *jinguoRow) UnmarshalJSON(body []byte) error {
	var detail detailResponse
	var names struct {
		TagNames []any `json:"tagNames"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		return err
	}
	if err := json.Unmarshal(body, &names); err != nil {
		return err
	}
	row.Drama = detail.Drama
	row.Drama.Tags = core.MapStringSlice(map[string]any{"tagNames": names.TagNames}, "tagNames")
	return nil
}

type jinguoChapter struct {
	core.Chapter
	CanPlay *bool `json:"canPlay"`
}

func validJinguoCategory(category string) bool {
	switch category {
	case "", jinguoChooseCategory, jinguoNewCategory:
		return true
	}
	id, ok := strings.CutPrefix(category, jinguoTabPrefix)
	return ok && jinguoID.MatchString(id)
}

func (p *jinguoProvider) fetchJinguoTabs(ctx context.Context) ([]Tab, error) {
	var tabs legacyTabList
	if err := p.client.get(ctx, "/api/app/playlet-tab/all", nil, &tabs); err != nil {
		return nil, err
	}
	var valid []Tab
	for _, tab := range tabs {
		tab.Name = strings.TrimSpace(tab.Name)
		if jinguoID.MatchString(tab.ID) && tab.Name != "" {
			valid = append(valid, tab)
		}
	}
	if len(valid) == 0 {
		return nil, errors.New("禁果未返回可用分类")
	}
	return valid, nil
}

func (p *jinguoProvider) fetchJinguoCategories(ctx context.Context) ([]core.Category, error) {
	tabs, err := p.fetchJinguoTabs(ctx)
	if err != nil {
		return nil, err
	}
	categories := []core.Category{{ID: jinguoChooseCategory, Name: "精选"}, {ID: jinguoNewCategory, Name: "最新"}}
	for _, tab := range tabs {
		categories = append(categories, core.Category{ID: jinguoTabPrefix + tab.ID, Name: tab.Name})
	}
	return categories, nil
}

func (p *jinguoProvider) fetchJinguoCatalogPage(ctx context.Context, page int, category, query string) ([]core.Drama, bool, error) {
	query = strings.TrimSpace(query)
	if !validJinguoCategory(category) || len(query) > 256 {
		return nil, false, errors.New("禁果目录查询参数无效")
	}
	params := map[string]string{"pageNumber": strconv.Itoa(max(1, page)), "pageSize": strconv.Itoa(max(1, jinguoPageSize))}
	switch {
	case query != "":
		params["keyword"] = query
		return p.fetchJinguoList(ctx, "/api/app/playlet/keyword/search", params, "")
	case category == jinguoChooseCategory:
		return p.fetchJinguoList(ctx, "/api/app/playlet-recommend/home/choose", params, "")
	case category == jinguoNewCategory:
		return p.fetchJinguoList(ctx, "/api/app/playlet-recommend/home/new", params, "")
	}
	tabs, err := p.fetchJinguoTabs(ctx)
	if err != nil {
		return nil, false, err
	}
	params["tabSortType"] = "1"
	selected := strings.TrimPrefix(category, jinguoTabPrefix)
	seen := map[string]bool{}
	var items []core.Drama
	var failures []error
	found, hasMore := false, false
	for _, tab := range tabs {
		if category != "" && tab.ID != selected {
			continue
		}
		found = true
		list, more, err := p.fetchJinguoList(ctx, "/api/app/playlet/home/tab/"+url.PathEscape(tab.ID), params, tab.Name)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", tab.Name, err))
			hasMore = true
			if ctx.Err() != nil {
				break
			}
			continue
		}
		hasMore = hasMore || more
		for _, drama := range list {
			if !seen[drama.ID] {
				seen[drama.ID] = true
				items = append(items, drama)
			}
		}
	}
	if !found {
		return nil, false, errors.New("此分类已调整，请刷新分类列表")
	}
	return items, hasMore, errors.Join(failures...)
}

func (p *jinguoProvider) fetchJinguoList(ctx context.Context, route string, params map[string]string, tabName string) ([]core.Drama, bool, error) {
	var result struct {
		List []jinguoRow `json:"list"`
	}
	if err := p.client.get(ctx, route, params, &result); err != nil {
		return nil, false, err
	}
	var items []core.Drama
	for _, row := range result.List {
		if drama, ok := jinguoDrama(row.Drama, tabName); ok {
			items = append(items, drama)
		}
	}
	return items, len(result.List) >= max(1, jinguoPageSize), nil
}

func jinguoDrama(drama core.Drama, tabName string) (core.Drama, bool) {
	if !jinguoID.MatchString(drama.ID) {
		return core.Drama{}, false
	}
	drama.SourceID, drama.Source = drama.ID, sourceJinguo
	drama.ID = core.ProviderDramaID(sourceJinguo, drama.SourceID)
	drama.ChannelName = "禁果"
	drama.CategoryName = core.FirstNonEmpty(drama.CategoryName, tabName)
	covers := []any{drama.Cover, drama.CoverURL, drama.CoverURLSnake, drama.Image, drama.ImageURL, drama.ImageURLSnake, drama.Img, drama.Pic, drama.Picture, drama.Poster, drama.Thumb, drama.Thumbnail}
	drama.Cover, drama.CoverURL = nil, nil
	for _, cover := range covers {
		if address := legacyCoverURL(cover); address != "" {
			drama.Cover, drama.CoverURL = address, address
			break
		}
	}
	return drama, true
}

func (p *jinguoProvider) fetchJinguoDetail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	if !jinguoID.MatchString(sourceID) {
		return core.Drama{}, nil, errors.New("无效的禁果剧集 ID")
	}
	client := p.client
	var raw json.RawMessage
	if err := client.get(ctx, "/api/app/playlet/detail/"+url.PathEscape(sourceID), nil, &raw); err != nil {
		return core.Drama{}, nil, err
	}
	var detail jinguoRow
	var playable struct {
		Chapters []jinguoChapter `json:"chapters"`
	}
	if json.Unmarshal(raw, &detail) != nil || json.Unmarshal(raw, &playable) != nil {
		return core.Drama{}, nil, errors.New("禁果详情数据格式无效")
	}
	drama, ok := jinguoDrama(detail.Drama, "")
	if !ok || drama.SourceID != sourceID {
		return core.Drama{}, nil, errors.New("禁果详情与请求剧集不符")
	}
	rows := playable.Chapters
	if len(rows) == 0 {
		if err := client.get(ctx, "/api/app/playlet-chapter/list/"+url.PathEscape(sourceID), nil, &rows); err != nil {
			return core.Drama{}, nil, err
		}
	}
	chapters := make([]core.Chapter, 0, len(rows))
	for index, row := range rows {
		chapter := row.Chapter
		chapter.VIP = chapter.VideoURL == "" || row.CanPlay != nil && !*row.CanPlay
		chapter.Source = sourceJinguo
		chapter.ID = core.ProviderChapterID(sourceJinguo, sourceID, core.FirstNonEmpty(chapter.ID, strconv.Itoa(index+1)))
		if core.ChapterEpisodeNumber(chapter, 0) <= 0 {
			chapter.CurrentEpisode = core.RawEpisode(index + 1)
		}
		chapter.Title = core.FirstNonEmpty(chapter.Title, "第"+chapter.EpisodeString(index+1)+"集")
		chapters = append(chapters, chapter)
	}
	chapters = core.UniqueChapters(chapters)
	core.SortProviderChapters(chapters)
	return drama, chapters, nil
}

func jinguoPlaylistPath(videoURL string) (string, bool) {
	videoURL = strings.Trim(strings.TrimSpace(videoURL), "/")
	if videoURL == "" || strings.ContainsAny(videoURL, "?#\\") || strings.Contains(videoURL, "://") {
		return "", false
	}
	segments := strings.Split(videoURL, "/")
	for index, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", false
		}
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/"), true
}

func (p *jinguoProvider) resolveJinguoMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	if strings.TrimSpace(task.Chapter.VideoURL) == "" {
		return core.ProviderMedia{}, errors.New("禁果该集需要金币或会员解锁后才能播放")
	}
	path, ok := jinguoPlaylistPath(task.Chapter.VideoURL)
	if !ok {
		return core.ProviderMedia{}, errors.New("禁果分集播放地址无效")
	}
	client := p.client
	for attempt := 0; attempt < 2; attempt++ {
		token, _, err := client.credentials(ctx)
		if err != nil {
			return core.ProviderMedia{}, err
		}
		var lastErr error
		expired := false
		for _, base := range client.bases() {
			referer := base + "/"
			address := base + "/api/app/vid/h5/m3u8/" + path + "?" + url.Values{"token": {token}}.Encode()
			playlist, finalURL, err := p.host.FetchPlaylist(ctx, address, referer, nil)
			err = legacyPlaylistError(err)
			if err == nil {
				client.rememberBase(base)
				return core.ProviderMedia{URL: finalURL, Playlist: playlist, Referer: referer, Duration: core.M3u8Duration(playlist)}, nil
			}
			lastErr = err
			if expired = jinguoSessionExpired(err); expired || ctx.Err() != nil {
				break
			}
		}
		if attempt == 0 && expired {
			client.invalidate(token)
			continue
		}
		return core.ProviderMedia{}, lastErr
	}
	return core.ProviderMedia{}, errors.New("禁果播放会话已失效，请稍后重试")
}

const jinguoPageSize = 30

type jinguoProvider struct {
	host   core.Host
	client *jinguoAPIClient
}

var jinguoDescriptor = core.SourceDescriptor{
	ID:                 sourceJinguo,
	Name:               "禁果",
	Description:        "精选 · 最新 · 分类短剧 · 在线搜索",
	DefaultOff:         true,
	AllowEmptyCatalog:  true,
	KeepDetailEpisodes: true,
	ValidCategory:      validJinguoCategory,
}

func init() {
	core.Register(jinguoDescriptor, func(host core.Host) core.Provider {
		return &jinguoProvider{host: host, client: &jinguoAPIClient{host: host}}
	})
}

func (p *jinguoProvider) Descriptor() core.SourceDescriptor { return jinguoDescriptor }

func (p *jinguoProvider) Categories(ctx context.Context, force bool) ([]core.Category, error) {
	return p.fetchJinguoCategories(ctx)
}

func (p *jinguoProvider) CatalogPage(ctx context.Context, page int, category string) ([]core.Drama, bool, error) {
	return p.fetchJinguoCatalogPage(ctx, page, category, "")
}

func (p *jinguoProvider) Search(ctx context.Context, page int, query string) ([]core.Drama, bool, error) {
	return p.fetchJinguoCatalogPage(ctx, page, "", query)
}

func (p *jinguoProvider) Detail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	return p.fetchJinguoDetail(ctx, sourceID)
}

func (p *jinguoProvider) ResolveMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	return p.resolveJinguoMedia(ctx, task)
}

const sourceJinguo = "jinguo"
