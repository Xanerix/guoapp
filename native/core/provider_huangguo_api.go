package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const huangguoAISearchPageSize = 20

func (d *Downloader) huangguoAIAPI(ctx context.Context, path string, out any) error {
	envelope, err := json.Marshal(map[string]string{"method": http.MethodGet, "path": path, "body": "", "content_type": ""})
	if err != nil {
		return err
	}
	base := d.providerBaseURL(sourceHuangguoAI)
	body, err := d.fetchProviderRequest(ctx, base+"/api/edge/request", base+"/", envelope)
	if err != nil {
		return err
	}
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

func (d *Downloader) fetchHuangguoAIDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	sourceID = strings.TrimPrefix(strings.Trim(strings.TrimSpace(sourceID), "/"), "detail/")
	if !rankingSourceID.MatchString(sourceID) {
		return Drama{}, nil, errors.New("无效的黄果剧集 ID")
	}
	var detail map[string]any
	if err := d.huangguoAIAPI(ctx, "/api/videos/"+url.PathEscape(sourceID), &detail); err != nil {
		return Drama{}, nil, err
	}
	base := d.providerBaseURL(sourceHuangguoAI)
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

func (d *Downloader) fetchHuangguoAIPlayURL(ctx context.Context, sourceID string, episode int) (string, error) {
	if !rankingSourceID.MatchString(sourceID) || episode < 1 {
		return "", errors.New("黄果集数无效")
	}
	var play struct {
		VideoURL string `json:"video_url"`
	}
	path := fmt.Sprintf("/api/videos/%s/play?ep=%d&page_texts=0", url.PathEscape(sourceID), episode)
	if err := d.huangguoAIAPI(ctx, path, &play); err != nil {
		return "", err
	}
	return normalizeProviderMediaURL(d.providerBaseURL(sourceHuangguoAI)+"/", play.VideoURL), nil
}

func (d *Downloader) searchHuangguoAI(ctx context.Context, page int, query string) ([]Drama, bool, error) {
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
	if err := d.huangguoAIAPI(ctx, path, &result); err != nil {
		return nil, false, err
	}
	items := parseHuangguoAIJSONCards(result.Items, d.providerBaseURL(sourceHuangguoAI)+"/", "")
	return items, page < result.Pagination.Pages, nil
}

func parseHuangguoRanking(rows []map[string]any, pageURL string) (rankingPage, error) {
	failure := errors.New("黄果未返回有效榜单")
	if len(rows) == 0 || len(rows) > 100 {
		return rankingPage{}, failure
	}
	result := rankingPage{Items: make([]rankingItem, 0, len(rows)), TotalPages: 1}
	seen := map[string]bool{}
	for index, row := range rows {
		row["id"] = row["video_id"]
		drama, ok := dramaFromAIMap(row, pageURL, mapString(row, "cate_name"))
		if !ok || !rankingSourceID.MatchString(drama.SourceID) || seen[drama.SourceID] {
			return rankingPage{}, failure
		}
		seen[drama.SourceID] = true
		rank, err := strconv.Atoi(mapString(row, "rank"))
		if err != nil || rank < 1 {
			rank = index + 1
		}
		result.Items = append(result.Items, rankingItem{Rank: rank, Drama: drama, Metric: huangguoRankingMetric(row)})
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
