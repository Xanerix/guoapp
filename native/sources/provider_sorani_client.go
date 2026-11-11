package sources

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"duanjuapp/native/core"
)

const (
	soraniSiteBaseURL = "https://www.sorani.net"
	soraniAPIBaseURL  = "https://api.sorani.cc/sorani-cms/api"
	soraniUserAgent   = "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
)

type soraniAPIClient struct {
	host      core.Host
	base      string
	site      string
	accountMu sync.Mutex
	loaded    bool
	session   soraniSession
}

type soraniAPIError struct{ code, message string }

func (err *soraniAPIError) Error() string { return "青空次元接口错误：" + err.message }

func (client *soraniAPIClient) get(ctx context.Context, route string, query url.Values, limit int64) (any, error) {
	return client.call(ctx, http.MethodGet, route, query, nil, "", limit)
}

func (client *soraniAPIClient) call(ctx context.Context, method, route string, query url.Values, body any, bearer string, limit int64) (any, error) {
	if limit <= 0 {
		limit = 4 << 20
	}
	base, err := url.Parse(client.base)
	if err != nil || !core.IsProviderHTTPMediaURL(client.base) || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("青空次元接口地址无效")
	}
	address := client.base + route
	if len(query) > 0 {
		address += "?" + query.Encode()
	}
	header := map[string]string{
		"User-Agent":      soraniUserAgent,
		"Accept":          "application/json, text/plain, */*",
		"Accept-Language": "zh-CN,zh;q=0.9",
		"Referer":         client.site + "/",
		"Origin":          client.site,
	}
	var payload []byte
	if body != nil {
		if payload, err = json.Marshal(body); err != nil {
			return nil, errors.New("无法创建青空次元请求")
		}
		header["Content-Type"] = "application/json"
	}
	if bearer != "" {
		header["Authorization"] = "Bearer " + bearer
	}
	response, err := client.host.Fetch(ctx, core.HTTPRequest{Label: "青空次元", Method: method, URL: address, Header: header, Body: payload, Limit: limit, Timeout: 15 * time.Second})
	if err != nil {
		return nil, err
	}
	data := response.Body
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, errors.New("青空次元返回的数据格式无效")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("青空次元返回的数据格式无效")
	}
	if row, ok := decoded.(map[string]any); ok {
		if code := core.NativeText(row["code"]); code != "" && code != "200" {
			message := core.MapString(row, "message", "msg")
			if message == "" {
				message = "上游返回错误码 " + code
			}
			return nil, &soraniAPIError{code: code, message: core.Truncate(message, 120)}
		}
	}
	return decoded, nil
}
