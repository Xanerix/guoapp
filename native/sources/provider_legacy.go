package sources

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"duanjuapp/native/core"
)

type Tab struct {
	ID   string
	Name string
}

type legacyTabList []Tab

func (tabs *legacyTabList) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '[' {
		return json.Unmarshal(raw, (*[]Tab)(tabs))
	}
	var wrapped struct {
		List []Tab `json:"list"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return err
	}
	*tabs = wrapped.List
	return nil
}

type apiEnvelope struct {
	Code json.RawMessage `json:"code"`
	Msg  string          `json:"msg"`
	Hash json.RawMessage `json:"hash"`
	Data json.RawMessage `json:"data"`
}

func apiHashEnabled(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(strings.ToLower(s))
		return s != "" && s != "false" && s != "0" && s != "null"
	}
	return true
}

type listResponse struct {
	List []core.Drama `json:"list"`
}

type detailResponse struct {
	core.Drama
	Title    string         `json:"title"`
	Name     string         `json:"name"`
	Chapters []core.Chapter `json:"chapters"`
}

func (detail *detailResponse) UnmarshalJSON(body []byte) error {
	var basic struct {
		Title    string         `json:"title"`
		Name     string         `json:"name"`
		Chapters []core.Chapter `json:"chapters"`
	}
	if err := json.Unmarshal(body, &basic); err != nil {
		return err
	}
	var fields map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return err
	}
	detail.Title, detail.Name, detail.Chapters = basic.Title, basic.Name, basic.Chapters
	detail.Drama = core.Drama{
		ID: core.MapString(fields, "id"), Title: basic.Title, Name: basic.Name,
		Desc: core.MapString(fields, "desc", "description", "summary"), Intro: core.MapString(fields, "intro"),
		Cover: fields["cover"], CoverURL: fields["coverUrl"], CoverURLSnake: fields["cover_url"],
		Image: fields["image"], ImageURL: fields["imageUrl"], ImageURLSnake: fields["image_url"],
		Img: fields["img"], Pic: fields["pic"], Picture: fields["picture"], Poster: fields["poster"],
		Thumb: fields["thumb"], Thumbnail: fields["thumbnail"],
		TotalEpisode: fields["totalEpisode"], TotalEpisodeSnake: fields["total_episode"],
		EpisodeCount: fields["episodeCount"], EpisodeCountSnake: fields["episode_count"],
		ChapterCount: fields["chapterCount"], ChapterCountSnake: fields["chapter_count"],
		Total: fields["total"], Episodes: fields["episodes"],
		CategoryName: core.MapString(fields, "categoryName", "category_name", "typeName", "type_name", "sortName", "sort_name", "category"),
		Heat:         core.MapString(fields, "heat"), Views: core.MapString(fields, "views", "play_count"),
		OnlineDate: core.MapString(fields, "onlineDate", "online_date", "issue_date"),
		Tags:       core.MapStringSlice(fields, "tags"), ReleaseStatus: core.MapString(fields, "releaseStatus", "release_status"),
	}
	if value, known := fields["vip"].(bool); known {
		detail.Drama.VIP = &value
	}
	if detail.Drama.ReleaseStatus == "" {
		detail.Drama.ReleaseStatus = core.ReleaseStatusFromRemark(core.MapString(fields, "remark"))
	}
	return nil
}

func pkcs7Pad(src []byte, blockSize int) []byte {
	pad := blockSize - len(src)%blockSize
	return append(src, bytes.Repeat([]byte{byte(pad)}, pad)...)
}

func encryptParam(obj any, key, iv []byte) (string, error) {
	plain, err := json.Marshal(obj)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	padded := pkcs7Pad(plain, block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out), nil
}

func sha256Bytes(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

func decryptResponse(dataStr string, interfaceKey []byte) ([]byte, error) {
	o, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataStr))
	if err != nil {
		return nil, err
	}
	if len(o) < 12 {
		return nil, errors.New("encrypted payload too short")
	}
	i := o[:12]
	r := append(append([]byte{}, interfaceKey...), i...)
	l := len(r) >> 1
	s := sha256Bytes(r)[8:24]
	c := append(append([]byte{}, s...), r[:l]...)
	a := append(append([]byte{}, r[l:]...), s...)
	d := sha256Bytes(c)
	f := sha256Bytes(a)
	m := append(append(append([]byte{}, d[:8]...), f[8:24]...), d[24:]...)
	p := append(append(append([]byte{}, f[:4]...), d[12:20]...), f[28:]...)
	g := o[12:]
	block, err := aes.NewCipher(m)
	if err != nil {
		return nil, err
	}
	if len(g)%block.BlockSize() != 0 {
		return nil, errors.New("ciphertext is not block aligned")
	}
	plain := make([]byte, len(g))
	cipher.NewCBCDecrypter(block, p).CryptBlocks(plain, g)
	return core.Pkcs7Unpad(plain, block.BlockSize())
}

func (p *legacyProvider) fetchAPI(ctx context.Context, apiPath string, params any, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		access, err := p.legacyCredentials(ctx)
		if err != nil {
			return err
		}
		payload, err := p.legacyRequest(ctx, http.MethodGet, apiPath, params, access)
		var apiErr *legacyAPIError
		if attempt == 0 && access.Anonymous && errors.As(err, &apiErr) && apiErr.code == "5005" {
			p.invalidateLegacyToken(access)
			continue
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(payload, out)
	}
	return errors.New("黄果访客登录已失效，请稍后重试")
}

type legacyAPIError struct{ code, message string }

func (err *legacyAPIError) Error() string {
	return fmt.Sprintf("黄果 API 错误 %s: %s", err.code, err.message)
}

func legacyPlaylistError(err error) error {
	var rejected *core.PlaylistError
	var envelope apiEnvelope
	if errors.As(err, &rejected) && json.Unmarshal(rejected.Body, &envelope) == nil && strings.Trim(string(envelope.Code), `"`) == "5005" {
		return &legacyAPIError{code: "5005", message: "播放会话已失效"}
	}
	return err
}

