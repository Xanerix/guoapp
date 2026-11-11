package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"duanjuapp/native/core"
)

const (
	jinguoSessionFile = "jinguo-session.json"
	jinguoXUserAgent  = "BuildID=com.hgaidj.ozThree;SysType=android;DevID=%s;Ver=1.0.8;DevType=Android;DeviceBrand=google;DeviceModel=Pixel;SystemName=Android;SystemVersion=14;Terminal=1;IsH5=1;Sid=00000000000000000000000000000"
)

var (
	jinguoAPIHosts       = []string{"https://d1wyuxwdrlykj1.cloudfront.net", "https://hghts.rlkfus.cn"}
	jinguoProtocol       = legacyProtocol{InterfaceKey: "65dc07d1b7915c6b2937432b091837a7", ParamKey: "BxJand%xf5h3sycH", ParamIV: "BxJand%xf5h3sycH"}
	jinguoDeviceIDFormat = regexp.MustCompile(`^[A-F0-9]{16}[0-9]{13}$`)
)

type jinguoAPIError struct{ code, message string }

func (err *jinguoAPIError) Error() string {
	return fmt.Sprintf("禁果接口返回错误 %s：%s", err.code, err.message)
}

type jinguoSession struct {
	Version  int    `json:"version"`
	DeviceID string `json:"deviceId"`
	Token    string `json:"token"`
}

type jinguoAuthCall struct {
	done  chan struct{}
	token string
	err   error
}

type jinguoAPIClient struct {
	host    core.Host
	mu      sync.Mutex
	loaded  bool
	base    string
	session jinguoSession
	pending *jinguoAuthCall
}

func (client *jinguoAPIClient) loadSession() {
	client.loaded = true
	client.session = jinguoSession{Version: 1}
	data, err := client.host.LoadState(jinguoSessionFile)
	if err != nil || len(data) == 0 || len(data) > 16*1024 {
		return
	}
	var stored jinguoSession
	if json.NewDecoder(bytes.NewReader(data)).Decode(&stored) != nil || stored.Version != 1 ||
		!jinguoDeviceIDFormat.MatchString(stored.DeviceID) || !validLegacyToken(stored.Token) {
		return
	}
	client.session = stored
}

func (client *jinguoAPIClient) saveSession(session jinguoSession) error {
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}
	return client.host.SaveState(jinguoSessionFile, data)
}

func (client *jinguoAPIClient) credentials(ctx context.Context) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "", "", err
	}
	client.mu.Lock()
	if !client.loaded {
		client.loadSession()
	}
	if client.session.DeviceID == "" {
		prefix := strings.ToUpper(randomHex(8))
		if prefix == "" {
			client.mu.Unlock()
			return "", "", errors.New("无法生成禁果访客标识")
		}
		client.session.DeviceID = prefix + strconv.FormatInt(time.Now().UnixMilli(), 10)
	}
	deviceID := client.session.DeviceID
	if token := client.session.Token; token != "" {
		client.mu.Unlock()
		return token, deviceID, nil
	}
	if pending := client.pending; pending != nil {
		client.mu.Unlock()
		select {
		case <-pending.done:
			if (errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded)) && ctx.Err() == nil {
				return client.credentials(ctx)
			}
			return pending.token, deviceID, pending.err
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
	pending := &jinguoAuthCall{done: make(chan struct{})}
	client.pending = pending
	client.mu.Unlock()

	token, err := client.login(ctx, deviceID)
	client.mu.Lock()
	if err == nil {
		client.session.Token = token
		if saveErr := client.saveSession(client.session); saveErr != nil {
			fmt.Println("禁果访客会话未能保存，本次会话仍可使用")
		}
	}
	pending.token, pending.err = token, err
	client.pending = nil
	close(pending.done)
	client.mu.Unlock()
	return token, deviceID, err
}

func (client *jinguoAPIClient) login(ctx context.Context, deviceID string) (string, error) {
	payload, err := client.request(ctx, http.MethodPost, "/api/app/mine/login/h5", map[string]any{
		"devID": deviceID, "sysType": "ios", "isAppStore": false,
	}, "", deviceID)
	if err != nil {
		return "", fmt.Errorf("禁果访客登录失败：%w", err)
	}
	var login struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(payload, &login) != nil || login.Token == "" || !validLegacyToken(login.Token) {
		return "", errors.New("禁果访客登录未返回有效令牌")
	}
	return login.Token, nil
}

func (client *jinguoAPIClient) invalidate(token string) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.session.Token == token {
		client.session.Token = ""
	}
}

