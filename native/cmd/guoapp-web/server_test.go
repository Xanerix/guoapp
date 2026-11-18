package main

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"duanjuapp/native/core"
)

type fixtureSource struct{ address string }

func (source fixtureSource) Descriptor() core.SourceDescriptor {
	return core.SourceDescriptor{ID: "webfixture", Name: "网页测试站源", ValidCategory: func(id string) bool { return id == "drama" }}
}

func (source fixtureSource) Categories(context.Context, bool) ([]core.Category, error) {
	return []core.Category{{ID: "drama", Name: "剧情"}}, nil
}

func (source fixtureSource) CatalogPage(context.Context, int, string) ([]core.Drama, bool, error) {
	return []core.Drama{source.drama()}, false, nil
}

func (source fixtureSource) Search(_ context.Context, _ int, query string) ([]core.Drama, bool, error) {
	if query != "测试" {
		return nil, false, nil
	}
	return []core.Drama{source.drama()}, false, nil
}

func (source fixtureSource) Detail(_ context.Context, id string) (core.Drama, []core.Chapter, error) {
	drama := source.drama()
	drama.ID = core.ProviderDramaID("webfixture", id)
	if id == "second" {
		return drama, []core.Chapter{{ID: drama.ID + ":1", Source: "webfixture", Title: "第 2 季第 1 集"}}, nil
	}
	return drama, []core.Chapter{
		{ID: drama.ID + ":1", Source: "webfixture", Title: "第 1 集"},
		{ID: drama.ID + ":2", Source: "webfixture", Title: "第 2 集"},
	}, nil
}

func (source fixtureSource) Seasons(context.Context, string) ([]core.Season, error) {
	first := source.drama()
	second := first
	second.ID, second.Title = "webfixture:second", "第二季"
	return []core.Season{{Number: 1, Drama: first}, {Number: 2, Drama: second}}, nil
}

func (source fixtureSource) ResolveMedia(_ context.Context, task core.Task) (core.ProviderMedia, error) {
	if task.Chapter.ID == "webfixture:demo:2" || task.DramaID == "webfixture:second" {
		return core.ProviderMedia{URL: source.address + "/film.mp4", Quality: 1080,
			Variants: []core.ProviderMedia{{URL: source.address + "/lower.mp4", Quality: 720}}}, nil
	}
	return core.ProviderMedia{URL: source.address + "/index.m3u8",
		Playlist: "#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXTINF:2,\nsegment.ts\n#EXT-X-ENDLIST\n"}, nil
}

func (source fixtureSource) drama() core.Drama {
	return core.Drama{ID: "webfixture:demo", Source: "webfixture", Title: "测试 <🎬>", TotalEpisode: 2}
}

