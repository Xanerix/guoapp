package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"duanjuapp/native/core"
)

const legacyFrontendURL = "https://d2pypzndaqisk.cloudfront.net"

var apiFallbackHosts = []string{
	core.DefaultAPIBase,
	"https://d18ka9rqpfd3lo.cloudfront.net",
	"https://d37n0wjehmw08f.cloudfront.net",
}

func (p *legacyProvider) apiEndpoint(ctx context.Context) (string, error) {
	p.apiMu.Lock()
	defer p.apiMu.Unlock()
	if p.apiBase != "" {
		return p.apiBase, nil
	}
	configured := strings.TrimRight(core.FirstNonEmpty(p.cfg.APIBase, core.DefaultAPIBase), "/")
	parsed, err := url.Parse(configured)
	if err != nil || !core.IsProviderHTTPMediaURL(configured) {
		return "", errors.New("API 地址无效")
	}
	if !strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".cloudfront.net") {
		p.apiBase = configured
		return configured, nil
	}
	candidates := append([]string{configured}, apiFallbackHosts...)
	seen := map[string]bool{}
	var lastErr error
	for pass := 0; pass < 2; pass++ {
		if pass == 1 {
			candidates, err = p.discoverAPIHosts(ctx)
			if err != nil {
				lastErr = err
				break
			}
		}
		for _, candidate := range candidates {
			if seen[candidate] || time.Now().Before(p.apiFailures[candidate]) {
				continue
			}
			seen[candidate] = true
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if err := p.probeAPIHost(ctx, candidate); err != nil {
				lastErr = err
				continue
			}
			p.apiBase = candidate
			return candidate, nil
		}
	}
	return "", fmt.Errorf("黄果原 API 的候选域名均不可用: %w", core.PublicError(lastErr))
}

func (p *legacyProvider) resetAPIEndpoint(failed string) {
	p.apiMu.Lock()
	if p.apiFailures == nil {
		p.apiFailures = map[string]time.Time{}
	}
	p.apiFailures[failed] = time.Now().Add(time.Minute)
	if p.apiBase == failed {
		p.apiBase = ""
	}
	p.apiMu.Unlock()
}

func (p *legacyProvider) probeAPIHost(ctx context.Context, host string) error {
	response, err := p.host.Fetch(ctx, core.HTTPRequest{
		Label: "黄果", Method: http.MethodGet, URL: host + "/api/app/ping/check", Header: map[string]string{"User-Agent": core.UserAgent},
		Limit: 64 * 1024, Timeout: 4 * time.Second, FollowRedirects: true, AnyStatus: true,
	})
	if err != nil {
		return err
	}
	var envelope struct {
		Code int `json:"code"`
	}
	if response.Status != http.StatusOK || json.Unmarshal(response.Body, &envelope) != nil || envelope.Code != 200 {
		address, _ := url.Parse(host)
		return fmt.Errorf("%s API 健康检查失败 (HTTP %d)", address.Hostname(), response.Status)
	}
	return nil
}

func (p *legacyProvider) discoverAPIHosts(ctx context.Context) ([]string, error) {
	body, err := p.fetchLegacyFrontendScript(ctx)
	if err != nil {
		return nil, err
	}
	if hosts := parseAPIHosts(body); len(hosts) > 0 {
		return hosts, nil
	}
	return nil, errors.New("官网前端未提供可识别的 API 地址，请稍后重试或更新应用")
}

func (p *legacyProvider) fetchLegacyFrontendScript(ctx context.Context) (string, error) {
	page, err := p.text(ctx, legacyFrontendURL+"/", legacyFrontendURL+"/")
	if err != nil {
		return "", err
	}
	scripts := regexp.MustCompile(`(?i)<script\b[^>]*\bsrc=["']([^"']+)["']`).FindAllStringSubmatch(page, -1)
	for _, script := range scripts {
		if !strings.Contains(script[1], "/main-") || !strings.HasSuffix(script[1], ".js") {
			continue
		}
		assetURL := core.ResolveProviderURL(legacyFrontendURL+"/", script[1])
		asset, err := url.Parse(assetURL)
		if err != nil || asset.Hostname() != "d2pypzndaqisk.cloudfront.net" {
			continue
		}
		body, err := p.text(ctx, assetURL, legacyFrontendURL+"/")
		if err != nil {
			return "", err
		}
		return body, nil
	}
	return "", errors.New("黄果官网未提供可识别的接口脚本")
}

func parseAPIHosts(script string) []string {
	matches := regexp.MustCompile(`https://[a-z0-9]+\.cloudfront\.net`).FindAllString(script, -1)
	seen := map[string]bool{}
	var hosts []string
	for _, host := range matches {
		if host != legacyFrontendURL && !seen[host] {
			seen[host] = true
			hosts = append(hosts, host)
			if len(hosts) == 8 {
				break
			}
		}
	}
	return hosts
}
