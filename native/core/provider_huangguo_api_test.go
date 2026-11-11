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

func TestHuangguoAILinesCoverEveryMirrorDomain(t *testing.T) {
	const address = "https://huangguoai.com/api/edge/request"
	candidates := providerURLCandidates(address)
	if len(candidates) == 0 || candidates[0] != address {
		t.Fatal("the primary line is no longer tried first")
	}
	for _, candidate := range candidates {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Path != "/api/edge/request" || providerSourceForURL(candidate) != sourceHuangguoAI {
			t.Fatal("a line was not recognized as huangguoai")
		}
	}
	generated := map[string]bool{}
	for _, mirror := range generatedHuangguoAIMirrors(address, 8) {
		// 随机子域只对当前主域名生成：旧域名整体被封后生成再多也访问不到。
		if !strings.HasSuffix(mirror, "."+huangguoAIMirrorDomains[0]) {
			t.Fatalf("generated line %s is not on the current mirror domain", mirror)
		}
		generated[huangguoAIMirrorDomains[0]] = true
	}
	if len(generated) != 1 {
		t.Fatal("generated lines should stay on the current mirror domain")
	}
	if providerSourceForURL("https://evil-lbvrhyir.cc/") != "" || providerSourceForURL("https://lbvrhyir.cc.example.test/") != "" {
		t.Fatal("an unrelated host was treated as a huangguoai line")
	}
}

func huangguoEdgeReply(data any) string {
	body, _ := json.Marshal(map[string]any{"status": 1, "msg": "", "data": data})
	return string(body)
}

// sourceFixturePublishScript 放行镜像发布页请求，返回空脚本：
// 合成用例只验证站内接口，不应被域名刷新请求干扰。
func sourceFixturePublishScript(request *http.Request) (*http.Response, bool) {
	if !strings.Contains(request.URL.Path, "publish.js") {
		return nil, false
	}
	return sourceFixtureResponse(request, 200, "var urls=[];\nvar subdomains=[];\n"), true
}

func TestHuangguoAIDetailAndPlaybackUseEdgeAPI(t *testing.T) {
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if response, ok := sourceFixturePublishScript(request); ok {
			return response, nil
		}
		if request.URL.Hostname() != "huangguoai.com" {
			return nil, errors.New("only synthetic huangguoai requests are allowed")
		}
		// 新版取流是公开 POST 接口，不再经过 edge 信封。
		if request.URL.Path == "/api/videos/12/episodes/2/playback" && request.Method == http.MethodPost {
			return sourceFixtureResponse(request, 200, `{"status":1,"msg":"","data":{"streams":{"main":"https://media.example.test/12/2.mp4?auth_key=synthetic"}}}`), nil
		}
		target := huangguoEdgeTarget(t, request)
		switch target.Path {
		case "/api/videos/12":
			return sourceFixtureResponse(request, 200, huangguoEdgeReply(map[string]any{
				"id": 12, "title": "合成剧", "description": "合成简介", "cover": "/covers/12.jpg", "episode_count": 2, "is_finished": false,
				"episodes": []any{map[string]any{"ep_num": 2, "title": "第2集"}, map[string]any{"ep_num": 1, "title": ""}},
			})), nil
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

func TestHuangguoVideoResultsReSourceIDs(t *testing.T) {
	drama := reSourceDrama(Drama{ID: "huangguoai:12", Source: sourceHuangguoAI}, sourceHuangguoVideo)
	if drama.ID != "huangguo-video:12" || drama.Source != sourceHuangguoVideo {
		t.Fatalf("drama not re-sourced: %+v", drama)
	}
	chapters := reSourceChapters([]Chapter{
		{ID: "huangguoai:12:auto-1", Source: sourceHuangguoAI},
		{ID: "huangguoai:12:ep-2", Source: sourceHuangguoAI},
	}, sourceHuangguoVideo)
	if chapters[0].ID != "huangguo-video:12:auto-1" || chapters[1].ID != "huangguo-video:12:ep-2" {
		t.Fatalf("chapters not re-sourced: %+v", chapters)
	}
	for _, chapter := range chapters {
		if chapter.Source != sourceHuangguoVideo {
			t.Fatalf("chapter source = %s", chapter.Source)
		}
	}
	if (&Downloader{cfg: defaultConfig()}).providerBaseURL(sourceHuangguoVideo) == "https://huangguo.video" {
		t.Fatal("huangguo video should follow the huangguoai mirror hosts")
	}
}

func TestHuangguoAISearchUsesSitePagination(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		if response, ok := sourceFixturePublishScript(request); ok {
			return response, nil
		}
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
