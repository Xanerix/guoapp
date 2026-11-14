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

func TestModuleAccountAndLoginError(t *testing.T) {
	directory := writeTestModule(t, `{"id":"accounttest","name":"账号测试","runtime":"js","api":1,"capabilities":["account"]}`, `
module.exports = {
  account(command, payload) {
    return host.exclusive(() => ({ command, name: payload ? payload.name : "" }));
  },
  detail() {
    const failure = new Error("需要登录");
    failure.loginRequired = true;
    throw failure;
  },
  categories() {
    return host.exclusive(() => { throw new Error("临界区出错"); });
  },
};`)
	module, err := readModule(directory)
	if err != nil {
		t.Fatal(err)
	}
	p := module.provider(&moduleTestHost{})
	account, ok := providerAs[AccountProvider](p)
	if !ok {
		t.Fatal("declared account capability is missing")
	}
	result, err := account.Account(context.Background(), "login", []byte(`{"name":"neko"}`))
	if row, _ := result.(map[string]any); err != nil || row["command"] != "login" || row["name"] != "neko" {
		t.Fatalf("account = %v, %v", result, err)
	}
	if result, err := account.Account(context.Background(), "", nil); err != nil || result.(map[string]any)["name"] != "" {
		t.Fatalf("account status = %v, %v", result, err)
	}
	if _, _, err := p.Detail(context.Background(), "x"); !errors.Is(err, ErrSourceLogin) || err.Error() != "需要登录" {
		t.Fatalf("login error = %v", err)
	}
	if _, err := p.Categories(context.Background(), false); err == nil || !strings.Contains(err.Error(), "临界区出错") {
		t.Fatalf("exclusive error = %v", err)
	}
	if _, err := account.Account(context.Background(), "again", nil); err != nil {
		t.Fatalf("exclusive lock was not released: %v", err)
	}
}

type moduleStatusHost struct{ moduleTestHost }

func (host *moduleStatusHost) Fetch(context.Context, HTTPRequest) (HTTPResponse, error) {
	return HTTPResponse{Status: 503, Body: []byte(`{"message":"忙"}`), Blocked: true}, errors.New("HTTP 503")
}

func TestModuleFetchErrorCarriesResponse(t *testing.T) {
	directory := writeTestModule(t, `{"id":"statustest","name":"状态测试","runtime":"js","api":1,"hosts":["api.example.invalid"]}`, `
module.exports = {
  categories() {
    try {
      host.fetch({ url: "https://api.example.invalid/x", acceptStatus: [401] });
    } catch (error) {
      return [{ id: String(error.status), name: error.body + " " + error.blocked + " " + error.message }];
    }
    return [];
  },
};`)
	module, err := readModule(directory)
	if err != nil {
		t.Fatal(err)
	}
	categories, err := module.provider(&moduleStatusHost{}).Categories(context.Background(), false)
	if err != nil || len(categories) != 1 || categories[0].ID != "503" || categories[0].Name != `{"message":"忙"} true HTTP 503` {
		t.Fatalf("categories = %v, %v", categories, err)
	}
}

func TestPreserveIntegers(t *testing.T) {
	input := `{"id":7701280094358096920,"small":42,"neg":-9007199254740993,"edge":9007199254740991,"float":1.5e3,"text":"12345678901234567890 \"7701280094358096920\"","list":[18446744073709551616,0]}`
	want := `{"id":"7701280094358096920","small":42,"neg":"-9007199254740993","edge":9007199254740991,"float":1.5e3,"text":"12345678901234567890 \"7701280094358096920\"","list":["18446744073709551616",0]}`
	if got := preserveIntegers(input); got != want {
		t.Fatalf("preserveIntegers =\n%s\nwant\n%s", got, want)
	}
}

