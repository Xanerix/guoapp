package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	jinguoChooseCategory = "choose"
	jinguoNewCategory    = "new"
	jinguoTabPrefix      = "tab:"
)

var jinguoID = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)

type jinguoRow struct {
	Drama
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
	row.Drama.Tags = mapStringSlice(map[string]any{"tagNames": names.TagNames}, "tagNames")
	return nil
}

type jinguoChapter struct {
	Chapter
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

func (d *Downloader) fetchJinguoTabs(ctx context.Context) ([]Tab, error) {
	var tabs legacyTabList
	if err := d.jinguoClient().get(ctx, "/api/app/playlet-tab/all", nil, &tabs); err != nil {
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

func (d *Downloader) fetchJinguoCategories(ctx context.Context) ([]nativeCategory, error) {
	tabs, err := d.fetchJinguoTabs(ctx)
	if err != nil {
		return nil, err
	}
	categories := []nativeCategory{{ID: jinguoChooseCategory, Name: "精选"}, {ID: jinguoNewCategory, Name: "最新"}}
	for _, tab := range tabs {
		categories = append(categories, nativeCategory{ID: jinguoTabPrefix + tab.ID, Name: tab.Name})
	}
	return categories, nil
}

func (d *Downloader) fetchJinguoCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	query = strings.TrimSpace(query)
	if !validJinguoCategory(category) || len(query) > 256 {
		return nil, false, errors.New("禁果目录查询参数无效")
	}
	params := map[string]string{"pageNumber": strconv.Itoa(max(1, page)), "pageSize": strconv.Itoa(max(1, d.cfg.PageSize))}
	switch {
	case query != "":
		params["keyword"] = query
		return d.fetchJinguoList(ctx, "/api/app/playlet/keyword/search", params, "")
	case category == jinguoChooseCategory:
		return d.fetchJinguoList(ctx, "/api/app/playlet-recommend/home/choose", params, "")
	case category == jinguoNewCategory:
		return d.fetchJinguoList(ctx, "/api/app/playlet-recommend/home/new", params, "")
	}
	tabs, err := d.fetchJinguoTabs(ctx)
	if err != nil {
		return nil, false, err
	}
	params["tabSortType"] = "1"
	selected := strings.TrimPrefix(category, jinguoTabPrefix)
	seen := map[string]bool{}
	var items []Drama
	var failures []error
	found, hasMore := false, false
	for _, tab := range tabs {
		if category != "" && tab.ID != selected {
			continue
		}
		found = true
		list, more, err := d.fetchJinguoList(ctx, "/api/app/playlet/home/tab/"+url.PathEscape(tab.ID), params, tab.Name)
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

func (d *Downloader) fetchJinguoList(ctx context.Context, route string, params map[string]string, tabName string) ([]Drama, bool, error) {
	var result struct {
		List []jinguoRow `json:"list"`
	}
	if err := d.jinguoClient().get(ctx, route, params, &result); err != nil {
		return nil, false, err
	}
	var items []Drama
	for _, row := range result.List {
		if drama, ok := jinguoDrama(row.Drama, tabName); ok {
			items = append(items, drama)
		}
	}
	return items, len(result.List) >= max(1, d.cfg.PageSize), nil
}

func jinguoDrama(drama Drama, tabName string) (Drama, bool) {
	if !jinguoID.MatchString(drama.ID) {
		return Drama{}, false
	}
	drama.SourceID, drama.Source = drama.ID, sourceJinguo
	drama.ID = providerDramaID(sourceJinguo, drama.SourceID)
	drama.ChannelName = "禁果"
	drama.CategoryName = firstNonEmpty(drama.CategoryName, tabName)
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

func (d *Downloader) fetchJinguoDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	if !jinguoID.MatchString(sourceID) {
		return Drama{}, nil, errors.New("无效的禁果剧集 ID")
	}
	client := d.jinguoClient()
	var raw json.RawMessage
	if err := client.get(ctx, "/api/app/playlet/detail/"+url.PathEscape(sourceID), nil, &raw); err != nil {
		return Drama{}, nil, err
	}
	var detail jinguoRow
	var playable struct {
		Chapters []jinguoChapter `json:"chapters"`
	}
	if json.Unmarshal(raw, &detail) != nil || json.Unmarshal(raw, &playable) != nil {
		return Drama{}, nil, errors.New("禁果详情数据格式无效")
	}
	drama, ok := jinguoDrama(detail.Drama, "")
	if !ok || drama.SourceID != sourceID {
		return Drama{}, nil, errors.New("禁果详情与请求剧集不符")
	}
	rows := playable.Chapters
	if len(rows) == 0 {
		if err := client.get(ctx, "/api/app/playlet-chapter/list/"+url.PathEscape(sourceID), nil, &rows); err != nil {
			return Drama{}, nil, err
		}
	}
	chapters := make([]Chapter, 0, len(rows))
	for index, row := range rows {
		chapter := row.Chapter
		chapter.VIP = chapter.VideoURL == "" || row.CanPlay != nil && !*row.CanPlay
		chapter.Source = sourceJinguo
		chapter.ID = providerChapterID(sourceJinguo, sourceID, firstNonEmpty(chapter.ID, strconv.Itoa(index+1)))
		if chapterEpisodeNumber(chapter, 0) <= 0 {
			chapter.CurrentEpisode = rawEpisode(index + 1)
		}
		chapter.Title = firstNonEmpty(chapter.Title, "第"+chapter.EpisodeString(index+1)+"集")
		chapters = append(chapters, chapter)
	}
	chapters = uniqueChapters(chapters)
	sortProviderChapters(chapters)
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

func (d *Downloader) resolveJinguoMedia(ctx context.Context, task Task) (providerMedia, error) {
	if strings.TrimSpace(task.Chapter.VideoURL) == "" {
		return providerMedia{}, errors.New("禁果该集需要金币或会员解锁后才能播放")
	}
	path, ok := jinguoPlaylistPath(task.Chapter.VideoURL)
	if !ok {
		return providerMedia{}, errors.New("禁果分集播放地址无效")
	}
	client := d.jinguoClient()
	for attempt := 0; attempt < 2; attempt++ {
		token, _, err := client.credentials(ctx)
		if err != nil {
			return providerMedia{}, err
		}
		var lastErr error
		expired := false
		for _, base := range client.bases() {
			referer := base + "/"
			address := base + "/api/app/vid/h5/m3u8/" + path + "?" + url.Values{"token": {token}}.Encode()
			playlist, finalURL, err := d.fetchMediaPlaylist(ctx, address, referer)
			if err == nil {
				client.rememberBase(base)
				return providerMedia{URL: finalURL, Playlist: playlist, Referer: referer, Duration: m3u8Duration(playlist)}, nil
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
		return providerMedia{}, lastErr
	}
	return providerMedia{}, errors.New("禁果播放会话已失效，请稍后重试")
}
