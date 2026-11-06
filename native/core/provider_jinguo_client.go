package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
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
	downloader *Downloader
	mu         sync.Mutex
	loaded     bool
	base       string
	session    jinguoSession
	pending    *jinguoAuthCall
}

func (d *Downloader) jinguoClient() *jinguoAPIClient {
	d.jinguoOnce.Do(func() { d.jinguo = &jinguoAPIClient{downloader: d} })
	return d.jinguo
}

func (client *jinguoAPIClient) loadSession() {
	client.loaded = true
	client.session = jinguoSession{Version: 1}
	file, err := os.Open(filepath.Join(client.downloader.cfg.dataDir, jinguoSessionFile))
	if err != nil {
		return
	}
	defer file.Close()
	var stored jinguoSession
	if json.NewDecoder(io.LimitReader(file, 16*1024)).Decode(&stored) != nil || stored.Version != 1 ||
		!jinguoDeviceIDFormat.MatchString(stored.DeviceID) || !validLegacyToken(stored.Token) {
		return
	}
	client.session = stored
}

func (client *jinguoAPIClient) saveSession(session jinguoSession) error {
	directory := client.downloader.cfg.dataDir
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".jinguo-session-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := json.NewEncoder(file).Encode(session); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(directory, jinguoSessionFile))
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
	d := client.downloader
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, base+route, reader)
	if err != nil {
		return nil, errors.New("无法创建禁果请求")
	}
	if token != "" {
		request.Header.Set("Authorization", token)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("temp", "test")
	request.Header.Set("X-User-Agent", fmt.Sprintf(jinguoXUserAgent, deviceID))
	request.Header.Set("User-Agent", userAgent)

	response, err := d.doCatalogRequestWithTimeout(request, providerTimeout)
	if err != nil {
		return nil, &jinguoLineError{fmt.Errorf("禁果连接失败：%w", publicError(err))}
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	_ = response.Body.Close()
	if readErr != nil || len(data) > providerMaxBodyBytes {
		return nil, &jinguoLineError{errors.New("禁果返回的数据过大或读取失败")}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err := d.catalogResponseError(request, response, data)
		if response.StatusCode >= 500 || catalogResponseBlockReason(response, data) != "" {
			return nil, &jinguoLineError{err}
		}
		return nil, err
	}
	var envelope apiEnvelope
	if json.Unmarshal(data, &envelope) != nil {
		return nil, errors.New("禁果返回的数据格式无效")
	}
	if code := strings.Trim(string(envelope.Code), `"`); code != "200" {
		message := firstNonEmpty(envelope.Msg, "未知错误")
		if token != "" {
			message = strings.ReplaceAll(message, token, "[已隐藏]")
		}
		return nil, &jinguoAPIError{code: truncate(code, 20), message: truncate(message, 160)}
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
