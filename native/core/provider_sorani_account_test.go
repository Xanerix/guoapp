package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func soraniFixtureToken(name string, expires time.Time) string {
	claims, _ := json.Marshal(map[string]any{"jti": name, "exp": expires.Unix()})
	return "eyJhbGciOiJIUzI1NiJ9." + base64.RawURLEncoding.EncodeToString(claims) + ".sig"
}

type soraniFixtureServer struct {
	mu          sync.Mutex
	access      map[string]bool
	refresh     string
	accessLife  time.Duration
	issued      int
	refreshes   int
	logouts     []string
	loginBodies []map[string]any
}

func (server *soraniFixtureServer) issue() map[string]any {
	server.issued++
	access := soraniFixtureToken(fmt.Sprintf("access-%d", server.issued), time.Now().Add(server.accessLife))
	server.refresh = soraniFixtureToken(fmt.Sprintf("refresh-%d", server.issued), time.Now().Add(7*24*time.Hour))
	server.access = map[string]bool{access: true}
	return map[string]any{"token": access, "refreshToken": server.refresh, "expireTime": "2026-11-07T12:00:00",
		"user": map[string]any{"username": "123521", "displayName": "友人"}}
}

func (server *soraniFixtureServer) transport(t *testing.T) sourceFixtureTransport {
	return func(request *http.Request) (*http.Response, error) {
		server.mu.Lock()
		defer server.mu.Unlock()
		reply := func(code int, data any, message string) (*http.Response, error) {
			body, _ := json.Marshal(map[string]any{"code": code, "message": message, "data": data, "success": code == 200})
			return sourceFixtureResponse(request, http.StatusOK, string(body)), nil
		}
		bearer := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if request.URL.Host == "www.sorani-vids.xyz" {
			return sourceFixtureResponse(request, http.StatusOK, "#EXTM3U\n#EXTINF:3.0,\na.ts\n#EXT-X-ENDLIST\n"), nil
		}
		if request.URL.Host != "api.sorani.cc" {
			t.Errorf("request left sorani: %s", request.URL)
			return nil, errors.New("unexpected host")
		}
		var body map[string]any
		if request.Body != nil {
			_ = json.NewDecoder(request.Body).Decode(&body)
		}
		switch strings.TrimPrefix(request.URL.Path, "/sorani-cms/api") {
		case "/auth/behavior-captcha/generate":
			if request.Method != http.MethodPost || request.URL.Query().Get("scene") != "login" {
				t.Errorf("captcha request changed: %s %s", request.Method, request.URL)
			}
			return reply(200, map[string]any{"id": "SLIDER_abc", "type": "SLIDER",
				"backgroundImage": "data:image/jpeg;base64,AA==", "templateImage": "data:image/png;base64,AA==",
				"backgroundImageWidth": 600, "backgroundImageHeight": 360, "templateImageWidth": 110, "templateImageHeight": 360}, "")
		case "/auth/behavior-captcha/verify":
			data, _ := body["data"].(map[string]any)
			if body["id"] != "SLIDER_abc" || data["bgImageWidth"] != float64(300) || len(data["trackList"].([]any)) != 3 {
				return reply(4001, nil, "验证码错误或已过期")
			}
			return reply(200, map[string]any{"token": "captcha-ok"}, "")
		case "/auth/login":
			server.loginBodies = append(server.loginBodies, body)
			if body["captchaToken"] != "captcha-ok" || body["password"] != "secret" {
				return reply(500, nil, "用户名或密码错误")
			}
			return reply(200, server.issue(), "")
		case "/auth/refresh":
			server.refreshes++
			if bearer == "" || bearer != server.refresh {
				server.refresh, server.access = "", nil
				return reply(401, nil, "令牌无效或已过期")
			}
			return reply(200, server.issue(), "")
		case "/auth/logout":
			server.logouts = append(server.logouts, bearer)
			return reply(200, nil, "")
		case "/video/episode/64334/play":
			if request.URL.Query().Get("lineCode") != soraniLineCodeDefault {
				t.Errorf("line code changed: %s", request.URL)
			}
			if !server.access[bearer] {
				return reply(200, map[string]any{"episodeId": 64334, "canPlay": false, "message": "请先登录", "playUrl": nil}, "")
			}
			return reply(200, map[string]any{"episodeId": 64334, "canPlay": true, "playUrl": "https://www.sorani-vids.xyz/v/index.m3u8?key=1"}, "")
		}
		t.Errorf("unexpected request: %s", request.URL.Path)
		return nil, errors.New("unexpected endpoint")
	}
}

func soraniFixtureLogin(password string) soraniAccountInput {
	return soraniAccountInput{Identifier: " 123521 ", Password: password, CaptchaID: "SLIDER_abc", Track: soraniCaptchaTrack{
		BgImageWidth: 300, BgImageHeight: 180, TemplateImageWidth: 55, TemplateImageHeight: 180,
		StartTime: 1000, StopTime: 1800, TrackList: []soraniCaptchaPoint{{0, 0, "down", 0}, {80, 2, "move", 400}, {120, 3, "up", 800}},
	}}
}

