package core

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// hongguoPlanSeason 官方 plan/v 接口返回的单个季。
// 这些字段直接对应 response.data[].video_data[] 里的每一项。
type hongguoPlanSeason struct {
	SeriesID    string // series_id，用来切换季后请求详情
	Title       string
	SeasonIndex int
	Episodes    int
	Cover       string
	Description string
	DisablePlay bool
	// OnlineDate 待上线季的预计上线日期（yyyy-MM-dd），常规季为空。
	OnlineDate string
}

// hongguoUpcomingDatePattern 待上线季的预计上线文本，如「预计11月15日上线」。
var hongguoUpcomingDatePattern = regexp.MustCompile(`预计(\d{1,2})月(\d{1,2})日上线`)

// hongguoPlanSeasons 调用红果官方多季接口 /reading/bookapi/plan/v/。
//
// 实测（2026-11）：必须用 GET 且路径带尾斜杠——POST 或不带斜杠都会 404。
// 这个接口按 book_id（即剧的 series_id）返回官方认可的完整系列季列表，
// 含待上线季（episode_cnt=0、sub_title_list 带「预计X月X日上线」），
// 不同系列（同名不同作者）互不污染，不依赖搜索召回。
// 成功时返回完整列表（至少 1 项），失败时返回 ok=false，调用方 fallback 到搜索聚合。
//
// 关键参数（query string，非 body）：
//   - scene=10, from=single_col_same_ip_series 让红果返回"同系列的季列表"
//   - book_id + filter_ids 是当前剧的 series_id
func (downloader *Downloader) hongguoPlanSeasons(ctx context.Context, bookID string) ([]hongguoPlanSeason, bool) {
	if !hongguoNumericID.MatchString(bookID) {
		return nil, false
	}
	var seasons []hongguoPlanSeason
	offset := ""
	for page := 0; page < 3; page++ {
		extra := url.Values{}
		extra.Set("scene", "10")
		extra.Set("from", "single_col_same_ip_series")
		extra.Set("book_id", bookID)
		extra.Set("filter_ids", bookID)
		if offset != "" {
			extra.Set("offset", offset)
		}
		result, err := downloader.hongguoAppRequest(ctx, http.MethodGet, "/reading/bookapi/plan/v/", extra, nil)
		if err != nil {
			if len(seasons) > 0 {
				break
			}
			return nil, false
		}
		batch, hasMore, next := parseHongguoPlanSeasons(result, bookID)
		seasons = append(seasons, batch...)
		if !hasMore || next == "" {
			break
		}
		offset = next
	}
	if len(seasons) == 0 {
		return nil, false
	}
	sort.Slice(seasons, func(i, j int) bool {
		return seasons[i].SeasonIndex < seasons[j].SeasonIndex
	})
	return seasons, true
}

// parseHongguoPlanSeasons 解析 plan/v 的返回结构。
//
// 实测返回格式：response.data 是卡片数组，每个卡片含
// video_data[]（同系列各季，content_type=1004 正常季 / 1007 待上线预告）
// 与 book_data[]（原著小说，跳过）；has_more/next_offset 用于分页。
func parseHongguoPlanSeasons(result map[string]any, bookID string) ([]hongguoPlanSeason, bool, string) {
	data := result["data"]
	if data == nil {
		return nil, false, ""
	}

	var videoList []any
	hasMore, nextOffset := "", ""
	switch d := data.(type) {
	case []any:
		for _, item := range d {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if vd := anyList(m["video_data"]); len(vd) > 0 {
				videoList = append(videoList, vd...)
			}
			if hasMore == "" {
				hasMore = mapString(m, "has_more")
				nextOffset = mapString(m, "next_offset")
			}
		}
	case map[string]any:
		videoList = anyList(d["video_data"])
		hasMore = mapString(d, "has_more")
		nextOffset = mapString(d, "next_offset")
	default:
		return nil, false, ""
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
		title := strings.TrimSpace(mapString(row, "title"))
		if title == "" {
			// 待上线季 title 为空，预约信息里带完整剧名。
			if sub, ok := row["subscribe_item"].(map[string]any); ok {
				title = strings.TrimSpace(mapString(sub, "name"))
			}
		}
		if title == "" {
			title = "第" + strconv.Itoa(idx) + "季"
		}
		season := hongguoPlanSeason{
			SeriesID:    sid,
			Title:       title,
			SeasonIndex: idx,
			Episodes:    episodes,
			Cover:       mapString(row, "cover"),
			Description: mapString(row, "video_desc"),
			DisablePlay: anyBool(row["disable_play"]),
			OnlineDate:  hongguoUpcomingDate(row),
		}
		// 过滤不可播放的季（付费但未购买的）；待上线季保留（episodes=0，
		// 客户端 DramaSeason.upcoming 会显示「待上线」并禁用点击）。
		if season.DisablePlay && sid != bookID && episodes > 0 {
			continue
		}
		seasons = append(seasons, season)
	}

	return seasons, hasMore == "1" || hasMore == "true", nextOffset
}

// hongguoUpcomingDate 从待上线季的 sub_title_list 提取预计上线日期。
// 「预计11月15日上线」→ 当年年份补全为 yyyy-MM-dd；已过则顺延一年。
func hongguoUpcomingDate(row map[string]any) string {
	subs := anyList(row["sub_title_list"])
	for _, item := range subs {
		text, ok := item.(map[string]any)
		if !ok {
			continue
		}
		match := hongguoUpcomingDatePattern.FindStringSubmatch(mapString(text, "content"))
		if match == nil {
			continue
		}
		month, err1 := strconv.Atoi(match[1])
		day, err2 := strconv.Atoi(match[2])
		if err1 != nil || err2 != nil || month < 1 || month > 12 || day < 1 || day > 31 {
			continue
		}
		now := time.Now()
		date := time.Date(now.Year(), time.Month(month), day, 0, 0, 0, 0, time.Local)
		if date.Before(now) {
			date = date.AddDate(1, 0, 0)
		}
		return date.Format("2006-01-02")
	}
	return ""
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
