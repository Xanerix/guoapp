package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nativeResourceSettings struct {
	ProxyMode           string `json:"proxyMode"`
	ProxyURL            string `json:"proxyUrl"`
	CatalogConcurrency  int    `json:"catalogConcurrency"`
	DownloadConcurrency int    `json:"downloadConcurrency"`
	DownloadBySource    bool   `json:"downloadBySource"`
	Warning             string `json:"warning,omitempty"`
	SystemProxyStatus   string `json:"systemProxyStatus,omitempty"`

	SourceProxies map[string]nativeSourceProxy `json:"sourceProxies,omitempty"`
}

type nativeSourceProxy struct {
	Mode string `json:"mode"`
	URL  string `json:"url,omitempty"`
}

func defaultResourceSettings() nativeResourceSettings {
	return nativeResourceSettings{ProxyMode: "auto", CatalogConcurrency: 3, DownloadConcurrency: 2}
}

func (settings nativeResourceSettings) validate() error {
	if settings.CatalogConcurrency < 1 || settings.CatalogConcurrency > 6 ||
		settings.DownloadConcurrency < 1 || settings.DownloadConcurrency > 6 {
		return errors.New("并发应为 1 至 6")
	}
	if err := validateProxy(settings.ProxyMode, settings.ProxyURL); err != nil {
		return err
	}
	if len(settings.SourceProxies) > 200 {
		return errors.New("站源代理设置过多")
	}
	for group, proxy := range settings.SourceProxies {
		if group == "" || len(group) > 64 {
			return errors.New("站源代理设置无效")
		}
		if err := validateProxy(proxy.Mode, proxy.URL); err != nil {
			return err
		}
	}
	return nil
}

func validateProxy(mode, address string) error {
	if mode != "auto" && mode != "direct" && mode != "manual" {
		return errors.New("代理模式无效")
	}
	if mode == "manual" || address != "" {
		if len(address) > 2048 {
			return errors.New("代理地址过长")
		}
		parsed, err := url.Parse(address)
		if err != nil || parsed.Hostname() == "" || parsed.Fragment != "" || parsed.RawQuery != "" ||
			(parsed.Path != "" && parsed.Path != "/") ||
			(parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "socks5" && parsed.Scheme != "socks5h") {
			return errors.New("请输入有效的 HTTP、HTTPS 或 SOCKS5 代理地址")
		}
		if port := parsed.Port(); port != "" {
			number, err := strconv.Atoi(port)
			if err != nil || number < 1 || number > 65535 {
				return errors.New("代理端口无效")
			}
		}
	}
	return nil
}

type proxyRoute struct {
	mode   string
	manual *url.URL
}

type proxySourceKey struct{}

func withProxySource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, proxySourceKey{}, source)
}

func proxyGroup(source string) string {
	source = canonicalProviderSource(source)
	if descriptor, ok := registeredDescriptor(source); ok && descriptor.Group != "" {
		return descriptor.Group
	}
	return source
}

type proxyRouter struct {
	mu             sync.RWMutex
	mode           string
	manual         *url.URL
	sources        map[string]proxyRoute
	system         nativeSystemProxy
	systemRead     time.Time
	systemOverride bool
	record         func(diagnosticEvent)
	pacFailures    map[string]time.Time
}

func (router *proxyRouter) proxy(request *http.Request) (*url.URL, error) {
	if request.URL.Hostname() == "127.0.0.1" || request.URL.Hostname() == "localhost" || request.URL.Hostname() == "::1" {
		return nil, nil
	}
	group := ""
	if source, _ := request.Context().Value(proxySourceKey{}).(string); source != "" {
		group = proxyGroup(source)
	}
	router.mu.RLock()
	mode, manual := router.mode, router.manual
	if route, ok := router.sources[group]; ok {
		mode, manual = route.mode, route.manual
	}
	router.mu.RUnlock()
	switch mode {
	case "direct":
		return nil, nil
	case "manual":
		return manual, nil
	}
	return router.resolveSystem(request)
}

