package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"duanjuapp/native/core"
)

const hongguoBaseURL = "https://hongguoduanju.com"

func (p *hongguoProvider) fetchHongguoCategoryPage(ctx context.Context, route, category string) ([]core.Drama, int, error) {
	body, err := p.text(ctx, hongguoBaseURL+"/category/"+route, hongguoBaseURL+"/")
	if err != nil {
		return nil, 0, err
	}
	data := parseRouterData(body)
	page := routerLoaderMap(data, "category_page", "category_$")
	if len(page) == 0 || page["isSuccess"] == false {
		return nil, 0, errors.New("红果分类数据不可用，可能是页面结构或访问权限变化")
	}
	items := anyList(page["recommendList"])
	out := make([]core.Drama, 0, len(items))
	for _, item := range items {
		if dr := hongguoDramaFromAny(item, category); dr.ID != "" {
			out = append(out, dr)
		}
	}
	pages, _ := strconv.Atoi(core.MapString(nestedMap(page, "pagination"), "totalPages"))
	return out, pages, nil
}

func (p *hongguoProvider) fetchHongguoChapters(ctx context.Context, sourceID string) (string, []core.Chapter, error) {
	sourceID = strings.TrimPrefix(strings.TrimSpace(sourceID), "hg-series-v1:")
	if sourceID == "" {
		return "", nil, fmt.Errorf("empty hongguo sourceID")
	}
	entry, appErr := p.hongguoAppDetail(ctx, sourceID)
	if appErr == nil {
		return entry.Drama.DisplayTitle(), append([]core.Chapter(nil), entry.Chapters...), nil
	}
	if ctx.Err() != nil {
		return "", nil, ctx.Err()
	}
	title, chapters, webErr := p.fetchHongguoWebChapters(ctx, sourceID)
	if webErr == nil {
		return title, chapters, nil
	}
	return "", nil, fmt.Errorf("App 详情失败: %v；网页详情失败: %w", core.PublicError(appErr), webErr)
}

func (p *hongguoProvider) fetchHongguoWebChapters(ctx context.Context, sourceID string) (string, []core.Chapter, error) {
	body, err := p.text(ctx, hongguoBaseURL+"/detail?series_id="+url.QueryEscape(sourceID), hongguoBaseURL+"/")
	if err != nil {
		return "", nil, err
	}
	data := parseRouterData(body)
	page := routerLoaderMap(data, "detail_page", "detail_")
	detail, _ := page["seriesDetail"].(map[string]any)
	if len(detail) == 0 {
		return "", nil, fmt.Errorf("hongguo detail is empty")
	}
	title := core.FirstNonEmpty(core.MapString(detail, "series_name", "series_title", "name"), sourceID)
	vids := anyList(detail["vid_list"])
	chapters := make([]core.Chapter, 0, len(vids))
	for i, v := range vids {
		vid := strings.TrimSpace(fmt.Sprint(v))
		if vid == "" || vid == "<nil>" {
			continue
		}
		idx := i + 1
		chapters = append(chapters, core.Chapter{ID: core.ProviderChapterID(sourceHongguo, sourceID, vid), Source: sourceHongguo, Title: fmt.Sprintf("第%d集", idx), VideoURL: "hongguo-cenc://" + vid, CurrentEpisode: core.RawEpisode(idx)})
	}
	if len(chapters) == 0 {
		return title, nil, errors.New("红果详情没有返回剧集 ID")
	}
	return title, chapters, nil
}

func parseRouterData(raw string) map[string]any {
	idx := regexp.MustCompile(`(?s)(?:window\.)?_ROUTER_DATA\s*=\s*`).FindStringIndex(raw)
	if idx == nil {
		return nil
	}
	var data map[string]any
	dec := json.NewDecoder(strings.NewReader(raw[idx[1]:]))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return nil
	}
	return data
}

func (p *hongguoProvider) resolveHongguoMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	source, seriesID, ok := core.SplitProviderDramaID(task.DramaID)
	if !ok || source != sourceHongguo {
		return core.ProviderMedia{}, fmt.Errorf("红果章节缺少剧 ID，请刷新剧库后重新加入队列")
	}
	videoID := strings.TrimPrefix(task.Chapter.VideoURL, "hongguo-cenc://")
	if !hongguoNumericID.MatchString(videoID) || !hongguoNumericID.MatchString(seriesID) {
		return core.ProviderMedia{}, fmt.Errorf("红果章节 ID 无效，请重新获取章节")
	}
	media, nativeErr := p.resolveHongguoAppMedia(ctx, videoID)
	if nativeErr == nil {
		return media, nil
	}
	if err := ctx.Err(); err != nil {
		return core.ProviderMedia{}, err
	}
	media, pageErr := p.resolveHongguoWebMedia(ctx, seriesID, videoID)
	if pageErr == nil {
		return media, nil
	}
	if err := ctx.Err(); err != nil {
		return core.ProviderMedia{}, err
	}
	return core.ProviderMedia{}, fmt.Errorf("红果 App 取流失败：%v；网页取流失败：%w", core.PublicError(nativeErr), core.PublicError(pageErr))
}

