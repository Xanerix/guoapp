package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	soraniSessionFile  = "sorani-session.json"
	soraniCaptchaScene = "login"
	soraniRenewBefore  = 5 * time.Minute
)

var (
	errSoraniLoginRequired = errors.New("青空次元需要登录后才能播放，请在 设置 > 站源 > 站源账号 中登录青空账号")
	errSoraniLoginExpired  = errors.New("青空次元登录已失效，请在 设置 > 站源 > 站源账号 中重新登录")
	soraniCaptchaIDFormat  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

type soraniSession struct {
	Version      int    `json:"version"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	Name         string `json:"name"`
}

type soraniAccountStatus struct {
	LoggedIn bool   `json:"loggedIn"`
	Name     string `json:"name"`
}

type soraniAccountInput struct {
	Identifier string             `json:"identifier"`
	Password   string             `json:"password"`
	CaptchaID  string             `json:"captchaId"`
	Track      soraniCaptchaTrack `json:"track"`
}

type soraniCaptchaTrack struct {
	BgImageWidth        int                  `json:"bgImageWidth"`
	BgImageHeight       int                  `json:"bgImageHeight"`
	TemplateImageWidth  int                  `json:"templateImageWidth"`
	TemplateImageHeight int                  `json:"templateImageHeight"`
	StartTime           int64                `json:"startTime"`
	StopTime            int64                `json:"stopTime"`
	TrackList           []soraniCaptchaPoint `json:"trackList"`
}

type soraniCaptchaPoint struct {
	X    int    `json:"x"`
	Y    int    `json:"y"`
	Type string `json:"type"`
	T    int64  `json:"t"`
}

type soraniCaptcha struct {
	ID               string `json:"id"`
	BackgroundImage  string `json:"backgroundImage"`
	TemplateImage    string `json:"templateImage"`
	BackgroundWidth  int    `json:"backgroundWidth"`
	BackgroundHeight int    `json:"backgroundHeight"`
	TemplateWidth    int    `json:"templateWidth"`
	TemplateHeight   int    `json:"templateHeight"`
}

func (d *Downloader) soraniAccount(ctx context.Context, command string, input soraniAccountInput) (any, error) {
	client := d.soraniClient()
	switch command {
	case "":
		return client.accountStatus(), nil
	case "captcha":
		return client.captcha(ctx)
	case "login":
		return client.login(ctx, input)
	case "logout":
		return client.logout(ctx), nil
	}
	return nil, errors.New("不支持的应用操作")
}

func soraniTokenExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(token) > 4096 || len(parts) != 3 || parts[2] == "" {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

func (client *soraniAPIClient) sessionPath() string {
	return filepath.Join(client.downloader.cfg.dataDir, soraniSessionFile)
}

func (client *soraniAPIClient) loadSessionLocked() {
	if client.loaded {
		return
	}
	client.loaded = true
	file, err := os.Open(client.sessionPath())
	if err != nil {
		return
	}
	defer file.Close()
	var stored soraniSession
	if json.NewDecoder(io.LimitReader(file, 32*1024)).Decode(&stored) != nil || stored.Version != 1 ||
		soraniTokenExpiry(stored.AccessToken).IsZero() || !time.Now().Before(soraniTokenExpiry(stored.RefreshToken)) {
		return
	}
	client.session = stored
}

func (client *soraniAPIClient) saveSessionLocked(session soraniSession) error {
	client.session = session
	if session.RefreshToken == "" {
		if err := os.Remove(client.sessionPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(client.downloader.cfg.dataDir, 0o700); err != nil {
		return err
	}
	return nativeDownloadWrite(client.sessionPath(), data)
}

func (client *soraniAPIClient) accountStatus() soraniAccountStatus {
	client.accountMu.Lock()
	defer client.accountMu.Unlock()
	client.loadSessionLocked()
	return soraniAccountStatus{LoggedIn: client.session.RefreshToken != "", Name: client.session.Name}
}

func (client *soraniAPIClient) accessToken(ctx context.Context) (string, error) {
	client.accountMu.Lock()
	defer client.accountMu.Unlock()
	client.loadSessionLocked()
	if client.session.RefreshToken == "" {
		return "", nil
	}
	if time.Until(soraniTokenExpiry(client.session.AccessToken)) > soraniRenewBefore {
		return client.session.AccessToken, nil
	}
	return client.refreshLocked(ctx)
}

func (client *soraniAPIClient) renew(ctx context.Context, rejected string) (string, error) {
	client.accountMu.Lock()
	defer client.accountMu.Unlock()
	client.loadSessionLocked()
	if client.session.RefreshToken == "" {
		return "", errSoraniLoginExpired
	}
	if client.session.AccessToken != rejected {
		return client.session.AccessToken, nil
	}
	return client.refreshLocked(ctx)
}

func (client *soraniAPIClient) refreshLocked(ctx context.Context) (string, error) {
	value, err := client.call(ctx, http.MethodPost, "/auth/refresh", nil, nil, client.session.RefreshToken, 256<<10)
	var apiErr *soraniAPIError
	if errors.As(err, &apiErr) && (apiErr.code == "401" || apiErr.code == "403") {
		_ = client.saveSessionLocked(soraniSession{})
		return "", errSoraniLoginExpired
	}
	if err != nil {
		return "", err
	}
	session, err := soraniSessionFromLogin(value, client.session.Name)
	if err != nil {
		return "", err
	}
	_ = client.saveSessionLocked(session)
	return session.AccessToken, nil
}

func soraniSessionFromLogin(value any, name string) (soraniSession, error) {
	row, _ := soraniPayload(value).(map[string]any)
	access, refresh := mapString(row, "token"), mapString(row, "refreshToken")
	if soraniTokenExpiry(access).IsZero() || soraniTokenExpiry(refresh).IsZero() {
		return soraniSession{}, errors.New("青空次元未返回有效的登录凭证")
	}
	if user, ok := row["user"].(map[string]any); ok {
		if display := mapString(user, "displayName", "username"); display != "" {
			name = display
		}
	}
	return soraniSession{Version: 1, AccessToken: access, RefreshToken: refresh, Name: truncate(name, 64)}, nil
}

func (client *soraniAPIClient) captcha(ctx context.Context) (soraniCaptcha, error) {
	value, err := client.call(ctx, http.MethodPost, "/auth/behavior-captcha/generate",
		url.Values{"scene": {soraniCaptchaScene}}, map[string]any{}, "", 2<<20)
	if err != nil {
		return soraniCaptcha{}, err
	}
	row, _ := soraniPayload(value).(map[string]any)
	if kind := mapString(row, "type"); kind != "SLIDER" {
		return soraniCaptcha{}, fmt.Errorf("青空次元返回了暂不支持的验证方式（%s），请稍后重试", truncate(kind, 32))
	}
	size := func(key string) int {
		number, err := strconv.Atoi(nativeText(row[key]))
		if err != nil || number <= 0 || number > 4000 {
			return 0
		}
		return number
	}
	captcha := soraniCaptcha{
		ID:               mapString(row, "id"),
		BackgroundImage:  mapString(row, "backgroundImage"),
		TemplateImage:    mapString(row, "templateImage"),
		BackgroundWidth:  size("backgroundImageWidth"),
		BackgroundHeight: size("backgroundImageHeight"),
		TemplateWidth:    size("templateImageWidth"),
		TemplateHeight:   size("templateImageHeight"),
	}
	if !soraniCaptchaIDFormat.MatchString(captcha.ID) ||
		!strings.HasPrefix(captcha.BackgroundImage, "data:image/") || !strings.HasPrefix(captcha.TemplateImage, "data:image/") ||
		captcha.BackgroundWidth == 0 || captcha.BackgroundHeight == 0 || captcha.TemplateWidth == 0 || captcha.TemplateHeight == 0 {
		return soraniCaptcha{}, errors.New("青空次元验证码数据无效，请重试")
	}
	return captcha, nil
}

func (track soraniCaptchaTrack) valid() bool {
	if track.BgImageWidth <= 0 || track.BgImageWidth > 4000 || track.BgImageHeight <= 0 || track.BgImageHeight > 4000 ||
		track.TemplateImageWidth <= 0 || track.TemplateImageWidth > 4000 || track.TemplateImageHeight <= 0 || track.TemplateImageHeight > 4000 ||
		track.StartTime <= 0 || track.StopTime < track.StartTime || len(track.TrackList) < 2 || len(track.TrackList) > 5000 {
		return false
	}
	for _, point := range track.TrackList {
		if point.Type != "down" && point.Type != "move" && point.Type != "up" || point.T < 0 {
			return false
		}
	}
	return true
}

func (client *soraniAPIClient) login(ctx context.Context, input soraniAccountInput) (soraniAccountStatus, error) {
	identifier := strings.TrimSpace(input.Identifier)
	if identifier == "" || len(identifier) > 128 {
		return soraniAccountStatus{}, errors.New("请输入青空次元用户名或邮箱")
	}
	if input.Password == "" || len(input.Password) > 128 {
		return soraniAccountStatus{}, errors.New("请输入青空次元密码")
	}
	if !soraniCaptchaIDFormat.MatchString(input.CaptchaID) || !input.Track.valid() {
		return soraniAccountStatus{}, errors.New("安全验证数据无效，请重新拖动滑块")
	}
	verified, err := client.call(ctx, http.MethodPost, "/auth/behavior-captcha/verify",
		url.Values{"scene": {soraniCaptchaScene}}, map[string]any{"id": input.CaptchaID, "data": input.Track}, "", 64<<10)
	if err != nil {
		var apiErr *soraniAPIError
		if errors.As(err, &apiErr) {
			return soraniAccountStatus{}, errors.New("安全验证未通过，请重新拖动滑块")
		}
		return soraniAccountStatus{}, err
	}
	row, _ := soraniPayload(verified).(map[string]any)
	captchaToken := mapString(row, "token")
	if captchaToken == "" || len(captchaToken) > 4096 {
		return soraniAccountStatus{}, errors.New("安全验证未通过，请重新拖动滑块")
	}
	value, err := client.call(ctx, http.MethodPost, "/auth/login", nil,
		map[string]any{"identifier": identifier, "password": input.Password, "captchaToken": captchaToken}, "", 256<<10)
	if err != nil {
		return soraniAccountStatus{}, err
	}
	session, err := soraniSessionFromLogin(value, identifier)
	if err != nil {
		return soraniAccountStatus{}, err
	}
	client.accountMu.Lock()
	defer client.accountMu.Unlock()
	client.loaded = true
	if err := client.saveSessionLocked(session); err != nil {
		return soraniAccountStatus{}, errors.New("青空次元登录成功，但未能保存登录状态，请检查存储空间后重试")
	}
	return soraniAccountStatus{LoggedIn: true, Name: session.Name}, nil
}

func (client *soraniAPIClient) logout(ctx context.Context) soraniAccountStatus {
	client.accountMu.Lock()
	client.loadSessionLocked()
	access := client.session.AccessToken
	_ = client.saveSessionLocked(soraniSession{})
	client.accountMu.Unlock()
	if access != "" {
		_, _ = client.call(ctx, http.MethodPost, "/auth/logout", nil, nil, access, 64<<10)
	}
	return soraniAccountStatus{}
}
