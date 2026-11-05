package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func huangguoEdgeTarget(t *testing.T, request *http.Request) *url.URL {
	t.Helper()
	var envelope struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}
	if request.Method != http.MethodPost || request.URL.Path != "/api/edge/request" || json.NewDecoder(request.Body).Decode(&envelope) != nil || envelope.Method != http.MethodGet {
		t.Errorf("request bypassed the edge envelope: %s %s", request.Method, request.URL)
		return &url.URL{}
	}
	target, err := url.Parse(envelope.Path)
	if err != nil {
		t.Errorf("invalid envelope path %q", envelope.Path)
		return &url.URL{}
	}
	return target
}

func huangguoEdgeReply(data any) string {
	body, _ := json.Marshal(map[string]any{"status": 1, "msg": "", "data": data})
	return string(body)
}

func TestHuangguoAIDetailAndPlaybackUseEdgeAPI(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() != "huangguoai.com" {
			return nil, errors.New("only synthetic huangguoai requests are allowed")
		}
		target := huangguoEdgeTarget(t, request)
		switch target.Path {
		case "/api/videos/12":
			return sourceFixtureResponse(request, 200, huangguoEdgeReply(map[string]any{
				"id": 12, "title": "合成剧", "description": "合成简介", "cover": "/covers/12.jpg", "episode_count": 2, "is_finished": false,
				"episodes": []any{map[string]any{"ep_num": 2, "title": "第2集"}, map[string]any{"ep_num": 1, "title": ""}},
			})), nil
		case "/api/videos/12/play":
			if target.Query().Get("ep") != "2" {
				t.Errorf("requested the wrong episode: %s", target)
			}
			return sourceFixtureResponse(request, 200, huangguoEdgeReply(map[string]any{"video_url": "https://media.example.test/12/2.mp4?auth_key=synthetic"})), nil
		}
		return sourceFixtureResponse(request, 200, `{"status":0,"msg":"合成剧集不存在"}`), nil
	})
	drama, chapters, err := d.fetchHuangguoAIDetail(context.Background(), "12")
	if err != nil || drama.ID != "huangguoai:12" || drama.Title != "合成剧" || drama.Desc != "合成简介" || drama.ReleaseStatus != "ongoing" || drama.Cover != "https://huangguoai.com/covers/12.jpg" {
		t.Fatal("detail metadata was not read from the API")
	}
	if len(chapters) != 2 || chapters[0].ID != "huangguoai:12:auto-1" || chapters[0].Title != "第1集" || chapters[1].ID != "huangguoai:12:ep-2" || chapters[1].PageURL != "https://huangguoai.com/video/12/ep-2/" {
		t.Fatal("chapter identities changed")
	}
	media, err := d.resolveProviderMedia(context.Background(), Task{DramaID: drama.ID, Chapter: chapters[1], Index: 2})
	if err != nil || media.URL != "https://media.example.test/12/2.mp4?auth_key=synthetic" {
		t.Fatal("playback did not use the play API")
	}
	if _, _, err := d.fetchHuangguoAIDetail(context.Background(), "13"); err == nil || !strings.Contains(err.Error(), "合成剧集不存在") {
		t.Fatal("upstream failure message was discarded")
	}
}

func TestHuangguoAISearchUsesSitePagination(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		target := huangguoEdgeTarget(t, request)
		if target.Path != "/api/search" || target.Query().Get("q") != "合成 关键词" {
			return nil, errors.New("only synthetic search requests are allowed")
		}
		page, _ := strconv.Atoi(target.Query().Get("page"))
		return sourceFixtureResponse(request, 200, huangguoEdgeReply(map[string]any{
			"items":      []any{map[string]any{"id": 50 + page, "title": "合成结果" + strconv.Itoa(page), "episode_count": 3}},
			"pagination": map[string]any{"page": page, "pages": 2, "size": 20, "total": 2},
		})), nil
	})
	first, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangguoAI, Query: "合成 关键词", Page: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != "huangguoai:51" || !first.HasMore || first.LocalSearch {
		t.Fatal("search did not reach the site")
	}
	last, err := engine.nativeCatalog(context.Background(), nativeInput{Source: sourceHuangguoAI, Query: "合成 关键词", Page: 2})
	if err != nil || len(last.Items) != 1 || last.Items[0].ID != "huangguoai:52" || last.HasMore {
		t.Fatal("search pagination ignored the site total")
	}
}