func (p *hongguoProvider) resolveHongguoWebMedia(ctx context.Context, seriesID, videoID string) (core.ProviderMedia, error) {
	pageURL := hongguoBaseURL + "/player/" + url.PathEscape(seriesID) + "/" + url.PathEscape(videoID)
	body, err := p.text(ctx, pageURL, hongguoBaseURL+"/")
	if err != nil {
		return core.ProviderMedia{}, err
	}
	page := routerLoaderMap(parseRouterData(body), "player_", "player_page")
	if core.MapString(page, "vid") != videoID || core.MapString(page, "series_id") != seriesID {
		return core.ProviderMedia{}, fmt.Errorf("红果未返回所请求的剧集，可能仅允许网页试看；请在站点确认该集的访问权限")
	}
	info, _ := page["video_player_info"].(map[string]any)
	addresses := hongguoMediaAddresses(info)
	if len(addresses) == 0 {
		return core.ProviderMedia{}, fmt.Errorf("红果该集未提供公开播放地址，可能需要登录或 App 授权；不会将试看集冒充该集下载")
	}
	duration, _ := strconv.ParseFloat(core.MapString(info, "duration"), 64)
	media := core.ProviderMedia{URL: addresses[0], Referer: hongguoBaseURL + "/", Duration: time.Duration(duration * float64(time.Second))}
	for _, address := range addresses {
		variant := media
		variant.URL, variant.Variants = address, nil
		media.Variants = append(media.Variants, variant)
	}
	return media, nil
}

func hongguoDramaFromAny(v any, category string) core.Drama {
	m, ok := v.(map[string]any)
	if !ok {
		return core.Drama{}
	}
	vd, _ := m["video_data"].(map[string]any)
	if len(vd) == 0 {
		vd = m
	}
	sourceID := core.FirstNonEmpty(core.MapString(vd, "series_id_str", "series_id"), core.MapString(m, "series_id_str", "series_id"), core.MapString(vd, "keyword"), core.MapString(m, "keyword"))
	if !hongguoNumericID.MatchString(sourceID) {
		return core.Drama{}
	}
	title := core.FirstNonEmpty(core.MapString(vd, "series_title", "series_name", "title"), core.MapString(m, "series_name", "name"), sourceID)
	cover := core.FirstNonEmpty(core.MapString(vd, "series_cover", "cover"), core.MapString(m, "series_cover"))
	intro := core.FirstNonEmpty(core.MapString(vd, "series_intro", "video_desc"), core.MapString(m, "series_intro"))
	count := core.FirstNonEmpty(core.MapString(vd, "episode_cnt"), core.MapString(m, "episode_cnt"))
	remark := core.FirstNonEmpty(core.MapString(vd, "episode_right_text"), core.MapString(m, "episode_right_text"))
	releaseStatus := core.ReleaseStatusFromRemark(remark)
	if core.MapString(vd, "series_status") == "1" {
		releaseStatus = "finished"
	} else if core.MapString(vd, "series_status") == "0" {
		releaseStatus = "ongoing"
	}
	if remark == "" && count != "" {
		remark = "共" + count + "集"
	}
	tags := core.MapStringSlice(vd, "tags")
	for _, value := range anyList(vd["category_list"]) {
		item, _ := value.(map[string]any)
		if name := core.MapString(item, "name"); name != "" && !slices.Contains(tags, name) {
			tags = append(tags, name)
		}
	}
	var categories []struct {
		Name string `json:"name"`
	}
	if json.Unmarshal([]byte(core.MapString(vd, "category_schema")), &categories) == nil {
		for _, item := range categories {
			if item.Name != "" && !slices.Contains(tags, item.Name) {
				tags = append(tags, item.Name)
			}
		}
	}
	genre := core.MapString(vd, "category_name", "categoryName", "category")
	if genre == "" && len(tags) > 0 {
		genre = tags[0]
	}
	return core.Drama{ID: core.ProviderDramaID(sourceHongguo, sourceID), Source: sourceHongguo, SourceID: sourceID, Title: title, Name: title, Desc: intro, Intro: intro, Cover: cover, CoverURL: cover, CategoryName: core.FirstNonEmpty(genre, category), ChannelName: "红果", Remark: remark, TotalEpisode: count, EpisodeCount: count, Tags: tags, ReleaseStatus: releaseStatus, Score: core.MapString(vd, "score"), Views: core.MapString(vd, "series_play_cnt", "play_cnt"), Heat: hongguoHeat(vd), OnlineDate: hongguoOnlineDate(vd, time.Now())}
}

func nestedMap(v any, keys ...string) map[string]any {
	cur, _ := v.(map[string]any)
	for _, key := range keys {
		if cur == nil {
			return nil
		}
		cur, _ = cur[key].(map[string]any)
	}
	return cur
}

func routerLoaderMap(data map[string]any, names ...string) map[string]any {
	loader, _ := data["loaderData"].(map[string]any)
	for _, name := range names {
		if page, _ := loader[name].(map[string]any); len(page) > 0 {
			return page
		}
	}
	for key, value := range loader {
		for _, name := range names {
			if strings.TrimSuffix(name, "$") != "" && strings.HasPrefix(key, strings.TrimSuffix(name, "$")) {
				if page, _ := value.(map[string]any); len(page) > 0 {
					return page
				}
			}
		}
	}
	return nil
}

func anyList(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case map[string]any:
		for _, key := range []string{"list", "items", "data"} {
			if out := anyList(x[key]); len(out) > 0 {
				return out
			}
		}
	}
	return nil
}