func TestWebLoginBrowseAndPlay(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content := "0123456789"
		switch r.URL.Path {
		case "/segment.ts":
			w.Header().Set("Content-Type", "video/mp2t")
		case "/film.mp4":
			w.Header().Set("Content-Type", "video/mp4")
		case "/lower.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			content = "abcdefghij"
		default:
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, r.URL.Path, time.Time{}, strings.NewReader(content))
	}))
	t.Cleanup(upstream.Close)

	lib, err := core.NewWebLibrary(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fixture := fixtureSource{address: upstream.URL}
	core.Register(fixture.Descriptor(), func(core.Host) core.Provider { return fixture })
	t.Cleanup(func() { core.Unregister("webfixture") })

	app := httptest.NewServer(newWebApp(lib, "fixture-password", false))
	t.Cleanup(app.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar: jar, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	request := func(method, path string, form url.Values, headers map[string]string, status int) (string, http.Header) {
		t.Helper()
		req, err := http.NewRequest(method, app.URL+path, strings.NewReader(form.Encode()))
		if err != nil {
			t.Fatal(err)
		}
		if form != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != status {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, res.StatusCode, status, body)
		}
		return string(body), res.Header
	}

	_, headers := request("GET", "/", nil, nil, 303)
	if headers.Get("Location") != "/login" {
		t.Fatal("missing login redirect")
	}
	_, headers = request("GET", "/login", nil, nil, 200)
	if headers.Get("Referrer-Policy") != "same-origin" {
		t.Fatalf("LAN forms need to keep their same-origin request origin: %s", headers.Get("Referrer-Policy"))
	}
	request("GET", "/media/unknown/film.mp4", nil, nil, 401)
	request("GET", "/watch/seasons?id=webfixture%3Ademo", nil, nil, 303)
	request("POST", "/login", url.Values{"password": {"invalid"}}, nil, 401)
	request("POST", "/login", url.Values{"password": {"fixture-password"}}, map[string]string{"Sec-Fetch-Site": "cross-site"}, 403)
	request("POST", "/login", url.Values{"password": {"fixture-password"}}, map[string]string{"Origin": "https://other.example"}, 403)
	request("POST", "/login", url.Values{"password": {"fixture-password"}}, map[string]string{"Origin": "null"}, 403)
	_, headers = request("POST", "/login", url.Values{"password": {"fixture-password"}}, map[string]string{"Origin": app.URL}, 303)
	if cookie := headers.Get("Set-Cookie"); !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Strict") {
		t.Fatalf("cookie flags missing: %s", cookie)
	}
	catalog, _ := request("GET", "/?source=webfixture&category=drama", nil, nil, 200)
	if !strings.Contains(catalog, "剧情") || !strings.Contains(catalog, "测试 &lt;🎬&gt;") {
		t.Fatalf("catalog not rendered: %s", catalog)
	}
	results, _ := request("GET", "/?source=webfixture&q="+url.QueryEscape("测试"), nil, nil, 200)
	if !strings.Contains(results, "搜索结果") || !strings.Contains(results, "测试 &lt;🎬&gt;") {
		t.Fatalf("search result not rendered: %s", results)
	}
	detail, _ := request("GET", "/title?id=webfixture%3Ademo", nil, nil, 200)
	if !strings.Contains(detail, "第 2 集") {
		t.Fatalf("detail not rendered: %s", detail)
	}
	seasons, _ := request("GET", "/watch/seasons?id=webfixture%3Ademo", nil, nil, 200)
	if !strings.Contains(seasons, `"id":"webfixture:second","number":2`) {
		t.Fatalf("second season is missing: %s", seasons)
	}
	episodes, _ := request("GET", "/watch/episodes?id=webfixture%3Asecond", nil, nil, 200)
	if !strings.Contains(episodes, `"label":"第 2 季第 1 集"`) {
		t.Fatalf("second season's episodes are missing: %s", episodes)
	}
	request("POST", "/watch", url.Values{"id": {"webfixture:second"}, "episode": {"1"}}, nil, 200)
	playerSrc := regexp.MustCompile(`<video data-player src="([^"]+)" controls playsinline`)
	watch := func(episode string) string {
		t.Helper()
		player, _ := request("POST", "/watch", url.Values{"id": {"webfixture:demo"}, "episode": {episode}}, nil, 200)
		for _, control := range []string{"倍速", "画质", "字幕", "横屏", "选集"} {
			if !strings.Contains(player, ">"+control+"</button>") {
				t.Fatalf("player missing control %q", control)
			}
		}
		match := playerSrc.FindStringSubmatch(player)
		if len(match) != 2 || !strings.HasPrefix(match[1], "/media/") {
			t.Fatalf("player has no same-origin media: %s", player)
		}
		return match[1]
	}
	hls := watch("1")
	playlist, headers := request("GET", hls, nil, nil, 200)
	segment := regexp.MustCompile(`(?m)^/media/[^\n]+\.ts$`).FindString(playlist)
	if segment == "" || strings.Contains(playlist, "127.0.0.1") || headers.Get("Content-Type") != "application/vnd.apple.mpegurl" {
		t.Fatalf("invalid same-origin HLS response: %q", playlist)
	}
	request("GET", segment, nil, nil, 200)
	mp4 := watch("2")
	request("HEAD", mp4, nil, nil, 200)
	body, headers := request("GET", mp4, nil, map[string]string{"Range": "bytes=2-5"}, 206)
	if body != "2345" || headers.Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("incorrect Range response: %q %v", body, headers)
	}
	form := url.Values{"id": {"webfixture:demo"}, "episode": {"2"}, "quality": {"720"}}
	request("POST", "/watch/quality", form, map[string]string{"Sec-Fetch-Site": "cross-site"}, 403)
	lower, _ := request("POST", "/watch/quality", form, nil, 200)
	match := regexp.MustCompile(`"url":"(/media/[^"]+)".*"quality":720`).FindStringSubmatch(lower)
	if len(match) != 2 {
		t.Fatalf("720P playback not returned: %s", lower)
	}
	body, _ = request("GET", match[1], nil, map[string]string{"Range": "bytes=2-5"}, 206)
	if body != "cdef" {
		t.Fatalf("wrong variant: %q", body)
	}
	form.Set("quality", "360")
	request("POST", "/watch/quality", form, nil, 422)
	request("POST", "/logout", nil, nil, 303)
	request("GET", hls, nil, nil, 401)
}
