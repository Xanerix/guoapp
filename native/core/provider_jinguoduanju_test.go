package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJinguoDuanjuAliasesAndDramaParsing(t *testing.T) {
	if got := (&Downloader{cfg: defaultConfig()}).providerBaseURL(sourceJinguoDuanju); got != jinguoDuanjuBaseURL {
		t.Fatalf("unexpected default jinguoDuanju endpoint: %s", got)
	}
	if got := (&Downloader{cfg: Config{JinguoDuanjuURL: "https://jgdj.app/"}}).providerBaseURL(sourceJinguoDuanju); got != "https://jgdj.app" {
		t.Fatalf("content host override was ignored: %s", got)
	}
	if got := (&Downloader{cfg: Config{JinguoDuanjuURL: "https://example.com"}}).providerBaseURL(sourceJinguoDuanju); got != jinguoDuanjuBaseURL {
		t.Fatalf("unknown host was used as the catalog endpoint: %s", got)
	}
	for _, address := range []string{
		"https://jinguoduanju.app/play/1",
		"https://play.jinguoduanju.app/",
		"https://jgdj.app/",
	} {
		if got := providerSourceForURL(address); got != sourceJinguoDuanju {
			t.Fatalf("jinguoDuanju host was not recognized: %s -> %s", address, got)
		}
	}
	for _, alias := range []string{"jinguoduanju", "jinguoduanju.app", "jgdj.app"} {
		if got := canonicalProviderSource(alias); got != sourceJinguoDuanju {
			t.Fatalf("jinguoDuanju alias was not canonicalized: %s -> %s", alias, got)
		}
	}
	if got := providerSourceForURL("https://9fu8e.com/"); got == sourceJinguoDuanju {
		t.Fatal("the unrelated 禁果 landing host was mapped to 禁果短剧")
	}
	if !validJinguoDuanjuCategory("home") || !validJinguoDuanjuCategory("tag:22") || validJinguoDuanjuCategory("tag:古装") || validJinguoDuanjuCategory("nav:") {
		t.Fatal("jinguoDuanju category validation changed")
	}
	row := map[string]any{"id": "2137", "title": "禁果短剧样本", "episodeCount": "12", "coverUrl": "/media/covers/sample.png", "heat": "8", "publishedAt": "2026-09-23T10:20:30+08:00", "filmType": "FREE", "tags": []any{"都市", "职场"}}
	drama, err := jinguoDuanjuDramaFromMap(row, jinguoDuanjuBaseURL, "")
	if err != nil || drama.ID != "jinguoduanju:2137" || drama.EpisodeCount != 12 || drama.ChannelName != "禁果短剧" || drama.CategoryName != "都市" || drama.Heat != "8" || drama.OnlineDate != "2026-09-23" || drama.VIP == nil || *drama.VIP || !strings.HasPrefix(nativeText(drama.Cover), jinguoDuanjuBaseURL+"/media/covers/") {
		t.Fatalf("jinguoDuanju row parsed incorrectly: %+v %v", drama, err)
	}
	if _, err := jinguoDuanjuDramaFromMap(row, jinguoDuanjuBaseURL, "2138"); err == nil {
		t.Fatal("jinguoDuanju accepted a mismatched source id")
	}
	page, more, err := jinguoDuanjuPage([]Drama{{ID: "jinguoduanju:1"}, {ID: "jinguoduanju:2"}}, 1)
	if err != nil || more || len(page) != 2 {
		t.Fatalf("short jinguoDuanju page was split: %+v more=%t err=%v", page, more, err)
	}
}

