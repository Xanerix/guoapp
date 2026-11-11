package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"duanjuapp/native/core"
)

const hongguoAppBaseURL = "https://api5-normal-sinfonlineb.fqnovel.com"

const hongguoAppUserAgent = "com.phoenix.read/73532 (Linux; U; Android 16; zh_CN; 25053RT47C; Build/BP2A.250605.031.A3; Cronet/TTNetVersion:04657795 2026-01-23 QuicVersion:c67e9834 2025-09-08)"

type hongguoCatalogCursor struct {
	Offset        int       `json:"offset"`
	SessionID     string    `json:"sessionId,omitempty"`
	LastID        string    `json:"lastId,omitempty"`
	PageSignature string    `json:"pageSignature,omitempty"`
	Initialized   bool      `json:"initialized"`
	Exhausted     bool      `json:"exhausted"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type hongguoCatalogState struct {
	Version          int                             `json:"version"`
	DeviceID         string                          `json:"deviceId"`
	InstallID        string                          `json:"installId"`
	Registered       bool                            `json:"registered,omitempty"`
	HelperDeviceID   string                          `json:"helperDeviceId,omitempty"`
	HelperInstallID  string                          `json:"helperInstallId,omitempty"`
	HelperRegistered bool                            `json:"helperRegistered,omitempty"`
	Feeds            map[string]hongguoCatalogCursor `json:"feeds"`
}

type hongguoAppClient struct {
	mu                  sync.Mutex
	catalogMu           sync.Mutex
	deviceMu            sync.Mutex
	registerPausedUntil time.Time
	registerPauseErr    error
	baseURL             string
	state               hongguoCatalogState
	details             map[string]hongguoDetailEntry
	pending             map[string]*hongguoDetailCall
	searches            map[string]hongguoSearchEntry
	searchPending       map[string]*hongguoSearchCall
	searchIDs           map[string]string
	suggestions         map[string]hongguoSuggestionEntry
	suggestPending      map[string]*hongguoSuggestionCall
	danmaku             map[string]hongguoDanmakuCacheEntry
	danmakuPending      map[string]*hongguoDanmakuCall
	tagCursors          map[string]hongguoTagCursor
}

func newHongguoAppClient() *hongguoAppClient {
	return &hongguoAppClient{
		baseURL: hongguoAppBaseURL,
		state: hongguoCatalogState{Version: 1, DeviceID: newHongguoDeviceID(), InstallID: newHongguoDeviceID(),
			HelperDeviceID: newHongguoDeviceID(), HelperInstallID: newHongguoDeviceID(), Feeds: map[string]hongguoCatalogCursor{}},
		details: map[string]hongguoDetailEntry{}, pending: map[string]*hongguoDetailCall{},
		searches: map[string]hongguoSearchEntry{}, searchPending: map[string]*hongguoSearchCall{}, searchIDs: map[string]string{},
		suggestions: map[string]hongguoSuggestionEntry{}, suggestPending: map[string]*hongguoSuggestionCall{},
		tagCursors: map[string]hongguoTagCursor{},
	}
}

func cloneHongguoCatalogState(state *hongguoCatalogState) *hongguoCatalogState {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.Feeds = make(map[string]hongguoCatalogCursor, len(state.Feeds))
	for name, cursor := range state.Feeds {
		cloned.Feeds[name] = cursor
	}
	return &cloned
}

func (p *hongguoProvider) restoreHongguoCatalog(state *hongguoCatalogState) {
	if state == nil || state.Version != 1 || !hongguoNumericID.MatchString(state.DeviceID) || !hongguoNumericID.MatchString(state.InstallID) {
		return
	}
	cloned := cloneHongguoCatalogState(state)
	if !hongguoNumericID.MatchString(cloned.HelperDeviceID) || !hongguoNumericID.MatchString(cloned.HelperInstallID) {
		cloned.HelperDeviceID, cloned.HelperInstallID, cloned.HelperRegistered = newHongguoDeviceID(), newHongguoDeviceID(), false
	}
	for name, cursor := range cloned.Feeds {
		known := false
		for _, genre := range hongguoAppGenres {
			known = known || name == genre.key || name == "category:"+genre.key
		}
		if !known || !cursor.Initialized || cursor.Offset < 0 || cursor.Offset > 1_000_000 || len(cursor.SessionID) > 4096 || len(cursor.LastID) > 256 || len(cursor.PageSignature) > 64 {
			delete(cloned.Feeds, name)
		}
	}
	client := p.client
	client.catalogMu.Lock()
	defer client.catalogMu.Unlock()
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.state.Feeds) == 0 {
		client.state = *cloned
	}
}

func (p *hongguoProvider) hongguoCatalogSnapshot() *hongguoCatalogState {
	client := p.client
	client.mu.Lock()
	defer client.mu.Unlock()
	return cloneHongguoCatalogState(&client.state)
}

func hongguoCatalogHasMore(state *hongguoCatalogState) bool {
	for _, genre := range hongguoAppGenres {
		if state == nil || !state.Feeds[genre.key].Exhausted {
			return true
		}
	}
	return false
}

func hongguoAppQuery(deviceID, installID string) url.Values {
	return url.Values{
		"aid": {"8662"}, "app_name": {"novelread"}, "version_code": {"73532"}, "version_name": {"7.3.5.32"},
		"manifest_version_code": {"73532"}, "update_version_code": {"73532"}, "channel": {"update_64"},
		"device_platform": {"android"}, "os": {"android"}, "ssmix": {"a"}, "device_type": {"25053RT47C"},
		"device_brand": {"Redmi"}, "language": {"zh"}, "os_api": {"36"}, "os_version": {"16"},
		"resolution": {"1280*2772"}, "dpi": {"520"}, "ac": {"wifi"}, "device_id": {deviceID}, "iid": {installID},
	}
}

func (p *hongguoProvider) hongguoAppRequest(ctx context.Context, method, path string, extra url.Values, payload any) (map[string]any, error) {
	client := p.client
	comment, _ := ctx.Value(hongguoCommentKey{}).(bool)
	helper, _ := ctx.Value(hongguoHelperDeviceKey{}).(bool)
	signed := !comment && path != hongguoBookmallTabPath
	lane := ""
	var registerErr error
	if signed {
		registerErr = p.ensureHongguoDevice(ctx, helper, "")
	}
	client.mu.Lock()
	base := client.baseURL
	deviceID, installID, _ := client.deviceLocked(helper)
	client.mu.Unlock()
	if helper {
		if deviceID == "" || !signed {
			return nil, errors.New("红果辅助设备不可用")
		}
		lane = "hongguo-helper"
	}
	query := hongguoAppQuery(deviceID, installID)
	for key, values := range extra {
		query[key] = append([]string(nil), values...)
	}
	var body []byte
	var err error
	if payload != nil {
		body, err = json.Marshal(payload)
		if err != nil {
			return nil, err
		}
	}
	attempts := p.host.Retries()
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 3 {
		attempts = 3
	}
	var lastErr error
	rotations := 0
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(time.Duration(attempt) * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		address := strings.TrimRight(base, "/") + path
		var commentNonce hongguoCommentNonce
		if comment {
			commentNonce, err = newHongguoCommentNonce()
			if err != nil {
				return nil, errors.New("无法准备红果文字请求")
			}
		}
		ticket, err := p.host.Reserve(ctx, address, lane)
		if err != nil {
			lastErr = err
			var backoff *core.RequestBackoff
			if errors.As(err, &backoff) || ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		now := time.Now()
		query.Set("_rticket", strconv.FormatInt(now.UnixMilli(), 10))
		prepared, err := http.NewRequest(method, address+"?"+query.Encode(), nil)
		if err != nil {
			return nil, err
		}
		prepared.Header.Set("User-Agent", hongguoAppUserAgent)
		prepared.Header.Set("Accept", "application/json")
		prepared.Header.Set("X-XS-From-Web", "0")
		prepared.Header.Set("Sdk-Version", "2")
		if comment {
			prepared.Header.Set("Comment-Source", "601")
			prepared.Header.Set("Server-Channel", "1000")
			signHongguoCommentRequest(prepared, commentNonce, now)
		} else if signed {
			signHongguoPlayerRequest(prepared, body, now)
		}
		if payload != nil {
			prepared.Header.Set("Content-Type", "application/json; charset=utf-8")
		}
		header := make(map[string]string, len(prepared.Header))
		for name := range prepared.Header {
			header[name] = prepared.Header.Get(name)
		}
		response, err := p.host.Fetch(ctx, core.HTTPRequest{
			Label: "红果", Method: method, URL: prepared.URL.String(), Header: header, Body: body, Ticket: ticket,
			Limit: core.ProviderMaxBodyBytes, Timeout: 20 * time.Second, FollowRedirects: true, AnyStatus: true,
		})
		if response.Oversized {
			return nil, errors.New("红果 App 接口未返回有效数据")
		}
		if err != nil {
			lastErr = err
			var backoff *core.RequestBackoff
			if errors.As(err, &backoff) || ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		content := response.Body
		if response.Status != http.StatusOK {
			lastErr = fmt.Errorf("红果 App 接口 HTTP %d", response.Status)
			if response.Status >= 400 && response.Status < 500 {
				return nil, lastErr
			}
			continue
		}
		if len(content) == 0 {
			lastErr = errors.New("红果 App 接口未返回有效数据")
			if signed && rotations < hongguoDeviceRotations {
				if registerErr = p.ensureHongguoDevice(ctx, helper, deviceID); registerErr == nil {
					rotations++
					deviceID, installID, _ = client.device(helper)
					query.Set("device_id", deviceID)
					query.Set("iid", installID)
					attempt--
				}
			}
			if registerErr != nil {
				lastErr = fmt.Errorf("红果 App 接口未返回有效数据（设备注册失败：%v）", registerErr)
			}
			continue
		}
		var result map[string]any
		decoder := json.NewDecoder(bytes.NewReader(content))
		decoder.UseNumber()
		if err := decoder.Decode(&result); err != nil || result == nil {
			return nil, errors.New("红果 App 接口返回格式异常")
		}
		code := core.FirstNonEmpty(core.MapString(result, "code", "Code", "status_code"), core.MapString(nestedMap(result, "BaseResp"), "StatusCode"))
		if code != "" && code != "0" {
			return nil, fmt.Errorf("红果 App 接口暂不可用（%s）", core.Truncate(code, 20))
		}
		return result, nil
	}
	return nil, lastErr
}