func TestModuleHostUtilitiesAndOptionalCapabilities(t *testing.T) {
	directory := writeTestModule(t, `{"id":"utiltest","name":"工具测试","runtime":"js","api":1,"capabilities":["search","danmaku","rankings"],
"rankings":[{"id":"util-hot","name":"热榜","description":"说明"}]}`, `
module.exports = {
  categories(force) {
    return force ? { items: [{ id: "a", name: "A" }], incomplete: true } : [{ id: "b", name: "B" }];
  },
  catalogPage(page) {
    return { items: [{ id: host.dramaId("x" + page), title: "x" }], hasMore: false, warning: page > 1 ? "部分失败" : "" };
  },
  search(page, query) {
    const key = host.hexDecode("000102030405060708090a0b0c0d0e0f");
    const sealed = host.aesCbcEncrypt(key, key, host.utf8Encode("0123456789abcdef"));
    host.cacheSet("query", query, 0);
    const parsed = JSON.parse(host.preserveIntegers('{"id":7701280094358096920}'));
    return {
      items: [{ id: host.dramaId(parsed.id), title: [
        host.hexEncode(host.md5("abc")), host.hexEncode(host.sha256(host.utf8Encode("abc"))), host.base64Encode(sealed),
        host.utf8Decode(host.base64Decode("57qi5p6c")), host.cacheGet("query"), String(host.cacheGet("missing")), String(host.randomBytes(5).length),
        host.resolveUrl("https://a.example/x/y.html", "../z?q=1"),
      ].join("|") }],
      hasMore: true,
      warning: "注意",
    };
  },
  detail() {
    return host.exclusive(() => host.exclusive(() => ({})));
  },
  danmakuIds(task) {
    return task.chapter.videoUrl === "ok" ? { series: "s", video: "v" } : null;
  },
  danmaku(series, video, start, duration) {
    return { episodeId: video, items: [{ id: "1", text: series, timeMs: start }], startMs: start, nextMs: duration, total: 1 };
  },
  rankingPage(board, page) {
    return { items: [{ rank: page, drama: { id: host.dramaId(board), title: board } }], hasMore: false };
  },
};`)
	module, err := readModule(directory)
	if err != nil {
		t.Fatal(err)
	}
	p := module.provider(&moduleTestHost{state: map[string][]byte{}})
	ctx := context.Background()
	if categories, err := p.Categories(ctx, true); !errors.Is(err, ErrCategoriesIncomplete) || len(categories) != 1 || categories[0].ID != "a" {
		t.Fatalf("incomplete categories = %v, %v", categories, err)
	}
	if categories, err := p.Categories(ctx, false); err != nil || len(categories) != 1 || categories[0].ID != "b" {
		t.Fatalf("categories = %v, %v", categories, err)
	}
	if items, _, err := p.CatalogPage(ctx, 2, ""); err == nil || err.Error() != "部分失败" || len(items) != 1 {
		t.Fatalf("catalog warning = %v, %v", items, err)
	}
	searcher, ok := providerAs[WarningSearcher](p)
	if !ok {
		t.Fatal("search capability does not expose warnings")
	}
	items, more, warning, err := searcher.SearchWithWarning(ctx, 1, "红果")
	want := "900150983cd24fb0d6963f7d28e17f72|ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad|oYSMQruLXhpGrNlYzP7FTw==|红果|红果|null|5|https://a.example/z?q=1"
	if err != nil || !more || warning != "注意" || len(items) != 1 || items[0].Title != want || items[0].ID != "utiltest:7701280094358096920" {
		t.Fatalf("search = %+v, %v, %q, %v", items, more, warning, err)
	}
	if _, _, err := p.Detail(ctx, "x"); err == nil || !strings.Contains(err.Error(), "不可重入") {
		t.Fatalf("nested exclusive error = %v", err)
	}
	danmaku, ok := providerAs[DanmakuProvider](p)
	if !ok {
		t.Fatal("declared danmaku capability is missing")
	}
	if _, _, valid := danmaku.DanmakuIDs(Task{Chapter: Chapter{VideoURL: "no"}}); valid {
		t.Fatal("danmaku ids accepted a rejected task")
	}
	series, video, valid := danmaku.DanmakuIDs(Task{Chapter: Chapter{VideoURL: "ok"}})
	if page, err := danmaku.Danmaku(ctx, series, video, 5, 9); !valid || err != nil || page.Items[0].Text != "s" || page.NextMS != 9 {
		t.Fatalf("danmaku = %+v, %v", page, err)
	}
	boards := module.descriptor().Rankings
	if len(boards) != 1 || boards[0].Source != "utiltest" || boards[0].Name != "热榜" {
		t.Fatalf("boards = %+v", boards)
	}
	ranker, _ := providerAs[Ranker](p)
	if page, err := ranker.RankingPage(ctx, boards[0], 3); err != nil || page.Items[0].Rank != 3 || page.Items[0].Drama.Title != "util-hot" {
		t.Fatalf("ranking = %+v, %v", page, err)
	}
	if _, ok := providerAs[Suggester](p); ok {
		t.Fatal("undeclared suggestions capability is exposed")
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
