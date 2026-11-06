package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func jinguoFixtureParams(t *testing.T, request *http.Request) map[string]any {
	t.Helper()
	encoded := request.URL.Query().Get("data")
	if request.Method == http.MethodPost {
		var body struct {
			Data string `json:"data"`
		}
		if json.NewDecoder(request.Body).Decode(&body) != nil {
			t.Error("encrypted body is missing")
		}
		encoded = body.Data
	}
	if encoded == "" {
		return nil
	}
	encrypted, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(encrypted)%16 != 0 {
		t.Errorf("invalid encrypted params: %q", encoded)
		return nil
	}
	block, _ := aes.NewCipher([]byte(jinguoProtocol.ParamKey))
	cipher.NewCBCDecrypter(block, []byte(jinguoProtocol.ParamIV)).CryptBlocks(encrypted, encrypted)
	plain, err := pkcs7Unpad(encrypted, 16)
	var params map[string]any
	if err != nil || json.Unmarshal(plain, &params) != nil {
		t.Errorf("params were not encrypted with the jinguo key")
	}
	return params
}

func TestJinguoCatalogSearchDetailAndSessionReuse(t *testing.T) {
	var logins atomic.Int32
	var searched string
	transport := sourceFixtureTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host != "d1wyuxwdrlykj1.cloudfront.net" {
			t.Errorf("request left the jinguo deployment: %s", request.URL)
		}
		params := jinguoFixtureParams(t, request)
		if request.URL.Path != "/api/app/mine/login/h5" && request.Header.Get("Authorization") != "jinguo-session" {
			t.Errorf("%s lost its session", request.URL.Path)
		}
		if !strings.Contains(request.Header.Get("X-User-Agent"), "BuildID=com.hgaidj.ozThree;") {
			t.Error("jinguo requests must identify as the jinguo app")
		}
		switch request.URL.Path {
		case "/api/app/mine/login/h5":
			logins.Add(1)
			device, _ := params["devID"].(string)
			if !jinguoDeviceIDFormat.MatchString(device) || !strings.Contains(request.Header.Get("X-User-Agent"), "DevID="+device+";") {
				t.Errorf("login identity is inconsistent: %v", params)
			}
			return legacyFixtureResponse(request, map[string]string{"token": "jinguo-session"}), nil
		case "/api/app/playlet-tab/all":
			return legacyFixtureResponse(request, []map[string]any{{"id": "6a928dfedb2cc815f75d7213", "name": "热播", "moduleType": 1}, {"id": "bad id", "name": "坏分类"}}), nil
		case "/api/app/playlet/home/tab/6a928dfedb2cc815f75d7213":
			if params["tabSortType"] != "1" || params["pageNumber"] != "1" {
				t.Errorf("tab params changed: %v", params)
			}
			return legacyFixtureResponse(request, map[string]any{"list": []map[string]any{
				{"id": "6aa409d8d7dd5abdc481cbb1", "title": "禁果样本", "summary": "简介", "cover": "cf230705/image/a/b.gif", "totalEpisode": 3, "tags": []any{"6a92a1b2c3d4e5f60718293a"}, "tagNames": []any{"都市"}},
				{"id": "6aa409d8d7dd5abdc481cbb1", "title": "重复条目"},
				{"id": "", "title": "坏条目"},
			}}), nil
		case "/api/app/playlet/keyword/search":
			searched, _ = params["keyword"].(string)
			return legacyFixtureResponse(request, map[string]any{"list": nil}), nil
		case "/api/app/playlet/detail/6aa409d8d7dd5abdc481cbb1":
			return legacyFixtureResponse(request, map[string]any{"id": "6aa409d8d7dd5abdc481cbb1", "title": "禁果样本", "cover": "cf230705/image/a/b.gif", "totalEpisode": 3, "chapters": []map[string]any{
				{"id": "c2", "currentEpisode": 2, "videoUrl": "", "canPlay": false, "accessType": "coin"},
				{"id": "c1", "currentEpisode": 1, "videoUrl": "JHA-213/tk1/abc.m3u8", "canPlay": true, "accessType": "free"},
			}}), nil
		default:
			t.Errorf("unexpected request: %s", request.URL.Path)
			return nil, errors.New("unexpected endpoint")
		}
	})
	d := sourceFixtureDownloader(t, transport)
	categories, err := d.fetchJinguoCategories(context.Background())
	if err != nil || len(categories) != 3 || categories[2].ID != "tab:6a928dfedb2cc815f75d7213" || !validJinguoCategory(categories[2].ID) || validJinguoCategory("tab:bad id") {
		t.Fatalf("categories: %+v %v", categories, err)
	}
	items, _, err := d.fetchJinguoCatalogPage(context.Background(), 1, "", "")
	if err != nil || len(items) != 1 {
		t.Fatalf("catalog: %+v %v", items, err)
	}
	drama := items[0]
	if drama.ID != "jinguo:6aa409d8d7dd5abdc481cbb1" || drama.Source != sourceJinguo || drama.CategoryName != "热播" || drama.Desc != "简介" ||
		drama.Cover != "https://zzzznnn.lkkwip.cn/cf230705/image/a/b.gif" || len(drama.Tags) != 1 || drama.Tags[0] != "都市" {
		t.Fatalf("drama parsed incorrectly: %+v", drama)
	}
	if items, more, err := d.fetchJinguoCatalogPage(context.Background(), 1, "", "  禁果 "); err != nil || len(items) != 0 || more || searched != "禁果" {
		t.Fatalf("search: %+v %t %v %q", items, more, err, searched)
	}
	_, chapters, err := d.fetchJinguoDetail(context.Background(), "6aa409d8d7dd5abdc481cbb1")
	if err != nil || len(chapters) != 2 || chapters[0].VIP || !chapters[1].VIP || chapters[0].Source != sourceJinguo || chapters[0].VideoURL != "JHA-213/tk1/abc.m3u8" {
		t.Fatalf("chapters: %+v %v", chapters, err)
	}
	if _, err := d.resolveJinguoMedia(context.Background(), Task{Chapter: chapters[1]}); err == nil || !strings.Contains(err.Error(), "解锁") {
		t.Fatalf("paid episode was not refused: %v", err)
	}
	restarted := sourceFixtureDownloader(t, transport)
	restarted.cfg.dataDir = d.cfg.dataDir
	if _, _, err := restarted.jinguoClient().credentials(context.Background()); err != nil || logins.Load() != 1 {
		t.Fatalf("restart did not reuse the guest session: logins=%d %v", logins.Load(), err)
	}
}

