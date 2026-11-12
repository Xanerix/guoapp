package core

import (
	"context"
	"testing"
)

func TestParseHongguoPlanSeasonsOfficialSeries(t *testing.T) {
	// 实测结构：data[].video_data[] 为同系列各季，book_data 为原著小说（跳过）。
	result := map[string]any{
		"data": []any{
			map[string]any{
				"has_more": "0",
				"book_data": []any{
					map[string]any{"book_name": "持械入宋", "author": "茶根儿"},
				},
				"video_data": []any{
					map[string]any{
						"series_id":    "7671966197985856536",
						"title":        "持械入宋：第一季",
						"season_index": 1,
						"episode_cnt":  100,
						"cover":        "https://example.test/1.jpg",
						"video_desc":   "第一季简介",
						"disable_play": false,
					},
					map[string]any{
						"series_id":    "7676706788724460568",
						"title":        "持械入宋：第二季",
						"season_index": 2,
						"episode_cnt":  100,
					},
					map[string]any{
						// 待上线季：title 空、无集数，预约信息带名字与预计上线日期。
						"series_id":    "7699796892317649944",
						"title":        "",
						"season_index": 11,
						"episode_cnt":  0,
						"content_type": 1007,
						"sub_title_list": []any{
							map[string]any{"content": "预计11月15日上线"},
						},
						"subscribe_item": map[string]any{"name": "持械入宋：第十一季"},
					},
				},
			},
		},
	}
	seasons, hasMore, next := parseHongguoPlanSeasons(result, "7671966197985856536")
	if hasMore || next != "" {
		t.Fatalf("has_more/next = %v/%q", hasMore, next)
	}
	if len(seasons) != 3 {
		t.Fatalf("seasons = %+v", seasons)
	}
	if seasons[0].Title != "持械入宋：第一季" || seasons[0].Episodes != 100 {
		t.Fatalf("season 1 = %+v", seasons[0])
	}
	// 蹭名剧不在官方系列里——列表里第 2 季必须是正确的 7676706788724460568。
	if seasons[1].SeriesID != "7676706788724460568" {
		t.Fatalf("season 2 = %+v", seasons[1])
	}
	upcoming := seasons[2]
	if upcoming.Title != "持械入宋：第十一季" || upcoming.Episodes != 0 {
		t.Fatalf("upcoming season = %+v", upcoming)
	}
	if upcoming.OnlineDate == "" || len(upcoming.OnlineDate) != 10 {
		t.Fatalf("upcoming date = %q", upcoming.OnlineDate)
	}
}

func TestParseHongguoPlanSeasonsDropsDisabledPaidSeasons(t *testing.T) {
	result := map[string]any{
		"data": []any{
			map[string]any{
				"video_data": []any{
					map[string]any{"series_id": "1", "title": "第1季", "season_index": 1, "episode_cnt": 10},
					map[string]any{"series_id": "2", "title": "第2季", "season_index": 2, "episode_cnt": 10, "disable_play": true},
					map[string]any{"series_id": "3", "title": "第3季", "season_index": 3, "episode_cnt": 0, "disable_play": true},
				},
			},
		},
	}
	seasons, _, _ := parseHongguoPlanSeasons(result, "1")
	if len(seasons) != 2 {
		t.Fatalf("seasons = %+v", seasons)
	}
	if seasons[1].SeasonIndex != 3 || seasons[1].Episodes != 0 {
		// 待上线季（无集数）即使 disable_play 也保留，客户端显示「待上线」。
		t.Fatalf("upcoming season must be kept: %+v", seasons[1])
	}
}

func TestHongguoPlanSeasonsRejectsInvalidID(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := engine.downloader.hongguoPlanSeasons(context.Background(), "not-numeric"); ok {
		t.Fatal("non-numeric book id must fail fast")
	}
}
