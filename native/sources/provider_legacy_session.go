package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"duanjuapp/native/core"
)

type legacyProtocol struct {
	InterfaceKey string `json:"interfaceKey"`
	ParamKey     string `json:"paramKey"`
	ParamIV      string `json:"paramIV"`
}

type legacySessionState struct {
	Version   int            `json:"version"`
	Scope     string         `json:"scope"`
	DeviceID  string         `json:"deviceId"`
	Token     string         `json:"token"`
	Protocol  legacyProtocol `json:"protocol"`
	UpdatedAt time.Time      `json:"updatedAt"`
}

type legacyAccess struct {
	legacyProtocol
	Token     string
	DeviceID  string
	Anonymous bool
}

type legacyAPIClient struct {
	mu      sync.Mutex
	loaded  bool
	state   legacySessionState
	access  *legacyAccess
	pending chan struct{}
	err     error
	retryAt time.Time
}

func (p *legacyProvider) legacyCredentials(ctx context.Context) (legacyAccess, error) {
	client := p.client
	for {
		if err := ctx.Err(); err != nil {
			return legacyAccess{}, err
		}
		client.mu.Lock()
		if client.access != nil {
			access := *client.access
			client.mu.Unlock()
			return access, nil
		}
		if pending := client.pending; pending != nil {
			client.mu.Unlock()
			select {
			case <-pending:
				continue
			case <-ctx.Done():
				return legacyAccess{}, ctx.Err()
			}
		}
		if client.err != nil && time.Now().Before(client.retryAt) {
			err := client.err
			client.mu.Unlock()
			return legacyAccess{}, err
		}
		if !client.loaded {
			client.state = p.loadLegacySession()
			client.loaded = true
		}
		state := client.state
		pending := make(chan struct{})
		client.pending = pending
		client.mu.Unlock()

		access, updated, err := p.bootstrapLegacySession(ctx, state)
		client.mu.Lock()
		client.state = updated
		client.err = err
		client.retryAt = time.Time{}
		if err == nil {
			client.access = &access
		} else if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			client.retryAt = time.Now().Add(time.Minute)
		}
		client.pending = nil
		close(pending)
		client.mu.Unlock()
		return access, err
	}
}

func (p *legacyProvider) legacySessionScope() string {
	return strings.TrimRight(core.FirstNonEmpty(p.cfg.APIBase, core.DefaultAPIBase), "/")
}

func (p *legacyProvider) loadLegacySession() legacySessionState {
	empty := legacySessionState{Version: 1, Scope: p.legacySessionScope()}
	data, err := p.host.LoadState(legacySessionFile)
	if err != nil || len(data) == 0 || len(data) > 16*1024 {
		return empty
	}
	var state legacySessionState
	if json.NewDecoder(bytes.NewReader(data)).Decode(&state) != nil || state.Version != 1 || state.Scope != empty.Scope ||
		!regexp.MustCompile(`^[A-Fa-f0-9]{16}[0-9]{13}$`).MatchString(state.DeviceID) || !validLegacyToken(state.Token) {
		return empty
	}
	return state
}

func validLegacyToken(token string) bool {
	return len(token) <= 4096 && !strings.ContainsAny(token, "\r\n\t ")
}

func (p *legacyProvider) saveLegacySession(state legacySessionState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return p.host.SaveState(legacySessionFile, data)
}

func (p *legacyProvider) bootstrapLegacySession(ctx context.Context, state legacySessionState) (legacyAccess, legacySessionState, error) {
	access := legacyAccess{Token: p.cfg.Token, Anonymous: p.cfg.Token == ""}
	protocol := state.Protocol
	needsProtocol := p.cfg.InterfaceKey == "" || p.cfg.ParamKey == "" || p.cfg.ParamIV == ""
	if needsProtocol && (!validLegacyProtocol(protocol) || time.Since(state.UpdatedAt) > 24*time.Hour) {
		script, err := p.fetchLegacyFrontendScript(ctx)
		if err != nil {
			return access, state, fmt.Errorf("获取黄果官网协议失败: %w", core.PublicError(err))
		}
		protocol, err = parseLegacyProtocol(script)
		if err != nil {
			return access, state, err
		}
		state.Protocol, state.UpdatedAt = protocol, time.Now()
	}
	access.legacyProtocol = legacyProtocol{
		InterfaceKey: core.FirstNonEmpty(p.cfg.InterfaceKey, protocol.InterfaceKey),
		ParamKey:     core.FirstNonEmpty(p.cfg.ParamKey, protocol.ParamKey),
		ParamIV:      core.FirstNonEmpty(p.cfg.ParamIV, protocol.ParamIV),
	}
	if !validLegacyProtocol(access.legacyProtocol) || !validLegacyToken(access.Token) {
		return access, state, errors.New("黄果 API 协议配置无效，请更新应用后重试")
	}
	if access.Anonymous {
		if state.DeviceID == "" {
			prefix := strings.ToUpper(randomHex(8))
			if prefix == "" {
				return access, state, errors.New("无法生成黄果访客标识")
			}
			state.DeviceID = prefix + strconv.FormatInt(time.Now().UnixMilli(), 10)
		}
		access.DeviceID, access.Token = state.DeviceID, state.Token
		if access.Token == "" {
			payload, err := p.legacyRequest(ctx, http.MethodPost, "/api/app/mine/login/h5", map[string]any{
				"devID": access.DeviceID, "sysType": "ios", "isAppStore": false,
			}, access)
			if err != nil {
				return access, state, fmt.Errorf("黄果访客登录失败: %w", err)
			}
			var login struct {
				Token string `json:"token"`
			}
			if json.Unmarshal(payload, &login) != nil || login.Token == "" || !validLegacyToken(login.Token) {
				return access, state, errors.New("黄果访客登录未返回有效令牌")
			}
			access.Token, state.Token = login.Token, login.Token
		}
	}
	if access.Anonymous {
		if err := p.saveLegacySession(state); err != nil {
			fmt.Println("黄果访客会话未能保存，本次会话仍可使用")
		}
	}
	return access, state, nil
}

func validLegacyProtocol(protocol legacyProtocol) bool {
	return len(protocol.InterfaceKey) > 0 && len(protocol.InterfaceKey) <= 512 && len(protocol.ParamKey) == 16 && len(protocol.ParamIV) == 16
}

func parseLegacyProtocol(script string) (legacyProtocol, error) {
	value := func(name string) string {
		pattern := `["']` + regexp.QuoteMeta(name) + `["']\s*[:,]\s*["']([^"'\\\r\n]+)["']`
		match := regexp.MustCompile(pattern).FindStringSubmatch(script)
		if len(match) == 2 {
			return match[1]
		}
		return ""
	}
	protocol := legacyProtocol{InterfaceKey: value("interfaceKey"), ParamKey: value("parameterKey"), ParamIV: value("parameterIv")}
	if !validLegacyProtocol(protocol) {
		return legacyProtocol{}, errors.New("黄果官网协议已变化，无法识别当前接口参数")
	}
	return protocol, nil
}

func (p *legacyProvider) invalidateLegacyToken(access legacyAccess) {
	client := p.client
	client.mu.Lock()
	defer client.mu.Unlock()
	if access.Anonymous && client.access != nil && client.access.Token == access.Token {
		client.state.Token = ""
		client.access = nil
		client.err = nil
	}
}

func (access legacyAccess) UserAgent() string {
	if access.DeviceID == "" {
		return core.XUserAgent
	}
	return strings.Replace(core.XUserAgent, "DevID=00000000000000000000000000000", "DevID="+access.DeviceID, 1)
}