func TestJinguoExpiredSessionRefreshesPlaylistOnce(t *testing.T) {
	var logins atomic.Int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/api/app/mine/login/h5":
			return legacyFixtureResponse(request, map[string]string{"token": "fresh-" + string(rune('0'+logins.Add(1)))}), nil
		case "/api/app/vid/h5/m3u8/JHA-213/tk1/a b.m3u8":
			if request.URL.EscapedPath() != "/api/app/vid/h5/m3u8/JHA-213/tk1/a%20b.m3u8" {
				t.Errorf("playlist path escaped incorrectly: %s", request.URL.EscapedPath())
			}
			if request.URL.Query().Get("token") == "fresh-1" {
				return sourceFixtureResponse(request, 200, `{"code":5005,"msg":"no token"}`), nil
			}
			return sourceFixtureResponse(request, 200, "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"/api/app/vid/sec\"\n#EXTINF:5,\nhttps://cdn.example.test/0.ts\n#EXT-X-ENDLIST\n"), nil
		default:
			t.Errorf("unexpected request: %s", request.URL)
			return nil, errors.New("unexpected endpoint")
		}
	})
	media, err := d.resolveJinguoMedia(context.Background(), Task{Chapter: Chapter{Source: sourceJinguo, VideoURL: "/JHA-213/tk1/a b.m3u8"}})
	if err != nil || logins.Load() != 2 || !strings.Contains(media.Playlist, "/api/app/vid/sec") || media.HLSKey != nil {
		t.Fatalf("playlist: %+v logins=%d %v", media, logins.Load(), err)
	}
	for _, bad := range []string{"../x.m3u8", "https://evil.test/x.m3u8", "a/./b.m3u8", "a.m3u8?x=1"} {
		if _, ok := jinguoPlaylistPath(bad); ok {
			t.Fatalf("unsafe video path accepted: %s", bad)
		}
	}
	if canonicalProviderSource("jinguo") != sourceJinguo || !isHuangguoProviderSource(sourceJinguo) {
		t.Fatal("jinguo source is not registered")
	}
}

func TestJinguoFailsOverToBackupLineAndKeepsIt(t *testing.T) {
	var primaryCalls, backupCalls atomic.Int32
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "d1wyuxwdrlykj1.cloudfront.net":
			primaryCalls.Add(1)
			return sourceFixtureResponse(request, http.StatusBadGateway, "bad gateway"), nil
		case "hghts.rlkfus.cn":
			backupCalls.Add(1)
		default:
			t.Errorf("request left the jinguo deployment: %s", request.URL)
			return nil, errors.New("unexpected host")
		}
		switch request.URL.Path {
		case "/api/app/mine/login/h5":
			return legacyFixtureResponse(request, map[string]string{"token": "shared-session"}), nil
		case "/api/app/playlet-tab/all":
			return legacyFixtureResponse(request, []Tab{{ID: "6a928dfedb2cc815f75d7213", Name: "热播"}}), nil
		case "/api/app/playlet/detail/missing":
			return sourceFixtureResponse(request, 200, `{"code":51021,"msg":"not found"}`), nil
		default:
			t.Errorf("unexpected request: %s", request.URL.Path)
			return nil, errors.New("unexpected endpoint")
		}
	})
	if _, err := d.fetchJinguoCategories(context.Background()); err != nil {
		t.Fatal(err)
	}
	if primaryCalls.Load() != 1 || backupCalls.Load() != 2 {
		t.Fatalf("failover did not pin the backup line: primary=%d backup=%d", primaryCalls.Load(), backupCalls.Load())
	}
	var detail json.RawMessage
	err := d.jinguoClient().get(context.Background(), "/api/app/playlet/detail/missing", nil, &detail)
	var apiErr *jinguoAPIError
	if !errors.As(err, &apiErr) || apiErr.code != "51021" || primaryCalls.Load() != 1 || backupCalls.Load() != 3 {
		t.Fatalf("business error switched lines: %v primary=%d backup=%d", err, primaryCalls.Load(), backupCalls.Load())
	}
}