func jinguoSessionExpired(err error) bool {
	var apiErr *jinguoAPIError
	var mediaErr *legacyAPIError
	return errors.As(err, &apiErr) && apiErr.code == "5005" || errors.As(err, &mediaErr) && mediaErr.code == "5005"
}

func (client *jinguoAPIClient) get(ctx context.Context, apiPath string, params any, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		token, deviceID, err := client.credentials(ctx)
		if err != nil {
			return err
		}
		payload, err := client.request(ctx, http.MethodGet, apiPath, params, token, deviceID)
		if attempt == 0 && jinguoSessionExpired(err) {
			client.invalidate(token)
			continue
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(payload, out); err != nil {
			return errors.New("禁果返回的数据格式无效")
		}
		return nil
	}
	return errors.New("禁果访客登录已失效，请稍后重试")
}

func (client *jinguoAPIClient) bases() []string {
	client.mu.Lock()
	pinned := client.base
	client.mu.Unlock()
	bases := []string{}
	if pinned != "" {
		bases = append(bases, pinned)
	}
	for _, base := range jinguoAPIHosts {
		if base != pinned {
			bases = append(bases, base)
		}
	}
	return bases
}

func (client *jinguoAPIClient) rememberBase(base string) {
	client.mu.Lock()
	client.base = base
	client.mu.Unlock()
}

type jinguoLineError struct{ err error }

func (err *jinguoLineError) Error() string { return err.err.Error() }
func (err *jinguoLineError) Unwrap() error { return err.err }

func (client *jinguoAPIClient) request(ctx context.Context, method, apiPath string, params any, token, deviceID string) ([]byte, error) {
	var query string
	var body []byte
	if params != nil {
		encrypted, err := encryptParam(params, []byte(jinguoProtocol.ParamKey), []byte(jinguoProtocol.ParamIV))
		if err != nil {
			return nil, err
		}
		if method == http.MethodGet {
			query = "?data=" + url.QueryEscape(encrypted)
		} else {
			body, _ = json.Marshal(map[string]string{"data": encrypted})
		}
	}
	var lastErr error
	for _, base := range client.bases() {
		payload, err := client.requestBase(ctx, base, method, apiPath+query, body, token, deviceID)
		var lineErr *jinguoLineError
		if !errors.As(err, &lineErr) {
			if err == nil {
				client.rememberBase(base)
			}
			return payload, err
		}
		lastErr = lineErr.err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}

func (client *jinguoAPIClient) requestBase(ctx context.Context, base, method, route string, body []byte, token, deviceID string) ([]byte, error) {
	header := map[string]string{
		"Accept":       "application/json",
		"temp":         "test",
		"X-User-Agent": fmt.Sprintf(jinguoXUserAgent, deviceID),
		"User-Agent":   core.UserAgent,
	}
	if token != "" {
		header["Authorization"] = token
	}
	if body != nil {
		header["Content-Type"] = "application/json"
	}
	response, err := client.host.Fetch(ctx, core.HTTPRequest{Label: "禁果", Method: method, URL: base + route, Header: header, Body: body, Limit: core.ProviderMaxBodyBytes, Timeout: core.ProviderTimeout, FollowRedirects: true})
	if err != nil {
		if response.Status == 0 || response.Status >= 500 || response.Blocked {
			return nil, &jinguoLineError{err}
		}
		return nil, err
	}
	data := response.Body
	var envelope apiEnvelope
	if json.Unmarshal(data, &envelope) != nil {
		return nil, errors.New("禁果返回的数据格式无效")
	}
	if code := strings.Trim(string(envelope.Code), `"`); code != "200" {
		message := core.FirstNonEmpty(envelope.Msg, "未知错误")
		if token != "" {
			message = strings.ReplaceAll(message, token, "[已隐藏]")
		}
		return nil, &jinguoAPIError{code: core.Truncate(code, 20), message: core.Truncate(message, 160)}
	}
	payload := envelope.Data
	if apiHashEnabled(envelope.Hash) && len(payload) > 0 {
		var encrypted string
		if json.Unmarshal(payload, &encrypted) != nil {
			return nil, errors.New("禁果返回的加密数据格式无效")
		}
		if payload, err = decryptResponse(encrypted, []byte(jinguoProtocol.InterfaceKey)); err != nil {
			return nil, errors.New("禁果返回的数据无法解密，接口可能已变更")
		}
	}
	if len(payload) == 0 {
		payload = []byte("null")
	}
	return payload, nil
}