func (engine *nativeEngine) loadResourceSettings() {
	settings := defaultResourceSettings()
	file := filepath.Join(engine.directory, "resource-settings.json")
	info, err := os.Stat(file)
	if err == nil {
		var stored nativeResourceSettings
		if info.Mode().IsRegular() && info.Size() <= 1<<20 {
			if data, readErr := os.ReadFile(file); readErr == nil && json.Unmarshal(data, &stored) == nil && stored.validate() == nil {
				settings = stored
			} else {
				settings.Warning = "网络与下载设置无法读取，已使用默认值，请重新保存设置"
			}
		} else {
			settings.Warning = "网络与下载设置无效，已使用默认值，请重新保存设置"
		}
	} else if !os.IsNotExist(err) {
		settings.Warning = "网络与下载设置无法读取，已使用默认值，请重新保存设置"
	}
	engine.settings = settings
	engine.applyResourceSettings(settings)
}

func (engine *nativeEngine) resourceSettings() nativeResourceSettings {
	engine.settingsMu.Lock()
	defer engine.settingsMu.Unlock()
	if engine.settings.ProxyMode == "" {
		return defaultResourceSettings()
	}
	settings := engine.settings
	system := engine.downloader.proxyRouter.systemSettings()
	settings.SystemProxyStatus = "自动读取系统静态代理及环境变量"
	if system.PACURL != "" {
		settings.SystemProxyStatus = "系统配置了自动代理脚本，按脚本结果选择代理；脚本取不到结果时改用系统静态代理或环境变量"
	} else if system.PAC && system.HTTP == "" && system.HTTPS == "" {
		settings.SystemProxyStatus = "检测到系统自动代理脚本；请配置手动代理或选择直连"
	} else if system.HTTP != "" || system.HTTPS != "" {
		settings.SystemProxyStatus = "已读取系统静态代理"
	}
	return settings
}

func (engine *nativeEngine) applyResourceSettings(settings nativeResourceSettings) {
	router := engine.downloader.proxyRouter
	manual, _ := url.Parse(settings.ProxyURL)
	sources := make(map[string]proxyRoute, len(settings.SourceProxies))
	for group, proxy := range settings.SourceProxies {
		address, _ := url.Parse(proxy.URL)
		sources[group] = proxyRoute{mode: proxy.Mode, manual: address}
	}
	router.mu.Lock()
	router.mode, router.manual, router.sources = settings.ProxyMode, manual, sources
	router.mu.Unlock()
	limiter := engine.downloader.limiter
	limiter.mu.Lock()
	limiter.concurrency = settings.CatalogConcurrency
	limiter.notifyLocked()
	limiter.mu.Unlock()
	if engine.downloads != nil {
		engine.downloads.mu.Lock()
		engine.downloads.concurrency = settings.DownloadConcurrency
		engine.downloads.bySource = settings.DownloadBySource
		engine.downloads.scheduleLocked()
		engine.downloads.mu.Unlock()
	}
}

func (engine *nativeEngine) saveResourceSettings(settings nativeResourceSettings) (nativeResourceSettings, error) {
	settings.ProxyURL = strings.TrimSpace(settings.ProxyURL)
	for group, proxy := range settings.SourceProxies {
		proxy.URL = strings.TrimSpace(proxy.URL)
		settings.SourceProxies[group] = proxy
	}
	settings.Warning = ""
	settings.SystemProxyStatus = ""
	if err := settings.validate(); err != nil {
		return nativeResourceSettings{}, err
	}
	engine.settingsMu.Lock()
	defer engine.settingsMu.Unlock()
	data, err := json.Marshal(settings)
	if err == nil {
		err = nativeDownloadWrite(filepath.Join(engine.directory, "resource-settings.json"), data)
	}
	if err != nil {
		return nativeResourceSettings{}, errors.New("未能保存网络与下载设置，原设置保持不变")
	}
	engine.settings = settings
	engine.applyResourceSettings(settings)
	engine.downloader.client.CloseIdleConnections()
	return settings, nil
}