func (p *legacyProvider) legacyRequest(ctx context.Context, method, apiPath string, params any, access legacyAccess) ([]byte, error) {
	var lastErr error
	attempts := p.cfg.Retries
	if attempts < 1 {
		attempts = 1
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			select {
			case <-time.After(time.Duration(attempt) * time.Second):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		fullPath := apiPath
		var requestBody []byte
		if params != nil {
			enc, err := encryptParam(params, []byte(access.ParamKey), []byte(access.ParamIV))
			if err != nil {
				return nil, err
			}
			if method == http.MethodGet {
				sep := "?"
				if strings.Contains(fullPath, "?") {
					sep = "&"
				}
				fullPath += sep + "data=" + url.QueryEscape(enc)
			} else {
				requestBody, _ = json.Marshal(map[string]string{"data": enc})
			}
		}
		base, err := p.apiEndpoint(ctx)
		if err != nil {
			return nil, err
		}
		header := map[string]string{
			"Accept":       "application/json",
			"temp":         "test",
			"X-User-Agent": access.UserAgent(),
			"Origin":       legacyFrontendURL,
			"Referer":      legacyFrontendURL + "/",
			"User-Agent":   core.UserAgent,
		}
		if access.Token != "" {
			header["Authorization"] = access.Token
		}
		if requestBody != nil {
			header["Content-Type"] = "application/json"
		}
		response, err := p.host.Fetch(ctx, core.HTTPRequest{Label: "黄果", Method: method, URL: base + fullPath, Header: header, Body: requestBody, Limit: core.ProviderMaxBodyBytes, Timeout: core.ProviderTimeout, FollowRedirects: true})
		if err != nil {
			lastErr = err
			switch {
			case response.Oversized:
				return nil, errors.New("API 响应过大")
			case response.Status == 0:
				var backoff *core.RequestBackoff
				if errors.As(err, &backoff) || ctx.Err() != nil {
					return nil, lastErr
				}
				p.resetAPIEndpoint(base)
			case response.Status >= 500:
				p.resetAPIEndpoint(base)
			case response.Status >= 400:
				return nil, lastErr
			}
			continue
		}
		body := response.Body
		var env apiEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			lastErr = err
			continue
		}
		if code := strings.Trim(string(env.Code), `"`); code != "" && code != "null" && code != "200" && code != "0" {
			message := core.FirstNonEmpty(env.Msg, "请检查访问配置或刷新登录凭证")
			if access.Token != "" {
				message = strings.ReplaceAll(message, access.Token, "[已隐藏]")
			}
			return nil, &legacyAPIError{code: core.Truncate(code, 20), message: core.Truncate(message, 160)}
		}
		payload := env.Data
		if apiHashEnabled(env.Hash) && len(env.Data) > 0 {
			var encrypted string
			if err := json.Unmarshal(env.Data, &encrypted); err != nil {
				lastErr = err
				continue
			}
			plain, err := decryptResponse(encrypted, []byte(access.InterfaceKey))
			if err != nil {
				lastErr = err
				continue
			}
			payload = plain
		}
		if len(payload) == 0 || string(payload) == "null" {
			return nil, errors.New("黄果 API 未返回数据，请检查登录凭证或接口是否变更")
		}
		return payload, nil
	}
	return nil, lastErr
}
