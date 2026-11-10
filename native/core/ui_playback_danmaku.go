package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const danmakuWindowMS = 30_000

type hongguoDanmakuCacheEntry struct {
	page    DanmakuPage
	err     error
	expires time.Time
}

type hongguoDanmakuCall struct {
	done  chan struct{}
	entry hongguoDanmakuCacheEntry
}

func hongguoPlaybackIDs(task Task) (string, string, bool) {
	source, seriesID, valid := SplitProviderDramaID(task.DramaID)
	if !valid || source != sourceHongguo || task.Chapter.Source != sourceHongguo || !strings.HasPrefix(task.Chapter.VideoURL, "hongguo-cenc://") {
		return "", "", false
	}
	videoID := strings.TrimPrefix(task.Chapter.VideoURL, "hongguo-cenc://")
	return seriesID, videoID, hongguoNumericID.MatchString(seriesID) && hongguoNumericID.MatchString(videoID)
}

func (p *hongguoProvider) hongguoDanmaku(ctx context.Context, seriesID, videoID string, start, duration int64) (DanmakuPage, error) {
	if err := ctx.Err(); err != nil {
		return DanmakuPage{}, err
	}
	if !hongguoNumericID.MatchString(seriesID) || !hongguoNumericID.MatchString(videoID) || start < 0 || duration <= start || duration > DanmakuMaxDurationMS {
		return DanmakuPage{}, errors.New("弹幕请求参数无效")
	}
	key := fmt.Sprintf("%s:%s:%d:%d", seriesID, videoID, start, duration)
	client := p.client
	client.mu.Lock()
	if entry, found := client.danmaku[key]; found && time.Now().Before(entry.expires) {
		client.mu.Unlock()
		return cloneDanmakuPage(entry.page), entry.err
	}
	if pending := client.danmakuPending[key]; pending != nil {
		client.mu.Unlock()
		select {
		case <-ctx.Done():
			return DanmakuPage{}, ctx.Err()
		case <-pending.done:
			if ctx.Err() == nil && (errors.Is(pending.entry.err, context.Canceled) || errors.Is(pending.entry.err, context.DeadlineExceeded)) {
				return p.hongguoDanmaku(ctx, seriesID, videoID, start, duration)
			}
			return cloneDanmakuPage(pending.entry.page), pending.entry.err
		}
	}
	if client.danmakuPending == nil {
		client.danmakuPending = map[string]*hongguoDanmakuCall{}
	}
	if len(client.danmakuPending) >= 16 {
		client.mu.Unlock()
		return DanmakuPage{}, errors.New("弹幕请求繁忙")
	}
	pending := &hongguoDanmakuCall{done: make(chan struct{})}
	client.danmakuPending[key] = pending
	client.mu.Unlock()
	ctx = context.WithValue(ctx, BackgroundCatalogKey{}, true)
	ctx = context.WithValue(ctx, hongguoCommentKey{}, true)
	body := map[string]any{
		"comment_source": 601, "server_channel": 1000, "group_id": videoID, "group_type": 30,
		"comment_type": 20, "sort": 1, "count": 90, "cursor": "", "aid": 8662, "compliance_status": 0,
		"business_param": map[string]any{"book_id": seriesID, "start_offset_time": start, "playlet_item_duration": duration, "need_danmaku_guide_type": []int{1, 3, 4, 2}},
	}
	result, err := p.hongguoAppRequest(ctx, http.MethodPost, "/novel/commentapi/comment/list/"+videoID+"/v1/", nil, body)
	var page DanmakuPage
	if err == nil {
		page, err = parseHongguoDanmaku(result, videoID, start, duration)
	}
	entry := hongguoDanmakuCacheEntry{page: page, err: err, expires: time.Now().Add(5 * time.Minute)}
	if err != nil {
		entry.expires = time.Now().Add(15 * time.Second)
	}
	client.mu.Lock()
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		if client.danmaku == nil || len(client.danmaku) >= 128 {
			client.danmaku = map[string]hongguoDanmakuCacheEntry{}
		}
		client.danmaku[key] = entry
	}
	pending.entry = entry
	delete(client.danmakuPending, key)
	close(pending.done)
	client.mu.Unlock()
	return cloneDanmakuPage(page), err
}

func cloneDanmakuPage(page DanmakuPage) DanmakuPage {
	page.Items = append([]DanmakuItem{}, page.Items...)
	return page
}

func parseHongguoDanmaku(result map[string]any, videoID string, start, duration int64) (DanmakuPage, error) {
	data := nestedMap(result, "data")
	rows, valid := data["data_list"].([]any)
	next, err := strconv.ParseInt(MapString(nestedMap(data, "extra"), "next_query_danmaku_list_time"), 10, 64)
	if !valid || err != nil || next <= start || next > DanmakuMaxDurationMS {
		return DanmakuPage{}, errors.New("红果弹幕时间段格式无效")
	}
	page := DanmakuPage{EpisodeID: videoID, Items: []DanmakuItem{}, StartMS: start, NextMS: min(next, duration)}
	var cursor struct {
		Total int64 `json:"danmaku_count"`
	}
	if raw := MapString(nestedMap(data, "common_list_info"), "cursor"); len(raw) <= 8192 && json.Unmarshal([]byte(raw), &cursor) == nil && cursor.Total >= 0 {
		page.Total = cursor.Total
	}
	seen := map[string]bool{}
	for _, raw := range rows {
		row, _ := raw.(map[string]any)
		comment := nestedMap(row, "comment")
		common := nestedMap(comment, "common")
		if MapString(common, "group_id") != videoID || MapString(common, "status") != "1" {
			continue
		}
		position, err := strconv.ParseInt(MapString(nestedMap(comment, "expand"), "offset_time"), 10, 64)
		text := strings.TrimSpace(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
				return ' '
			}
			return r
		}, MapString(nestedMap(common, "content"), "text")))
		id := MapString(comment, "comment_id")
		if err != nil || position < start || position >= page.NextMS || text == "" || id == "" || len(id) > 120 || seen[id] {
			continue
		}
		if runes := []rune(text); len(runes) > 180 {
			text = string(runes[:180]) + "…"
		}
		seen[id] = true
		page.Items = append(page.Items, DanmakuItem{ID: id, Text: text, TimeMS: position})
		if len(page.Items) == 90 {
			break
		}
	}
	sort.SliceStable(page.Items, func(i, j int) bool { return page.Items[i].TimeMS < page.Items[j].TimeMS })
	return page, nil
}