func TestSoraniAccountLoginRenewAndExpiry(t *testing.T) {
	server := &soraniFixtureServer{accessLife: time.Hour}
	d := sourceFixtureDownloader(t, server.transport(t))
	ctx := context.Background()
	task := Task{DramaID: providerDramaID(sourceSorani, "4647"), Chapter: Chapter{ID: providerChapterID(sourceSorani, "4647", "64334")}}

	if _, err := d.resolveSoraniMedia(ctx, task); !errors.Is(err, errSoraniLoginRequired) {
		t.Fatalf("anonymous playback should ask for login: %v", err)
	}
	captcha, err := d.soraniAccount(ctx, "captcha", soraniAccountInput{})
	if value, _ := captcha.(soraniCaptcha); err != nil || value.ID != "SLIDER_abc" || value.BackgroundWidth != 600 || value.TemplateHeight != 360 {
		t.Fatalf("captcha: %+v %v", captcha, err)
	}
	if _, err := d.soraniAccount(ctx, "login", soraniFixtureLogin("wrong")); err == nil || !strings.Contains(err.Error(), "用户名或密码错误") {
		t.Fatalf("bad password was not reported: %v", err)
	}
	status, err := d.soraniAccount(ctx, "login", soraniFixtureLogin("secret"))
	if err != nil || status != (soraniAccountStatus{LoggedIn: true, Name: "友人"}) || server.loginBodies[1]["identifier"] != "123521" {
		t.Fatalf("login: %+v %v %v", status, err, server.loginBodies)
	}
	media, err := d.resolveSoraniMedia(ctx, task)
	if err != nil || !strings.HasPrefix(media.Playlist, "#EXTM3U") || server.refreshes != 0 {
		t.Fatalf("logged-in playback: %+v %v refreshes=%d", media, err, server.refreshes)
	}

	server.mu.Lock()
	server.access, server.accessLife = nil, time.Minute
	server.mu.Unlock()
	if _, err := d.resolveSoraniMedia(ctx, task); err != nil || server.refreshes != 1 {
		t.Fatalf("revoked access token was not renewed once: %v refreshes=%d", err, server.refreshes)
	}

	restarted := sourceFixtureDownloader(t, server.transport(t))
	restarted.cfg.dataDir = d.cfg.dataDir
	if token, err := restarted.soraniClient().accessToken(ctx); err != nil || server.refreshes != 2 || !server.access[token] {
		t.Fatalf("restart did not renew the stored near-expiry token with the rotated refresh token: %v refreshes=%d", err, server.refreshes)
	}
	server.mu.Lock()
	server.accessLife = time.Hour
	server.mu.Unlock()
	if _, err := restarted.resolveSoraniMedia(ctx, task); err != nil || server.refreshes != 3 {
		t.Fatalf("restarted playback: %v refreshes=%d", err, server.refreshes)
	}

	server.mu.Lock()
	server.access, server.refresh = nil, ""
	server.mu.Unlock()
	if _, err := restarted.resolveSoraniMedia(ctx, task); !errors.Is(err, errSoraniLoginExpired) {
		t.Fatalf("revoked session should require a new login: %v", err)
	}
	if status := restarted.soraniClient().accountStatus(); status.LoggedIn {
		t.Fatalf("revoked session is still shown as logged in: %+v", status)
	}
	if _, err := os.Stat(filepath.Join(d.cfg.dataDir, soraniSessionFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revoked session file was kept: %v", err)
	}
}

func TestSoraniAccountLogoutAndCaptchaChecks(t *testing.T) {
	server := &soraniFixtureServer{accessLife: time.Hour}
	d := sourceFixtureDownloader(t, server.transport(t))
	ctx := context.Background()
	bad := soraniFixtureLogin("secret")
	bad.Track.TrackList = bad.Track.TrackList[:2]
	if _, err := d.soraniAccount(ctx, "login", bad); err == nil || !strings.Contains(err.Error(), "安全验证未通过") || len(server.loginBodies) != 0 {
		t.Fatalf("rejected captcha must not reach login: %v", err)
	}
	invalid := soraniFixtureLogin("secret")
	invalid.Track.TrackList[1].Type = "jump"
	if _, err := d.soraniAccount(ctx, "login", invalid); err == nil || !strings.Contains(err.Error(), "安全验证数据无效") {
		t.Fatalf("malformed track was forwarded: %v", err)
	}
	if _, err := d.soraniAccount(ctx, "login", soraniFixtureLogin("secret")); err != nil {
		t.Fatal(err)
	}
	server.mu.Lock()
	var access string
	for token := range server.access {
		access = token
	}
	server.mu.Unlock()
	status, err := d.soraniAccount(ctx, "logout", soraniAccountInput{})
	if err != nil || status != (soraniAccountStatus{}) || len(server.logouts) != 1 || server.logouts[0] != access {
		t.Fatalf("logout: %+v %v %v", status, err, server.logouts)
	}
	if _, err := os.Stat(filepath.Join(d.cfg.dataDir, soraniSessionFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("logout kept the session file: %v", err)
	}
}
