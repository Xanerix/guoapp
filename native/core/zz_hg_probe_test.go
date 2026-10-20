package core

// Diagnostic probe for the 2026-10-20 hongguo playback failure.
// Reproduces hongguoAppRequest's query/headers/signing against the live API
// and dumps raw exchanges for A/B comparison. Not a regression test.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	probeSeriesID = "7692335909320018969"
	probeVideoID  = "7692338051632090136"
)

func probeRequest(t *testing.T, d *Downloader, installID, versionCode, versionName string,
	commentSign bool, path string, payload map[string]any) {
	t.Helper()
	client := d.hongguoClient()
	client.mu.Lock()
	deviceID := client.state.DeviceID
	client.mu.Unlock()

	query := url.Values{
		"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {versionCode}, "version_name": {versionName},
		"manifest_version_code": {versionCode}, "update_version_code": {versionCode}, "channel": {"update_64"},
		"device_platform": {"android"}, "os": {"android"}, "ssmix": {"a"}, "device_type": {"25053RT47C"},
		"device_brand": {"Redmi"}, "language": {"zh"}, "os_api": {"36"}, "os_version": {"16"},
		"resolution": {"1280*2772"}, "dpi": {"520"}, "ac": {"wifi"}, "device_id": {deviceID}, "iid": {installID},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		strings.TrimRight(hongguoAppBaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", hongguoAppUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-XS-From-Web", "0")
	req.Header.Set("Sdk-Version", "2")
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	now := time.Now()
	query.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
	req.URL.RawQuery = query.Encode()
	if commentSign {
		nonce, err := newHongguoCommentNonce()
		if err != nil {
			t.Fatal(err)
		}
		signHongguoCommentRequest(req, nonce, now)
	} else {
		signHongguoPlayerRequest(req, body, now, installID)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		t.Fatalf("transport: %v", err)
	}
	defer resp.Body.Close()
	content, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	dump := os.Getenv("HG_DUMP")
	if dump != "" {
		name := strings.NewReplacer("/", "_", " ", "_").Replace(path) + "_" + signName(commentSign) + "_" + t.Name() + ".json"
		os.WriteFile("/tmp/hg_dumps/"+name, content, 0644)
	}
	t.Logf("path=%s sign=%s status=%d bodyLen>8k=%v", path, signName(commentSign), resp.StatusCode, len(content) == 8192)
	for _, key := range []string{"X-Tt-Logid", "X-Argus-Desc", "X-Ladon-Desc", "Content-Type", "Server"} {
		if v := resp.Header.Get(key); v != "" {
			t.Logf("  resp[%s]=%s", key, v)
		}
	}
	preview := string(content)
	if len(preview) > 900 {
		preview = preview[:900]
	}
	t.Logf("  body: %s", strings.ReplaceAll(preview, "\n", " "))
}

func signName(comment bool) string {
	if comment {
		return "comment"
	}
	return "player"
}

func TestProbeHongguoPlayback(t *testing.T) {
	if os.Getenv("HG_PROBE") == "" {
		t.Skip("set HG_PROBE=1 to hit the live hongguo API")
	}
	engine, err := newNativeEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	d := engine.downloader
	client := d.hongguoClient()
	client.mu.Lock()
	installID := client.state.InstallID
	client.mu.Unlock()
	t.Logf("deviceID=%s installID=%s", client.state.DeviceID, installID)

	videoPayload := map[string]any{
		"video_id": probeVideoID, "content_type": 1,
		"biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 3},
	}

	t.Run("known_good_video_detail_comment", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true,
			"/novel/player/video_detail/v1/", map[string]any{"series_id": probeSeriesID})
	})
	t.Run("video_model_comment_baseline", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true, "/novel/player/video_model/v1/", videoPayload)
	})
	t.Run("video_model_player_sign", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", false, "/novel/player/video_model/v1/", videoPayload)
	})
	t.Run("video_model_comment_bumped_version", func(t *testing.T) {
		probeRequest(t, d, installID, "74202", "7.4.2.02", true, "/novel/player/video_model/v1/", videoPayload)
	})

	t.Run("login_check_read_progress", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true,
			"/reading/bookapi/read_progress/recent/v/", nil)
	})

	t.Run("video_model_content_type_1004", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true, "/novel/player/video_model/v1/", map[string]any{
			"video_id": probeVideoID, "content_type": 1004,
			"biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 3},
		})
	})
	t.Run("multi_video_model", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true, "/novel/player/multi_video_model/v1/", map[string]any{
			"video_ids": []string{probeVideoID}, "content_type": 1,
			"biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 3},
		})
	})

	t.Run("ncp_empty", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true, "/novel/player/video_model/v1/", map[string]any{
			"video_id": probeVideoID, "content_type": 1,
			"biz_param":          map[string]any{"need_all_video_definition": true, "video_platform": 3},
			"novel_common_param": map[string]any{},
		})
	})
	t.Run("ncp_minimal", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true, "/novel/player/video_model/v1/", map[string]any{
			"video_id": probeVideoID, "content_type": 1,
			"biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 3},
			"novel_common_param": map[string]any{
				"region": "CN", "current_region": "CN", "language": "zh", "app_language": "zh",
				"app_region": "CN", "carrier_region": "CN", "carrier_region_v2": "46000",
				"mcc_mnc": "46000", "device_dpi": 520, "is_from_web": false,
			},
		})
	})
	t.Run("mixed_map", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true, "/novel/player/video_model/v1/", map[string]any{
			"video_id": probeVideoID, "content_type": 1,
			"mixed_video_id_map": map[string]any{"1": []string{probeVideoID}},
			"biz_param":          map[string]any{"need_all_video_definition": true, "video_platform": 3},
		})
	})
	t.Run("biz_full", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true, "/novel/player/video_model/v1/", map[string]any{
			"video_id": probeVideoID, "content_type": 1,
			"biz_param": map[string]any{
				"need_all_video_definition": true, "video_platform": 3, "caller_scene": "player",
				"device_level": 3, "video_id_type": 0, "source": 4, "screen_width_px": "1280",
				"expire_strategy": "cache_first", "use_server_dns_scene": "video_model",
			},
		})
	})
	t.Run("content_type_1003_album", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true, "/novel/player/video_model/v1/", map[string]any{
			"video_id": probeVideoID, "content_type": 1003,
			"biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 3},
		})
	})

	t.Run("multi_preload_baseline", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true, "/novel/player/multi_video_model/preload/v1/", map[string]any{
			"video_ids": []string{probeVideoID}, "content_type": 1,
			"biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 3},
		})
	})
	t.Run("multi_preload_single_shape", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", true, "/novel/player/multi_video_model/preload/v1/", map[string]any{
			"video_id": probeVideoID, "content_type": 1,
			"biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 3},
		})
	})
	t.Run("multi_preload_player_sign", func(t *testing.T) {
		probeRequest(t, d, installID, "73532", "7.3.5.32", false, "/novel/player/multi_video_model/preload/v1/", map[string]any{
			"video_ids": []string{probeVideoID}, "content_type": 1,
			"biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 3},
		})
	})

	t.Run("preload_platform_sweep", func(t *testing.T) {
		for _, platform := range []int{0, 1, 2, 4, 5, 1024} {
			probeRequest(t, d, installID, "73532", "7.3.5.32", false, "/novel/player/multi_video_model/preload/v1/", map[string]any{
				"video_ids": []string{probeVideoID}, "content_type": 1,
				"biz_param": map[string]any{"need_all_video_definition": true, "video_platform": platform},
			})
		}
	})
	t.Run("preload_single_vs_list", func(t *testing.T) {
		for _, body := range []map[string]any{
			{"video_id": probeVideoID, "content_type": 1, "biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 1}},
			{"video_ids": []string{probeVideoID}, "content_type": 1, "biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 1}},
			{"video_ids": probeVideoID, "content_type": 1, "biz_param": map[string]any{"need_all_video_definition": true, "video_platform": 1}},
		} {
			probeRequest(t, d, installID, "73532", "7.3.5.32", false, "/novel/player/multi_video_model/preload/v1/", body)
		}
	})

	t.Run("preload_mixed_map_1024", func(t *testing.T) {
		for _, key := range []string{"0", "1", "2", "3"} {
			probeRequest(t, d, installID, "73532", "7.3.5.32", false, "/novel/player/multi_video_model/preload/v1/", map[string]any{
				"mixed_video_id_map": map[string]any{key: []string{probeVideoID}},
				"content_type":       1,
				"dr_scene":           "preload",
				"biz_param":          map[string]any{"need_all_video_definition": true, "video_platform": 1024},
			})
		}
	})

	t.Run("preload_resolve_via_core", func(t *testing.T) {
		media, err := d.resolveHongguoAppMedia(context.Background(), probeVideoID)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		parsed, _ := url.Parse(media.URL)
		t.Logf("resolved url host=%s dur=%v variants=%d", parsed.Host, media.Duration, len(media.Variants))
		if len(media.URL) > 100 {
			t.Logf("FULLURL %s", media.URL)
		}
	})
	t.Run("video_model_player_sign_bumped_version", func(t *testing.T) {
		probeRequest(t, d, installID, "74202", "7.4.2.02", false, "/novel/player/video_model/v1/", videoPayload)
	})
}

var _ = fmt.Sprint
