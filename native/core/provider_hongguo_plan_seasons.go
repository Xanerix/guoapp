package core

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// hongguoPlanSeason 官方 plan/v 接口返回的单个季。
// 这些字段直接对应 response.data[].video_data[] 里的每一项。
type hongguoPlanSeason struct {
	SeriesID    string // series_id_str，用来切换季后请求详情
	Title       string
	SeasonIndex int
	Episodes    int
	Cover       string
	Description string
	DisablePlay bool
}

// hongguoPlanSeasons 调用红果官方多季接口 /reading/bookapi/plan/v。
//
// 这个接口会返回同一系列下的全部季，不依赖搜索是否召回了全部。
// 成功时返回完整列表（至少 1 项），失败时返回 ok=false，调用方 fallback 到搜索聚合。
//
// 关键参数：
//   - scene=10, from=single_col_same_ip_series 让红果返回"同系列的季列表"
//   - book_id + filter_ids 是当前剧的 series_id_str
func (downloader *Downloader) hongguoPlanSeasons(ctx context.Context, bookID string) ([]hongguoPlanSeason, bool) {
	if !hongguoNumericID.MatchString(bookID) {
		return nil, false
	}
	result, err := downloader.hongguoAppRequest(
		ctx,
		http.MethodPost,
		"/reading/bookapi/plan/v",
		nil,
		map[string]any{
			"scene":      10,
			"from":       "single_col_same_ip_series",
			"book_id":    bookID,
			"filter_ids": bookID,
		},
	)
	if err != nil {
		return nil, false
	}
	return parseHongguoPlanSeasons(result, bookID)
}

// parseHongguoPlanSeasons 解析 plan/v 的返回结构。
//
// 返回格式推测：response.data 是数组，每个元素是一个 book，
// 每个 book 里有 video_data[] 数组存放真正的季条目。
// 也可能 data 直接就是 video_data 数组——两种结构都兼容。
func parseHongguoPlanSeasons(result map[string]any, bookID string) ([]hongguoPlanSeason, bool) {
	data := result["data"]
	if data == nil {
		return nil, false
	}

	// 兼容两种结构：data 是 book 数组，或 data 直接是季数组。
	var videoList []any
	switch d := data.(type) {
	case []any:
		// data 是数组——遍历每个元素找 video_data
		for _, item := range d {
			if m, ok := item.(map[string]any); ok {
				// 先试 video_data
				if vd := anyList(m["video_data"]); len(vd) > 0 {
					videoList = append(videoList, vd...)
					continue
				}
				// 如果 item 本身就是季条目（有 series_id_str），直接加
				if sid := mapString(m, "series_id_str", "series_id"); sid != "" {
					videoList = append(videoList, m)
				}
			}
		}
	case map[string]any:
		videoList = anyList(d["video_data"])
		if len(videoList) == 0 {
			// data 直接是一个季条目
			if sid := mapString(d, "series_id_str", "series_id"); sid != "" {
				videoList = []any{d}
			}
		}
	default:
		return nil, false
	}

	if len(videoList) == 0 {
		return nil, false
	}

	var seasons []hongguoPlanSeason
	seen := map[string]bool{}
	for _, item := range videoList {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		sid := mapString(row, "series_id_str", "series_id")
		if sid == "" || !hongguoNumericID.MatchString(sid) {
			continue
		}
		if seen[sid] {
			continue
		}
		seen[sid] = true
		idx, _ := strconv.Atoi(mapString(row, "season_index"))
		if idx < 1 || idx > hongguoSearchSeasonLimit {
			idx = 1
		}
		episodes, _ := strconv.Atoi(mapString(row, "episode_cnt"))
		if episodes < 0 {
			episodes = 0
		}
		season := hongguoPlanSeason{
			SeriesID:    sid,
			Title:       strings.TrimSpace(mapString(row, "title")),
			SeasonIndex: idx,
			Episodes:    episodes,
			Cover:       mapString(row, "cover"),
			Description: mapString(row, "video_desc"),
			DisablePlay: anyBool(row["disable_play"]),
		}
		// 过滤不可播放的季（付费但未购买的）
		if season.DisablePlay && sid != bookID {
			continue
		}
		seasons = append(seasons, season)
	}

	if len(seasons) == 0 {
		return nil, false
	}

	// 按 season_index 排序
	sort.Slice(seasons, func(i, j int) bool {
		return seasons[i].SeasonIndex < seasons[j].SeasonIndex
	})

	return seasons, true
}

// anyBool 把 any 转 bool，解析失败返回 false。
func anyBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		return b == "true" || b == "1"
	case float64:
		return b != 0
	}
	return false
}

// _hongguoPlanSeasonsSingleBook 如果 plan/v 没返回多季，单独再查一次当前 book 自己的详情。
// 这个在 nativeSeasons 里用不到，但留着以备将来需要。
func _hongguoPlanSeasonsSingleBook(detail map[string]any) hongguoPlanSeason {
	sid := mapString(detail, "series_id_str", "series_id")
	idx, _ := strconv.Atoi(mapString(detail, "season_index"))
	if idx < 1 {
		idx = 1
	}
	episodes, _ := strconv.Atoi(mapString(detail, "episode_cnt"))
	return hongguoPlanSeason{
		SeriesID:    sid,
		Title:       strings.TrimSpace(mapString(detail, "title")),
		SeasonIndex: idx,
		Episodes:    episodes,
		Cover:       mapString(detail, "cover"),
		Description: mapString(detail, "video_desc"),
	}
}

// compile-time check that parseHongguoPlanSeasons signature is used
var _ = errors.New