func TestJinguoDuanjuCatalogDetailAndPlaybackDoNotUnlock(t *testing.T) {
	var sawUnlock bool
	var playAuthorization string
	var catalogRequests int
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "/unlock") || strings.Contains(request.URL.Path, "/payments") {
			sawUnlock = true
			http.Error(response, "unlock should not be called", http.StatusForbidden)
			return
		}
		switch request.URL.Path {
		case "/api/v1/auth/device":
			body, _ := io.ReadAll(request.Body)
			if !strings.Contains(string(body), `"deviceId":"h5:`) {
				t.Errorf("device auth body changed: %s", body)
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"token":"guest-token","user":{"id":1}}`)
		case "/api/v1/top-navigation":
			_, _ = io.WriteString(response, `[{"id":1,"frontendName":"标签系统","targetType":"SYSTEM","active":true,"valid":true},{"id":2,"frontendName":"都市","targetType":"CATEGORIES","active":true,"valid":true}]`)
		case "/api/v1/filter-config":
			_, _ = io.WriteString(response, `[{"id":9,"active":true,"frontendVisible":true,"tags":[{"id":22,"name":"古装","active":true,"frontendVisible":true}]}]`)
		case "/api/v1/dramas":
			catalogRequests++
			if request.Header.Get("Authorization") != "Bearer guest-token" {
				t.Errorf("catalog missing authorization: %s", request.Header.Get("Authorization"))
			}
			if request.URL.Query().Get("q") != "" && request.URL.Query().Get("q") != "样本" {
				t.Errorf("unexpected search query: %s", request.URL.RawQuery)
			}
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `[{"id":2137,"title":"禁果短剧样本","episodeCount":2,"coverUrl":"/media/covers/sample.png","filmType":"FREE","tags":["都市"]},{"id":2138,"title":"另一部","episodeCount":1,"filmType":"VIP","tags":["古装"]},{"id":2137,"title":"禁果短剧样本"},{"id":2139,"title":""}]`)
		case "/api/v1/dramas/2137":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"drama":{"id":2137,"title":"禁果短剧样本","episodeCount":2,"filmType":"FREE","tags":["都市"]},"description":"简介","episodes":[{"id":5671,"number":1,"title":"第1集","locked":false},{"id":5672,"number":2,"title":"第2集","locked":true}]}`)
		case "/api/v1/episodes/5671/play":
			playAuthorization = request.Header.Get("Authorization")
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"episodeId":5671,"videoUrl":"`+serverURL(request)+`/media/index.m3u8","allowance":{"allowed":true}}`)
		case "/api/v1/episodes/5672/play":
			response.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(response, `{"episodeId":5672,"videoUrl":"","allowance":{"allowed":false}}`)
		case "/media/index.m3u8":
			if request.Header.Get("Referer") == "" {
				t.Error("playlist request missing referer")
			}
			_, _ = io.WriteString(response, "#EXTM3U\n#EXTINF:1.0,\nsegment.ts\n")
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	d := &Downloader{cfg: Config{}, client: server.Client(), limiter: newRequestLimiter(3, 0), providerHosts: map[string]string{}}
	client := d.jinguoDuanjuClient()
	client.base = server.URL + "/api/v1"
	client.site = server.URL

	items, more, err := d.fetchJinguoDuanjuCatalogPage(context.Background(), 1, "", "")
	if err != nil || more || len(items) != 2 || items[0].ID != "jinguoduanju:2137" || items[1].VIP == nil || !*items[1].VIP {
		t.Fatalf("catalog page parsed incorrectly: %+v more=%t err=%v", items, more, err)
	}
	if rest, more, err := d.fetchJinguoDuanjuCatalogPage(context.Background(), 2, "", ""); err != nil || more || len(rest) != 0 || catalogRequests != 1 {
		t.Fatalf("second page did not reuse the cached listing: %d more=%t err=%v requests=%d", len(rest), more, err, catalogRequests)
	}
	categories, err := d.fetchJinguoDuanjuCategories(context.Background())
	if err != nil || len(categories) != 4 || categories[2].ID != "nav:2" || categories[3].ID != "tag:22" {
		t.Fatalf("categories parsed incorrectly: %+v err=%v", categories, err)
	}
	found, more, err := d.fetchJinguoDuanjuCatalogPage(context.Background(), 1, "", "样本")
	if err != nil || more || len(found) != 2 {
		t.Fatalf("search page parsed incorrectly: %d more=%t err=%v", len(found), more, err)
	}
	drama, chapters, err := d.fetchJinguoDuanjuDetail(context.Background(), "2137")
	if err != nil || drama.Desc != "简介" || len(chapters) != 2 || !chapters[1].VIP || chapters[0].VideoURL != "jinguoduanju-play://5671" {
		t.Fatalf("detail parsed incorrectly: %+v chapters=%+v err=%v", drama, chapters, err)
	}
	media, err := d.resolveProviderMedia(context.Background(), Task{DramaID: drama.ID, Chapter: chapters[0], Index: 1})
	if err != nil || !strings.Contains(media.Playlist, "#EXTM3U") || playAuthorization != "Bearer guest-token" {
		t.Fatalf("playback was not resolved: %+v auth=%s err=%v", media, playAuthorization, err)
	}
	if _, err := d.resolveProviderMedia(context.Background(), Task{DramaID: drama.ID, Chapter: chapters[1], Index: 2}); err == nil || sawUnlock {
		t.Fatalf("locked episode was unlocked or accepted: unlock=%t err=%v", sawUnlock, err)
	}
}

func serverURL(request *http.Request) string {
	return "https://" + request.Host
}
