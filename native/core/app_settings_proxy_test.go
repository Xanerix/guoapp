package core

import (
	"context"
	"net/http"
	"testing"
)

func TestProxyRouterSourceOverride(t *testing.T) {
	settings := defaultResourceSettings()
	settings.ProxyMode = "direct"
	settings.SourceProxies = map[string]nativeSourceProxy{
		"overridden": {Mode: "manual", URL: "socks5://127.0.0.1:1080"},
	}
	if err := settings.validate(); err != nil {
		t.Fatal(err)
	}
	engine := &nativeEngine{downloader: &Downloader{proxyRouter: &proxyRouter{}, limiter: newRequestLimiter(3, 0)}}
	engine.applyResourceSettings(settings)
	resolve := func(source string) string {
		ctx := context.Background()
		if source != "" {
			ctx = withProxySource(ctx, source)
		}
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://media.example/a.m3u8", nil)
		proxy, err := engine.downloader.proxyRouter.proxy(request)
		if err != nil {
			t.Fatal(err)
		}
		if proxy == nil {
			return ""
		}
		return proxy.String()
	}
	if got := resolve("overridden"); got != "socks5://127.0.0.1:1080" {
		t.Fatalf("overridden source proxy = %q", got)
	}
	if got := resolve("other"); got != "" {
		t.Fatalf("other source proxy = %q", got)
	}
	if got := resolve(""); got != "" {
		t.Fatalf("unattributed proxy = %q", got)
	}
}

func TestSourceProxyValidation(t *testing.T) {
	for _, proxy := range []nativeSourceProxy{{Mode: "follow"}, {Mode: "manual"}, {Mode: "manual", URL: "ftp://127.0.0.1:21"}} {
		settings := defaultResourceSettings()
		settings.SourceProxies = map[string]nativeSourceProxy{"group": proxy}
		if settings.validate() == nil {
			t.Fatalf("accepted %+v", proxy)
		}
	}
}
