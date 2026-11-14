package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type moduleTestHost struct {
	Host
	requests []HTTPRequest
	state    map[string][]byte
}

func (host *moduleTestHost) Fetch(_ context.Context, request HTTPRequest) (HTTPResponse, error) {
	host.requests = append(host.requests, request)
	return HTTPResponse{Status: 200, Header: map[string][]string{"X-Test": {"yes"}}, Body: []byte("pong " + string(request.Body))}, nil
}

func (host *moduleTestHost) LoadState(name string) ([]byte, error) { return host.state[name], nil }

func (host *moduleTestHost) SaveState(name string, data []byte) error {
	host.state[name] = data
	return nil
}

func writeTestModule(t *testing.T, manifest, code string) string {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, moduleManifest), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, moduleEntry), []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func TestExampleModuleServesCatalog(t *testing.T) {
	module, err := readModule(filepath.Join("..", "module_example", "jsexample"))
	if err != nil {
		t.Fatal(err)
	}
	p := module.provider(&moduleTestHost{})
	ctx := context.Background()

	categories, err := p.Categories(ctx, false)
	if err != nil || len(categories) != 2 || categories[0].ID != "romance" {
		t.Fatalf("categories = %v, %v", categories, err)
	}
	dramas, more, err := p.CatalogPage(ctx, 1, "suspense")
	if err != nil || !more || len(dramas) != 12 || dramas[0].ID != "jsexample:suspense-1" || dramas[0].Source != "jsexample" {
		t.Fatalf("catalog page = %d items, more %v, first %+v, %v", len(dramas), more, dramas[:min(1, len(dramas))], err)
	}
	if _, _, err := p.CatalogPage(ctx, 1, "missing"); err == nil || !strings.Contains(err.Error(), "没有分类") {
		t.Fatalf("unknown category error = %v", err)
	}
	drama, chapters, err := p.Detail(ctx, "romance-3")
	if err != nil || drama.SourceID != "romance-3" || len(chapters) != 8 || chapters[7].ID != "jsexample:romance-3:8" || chapters[7].EpisodeString(0) != "8" {
		t.Fatalf("detail = %+v, %d chapters, %v", drama, len(chapters), err)
	}
	if _, err := p.ResolveMedia(ctx, Task{}); err == nil || !strings.Contains(err.Error(), "不能播放") {
		t.Fatalf("resolve media error = %v", err)
	}
	if !module.descriptor().ValidCategory("romance") || module.descriptor().ValidCategory("missing") {
		t.Fatal("validCategory did not follow the module")
	}
	searcher, ok := providerAs[Searcher](p)
	if !ok {
		t.Fatal("declared search capability is missing")
	}
	if found, _, err := searcher.Search(ctx, 1, "x"); err != nil || len(found) != 12 {
		t.Fatalf("search = %d items, %v", len(found), err)
	}
}

func TestModuleHostRestrictsFetchAndIsolatesState(t *testing.T) {
	directory := writeTestModule(t, `{"id":"hosttest","name":"宿主测试","runtime":"js","api":1,"hosts":["api.example.invalid","*.cdn.example.invalid"]}`, `
module.exports = {
  categories() {
    host.saveState("token", "abc");
    const response = host.fetch({ method: "POST", url: "https://api.example.invalid/ping", headers: { "X-A": "1" }, body: "ping" });
    return [{ id: host.loadState("token"), name: response.body + " " + response.status + " " + response.headers["X-Test"][0] }];
  },
  catalogPage(page, category) {
    host.fetch({ url: category });
    return { items: [], hasMore: false };
  },
};`)
	module, err := readModule(directory)
	if err != nil {
		t.Fatal(err)
	}
	host := &moduleTestHost{state: map[string][]byte{}}
	p := module.provider(host)
	ctx := context.Background()

	categories, err := p.Categories(ctx, false)
	if err != nil || len(categories) != 1 || categories[0].ID != "abc" || categories[0].Name != "pong ping 200 yes" {
		t.Fatalf("categories = %v, %v", categories, err)
	}
	if len(host.requests) != 1 || host.requests[0].Method != "POST" || host.requests[0].Header["X-A"] != "1" {
		t.Fatalf("requests = %+v", host.requests)
	}
	if string(host.state["module-hosttest-token"]) != "abc" {
		t.Fatalf("state keys = %v", host.state)
	}
	if _, _, err := p.CatalogPage(ctx, 1, "https://a.cdn.example.invalid/x"); err != nil {
		t.Fatalf("wildcard host rejected: %v", err)
	}
	for _, address := range []string{"https://evil.invalid/x", "https://cdn.example.invalid/x", "https://api.example.invalid.evil.invalid/x", "file:///etc/passwd"} {
		if _, _, err := p.CatalogPage(ctx, 1, address); err == nil {
			t.Fatalf("fetch to %s was allowed", address)
		}
	}
	if len(host.requests) != 2 {
		t.Fatalf("host saw %d requests, want 2", len(host.requests))
	}
	if _, ok := providerAs[Searcher](p); ok {
		t.Fatal("undeclared search capability is exposed")
	}
}

func TestModuleCallStopsWithContext(t *testing.T) {
	directory := writeTestModule(t, `{"id":"looptest","name":"循环测试","runtime":"js","api":1}`, `
module.exports = {
  categories(force) {
    if (force) for (;;) {}
    return [];
  },
  detail: async function () {},
};`)
	module, err := readModule(directory)
	if err != nil {
		t.Fatal(err)
	}
	p := module.provider(&moduleTestHost{})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := p.Categories(ctx, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("endless loop error = %v", err)
	}
	if _, err := p.Categories(context.Background(), false); err != nil {
		t.Fatalf("instance unusable after interrupt: %v", err)
	}
	if _, _, err := p.Detail(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "异步") {
		t.Fatalf("async function error = %v", err)
	}
	if _, _, err := p.CatalogPage(context.Background(), 1, ""); err == nil || !strings.Contains(err.Error(), "未导出") {
		t.Fatalf("missing export error = %v", err)
	}
}

func TestLoadModulesRegistersAndReplaces(t *testing.T) {
	data := t.TempDir()
	target := filepath.Join(data, moduleDirectory, "jsexample")
	if err := os.CopyFS(target, os.DirFS(filepath.Join("..", "module_example", "jsexample"))); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(data, moduleDirectory, "broken")
	if err := os.MkdirAll(broken, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, moduleManifest), []byte(`{"id":"other","name":"x","runtime":"js","api":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	downloader := &Downloader{cfg: Config{dataDir: data}}
	downloader.loadModules()
	t.Cleanup(func() {
		if err := os.RemoveAll(filepath.Join(data, moduleDirectory)); err != nil {
			t.Fatal(err)
		}
		downloader.loadModules()
	})
	if !nativeSourceAvailable("jsexample") {
		t.Fatal("loaded module is not available")
	}
	if registeredProviderSource("other") || registeredProviderSource("broken") {
		t.Fatal("broken module was registered")
	}
	first, ok := downloader.provider("jsexample")
	if !ok {
		t.Fatal("loaded module has no provider")
	}
	downloader.loadModules()
	second, ok := downloader.provider("jsexample")
	if !ok || second == first {
		t.Fatal("reload did not replace the provider")
	}
}
