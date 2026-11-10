package core

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

var rankingScriptTags = regexp.MustCompile(`(?is)<script\b[^>]*>`)
var rankingJSONLD = regexp.MustCompile(`(?is)<script\b[^>]*type=["']application/ld\+json["'][^>]*>(.*?)</script>`)
var rankingSourceID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,100}$`)
var errHongguoRankingIncomplete = errors.New("红果榜单数据尚未完整返回，请稍后重试")
var errHongguoRankingFormat = errors.New("红果榜单格式或分页已变化，请稍后重试")

type hongguoRankingContent struct {
	Success bool `json:"isSuccess"`
	Rows    []struct {
		ID          string   `json:"id"`
		SeriesID    string   `json:"seriesId"`
		Rank        int      `json:"rank"`
		Title       string   `json:"title"`
		Heat        string   `json:"heatText"`
		Score       string   `json:"scoreText"`
		Tags        []string `json:"tags"`
		Description string   `json:"description"`
		EpisodeIDs  []string `json:"episodeVids"`
		Cover       any      `json:"cover"`
	} `json:"rankList"`
	Pagination struct {
		Page       int `json:"pageNum"`
		TotalPages int `json:"totalPages"`
	} `json:"pagination"`
}

func parseHongguoRanking(body string, board RankingBoard, page int) (RankingPage, error) {
	failure := errHongguoRankingFormat
	loaderKey := "rank_" + board.Path + "/page"
	loader := nestedMap(parseRouterData(body), "loaderData", loaderKey)
	if len(loader) > 0 && (MapString(loader, "rankKey") != board.UpstreamKey || MapString(loader, "pageNum") != strconv.Itoa(page)) {
		return RankingPage{}, failure
	}
	var content hongguoRankingContent
	if inline := nestedMap(loader, "content"); len(inline) > 0 {
		raw, err := json.Marshal(inline)
		if err != nil || json.Unmarshal(raw, &content) != nil {
			return RankingPage{}, failure
		}
	} else if merged, found := parseHongguoMergeLoader(body, loaderKey); found {
		content = merged
	} else {
		for _, tag := range rankingScriptTags.FindAllString(body, -1) {
			if ExtractAttr(tag, "data-fn-name") != "r" || ExtractAttr(tag, "data-script-src") != "modern-run-router-data-fn" {
				continue
			}
			var args []json.RawMessage
			if json.Unmarshal([]byte(ExtractAttr(tag, "data-fn-args")), &args) != nil || len(args) != 3 {
				continue
			}
			var route, field string
			if json.Unmarshal(args[0], &route) != nil || json.Unmarshal(args[1], &field) != nil || route != loaderKey || field != "content" {
				continue
			}
			if json.Unmarshal(args[2], &content) != nil {
				return RankingPage{}, failure
			}
			break
		}
	}
	if !content.Success || content.Rows == nil {
		return parseHongguoRankingDocument(body, board, page, loader)
	}
	if content.Pagination.Page != page || content.Pagination.TotalPages < page || content.Pagination.TotalPages > 500 {
		return RankingPage{}, failure
	}
	result := RankingPage{Items: make([]RankingItem, 0, len(content.Rows)), TotalPages: content.Pagination.TotalPages, HasMore: page < content.Pagination.TotalPages, UpdatedText: MapString(loader, "updatedText")}
	seen := map[string]bool{}
	previous := (page - 1) * 20
	for _, row := range content.Rows {
		id := FirstNonEmpty(row.SeriesID, row.ID)
		if !hongguoNumericID.MatchString(id) || row.ID != "" && row.SeriesID != "" && row.ID != row.SeriesID || strings.TrimSpace(row.Title) == "" || row.Rank <= previous || row.Rank > page*20 || seen[id] {
			return RankingPage{}, failure
		}
		seen[id], previous = true, row.Rank
		drama := Drama{ID: ProviderDramaID(sourceHongguo, id), Source: sourceHongguo, SourceID: id, Title: row.Title, Name: row.Title, Desc: row.Description, Intro: row.Description, Tags: row.Tags, Heat: row.Heat, Score: strings.TrimPrefix(row.Score, "评分"), ChannelName: "红果"}
		if cover := hongguoCoverAddress(CoverPathFromAny(row.Cover)); cover != "" {
			drama.Cover, drama.CoverURL = cover, cover
		}
		if len(row.Tags) > 0 {
			drama.CategoryName = row.Tags[0]
		}
		if len(row.EpisodeIDs) > 0 {
			drama.TotalEpisode = len(row.EpisodeIDs)
		}
		result.Items = append(result.Items, RankingItem{Rank: row.Rank, Drama: drama, Metric: row.Heat})
	}
	if len(result.Items) == 0 && result.HasMore {
		return RankingPage{}, failure
	}
	return result, nil
}

func parseHuangdouRanking(decoded any, page int) (RankingPage, error) {
	data := huangdouDataMap(decoded)
	rows, valid := data["list"].([]any)
	if !valid {
		return RankingPage{}, errors.New("黄豆未返回有效榜单")
	}
	if len(rows) > 20 {
		return RankingPage{}, errors.New("黄豆榜单分页格式已变化")
	}
	result := RankingPage{Items: make([]RankingItem, 0, len(rows)), HasMore: len(rows) == 20}
	seen := map[string]bool{}
	for index, value := range rows {
		row, valid := value.(map[string]any)
		id := strings.TrimPrefix(FirstNonEmpty(MapString(row, "id"), MapString(row, "drama_id")), "rp_")
		title := MapString(row, "name", "title", "t")
		if !valid || !rankingSourceID.MatchString(id) || strings.TrimSpace(title) == "" || seen[id] {
			return RankingPage{}, errors.New("黄豆榜单包含无效或重复条目")
		}
		seen[id] = true
		drama := huangdouDramaFromMap(row)
		drama.ID, drama.Source, drama.SourceID = ProviderDramaID(sourceHuangdou, id), sourceHuangdou, id
		metric := ""
		if drama.Heat != "" {
			metric = drama.Heat + "热度"
		}

		result.Items = append(result.Items, RankingItem{Rank: (page-1)*20 + index + 1, Drama: drama, Metric: metric})
	}
	return result, nil
}
