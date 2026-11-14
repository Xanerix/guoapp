package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMoveLegacyNativeCache(t *testing.T) {
	directory, cache := t.TempDir(), t.TempDir()
	write := func(root, name, body string) {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(directory, "catalogs.json", "legacy")
	write(directory, "covers-v1/a.img", "cover")
	write(directory, "rankings.json", "legacy")
	write(cache, "rankings.json", "current")
	write(directory, "resource-settings.json", "settings")

	moveLegacyNativeCache(directory, cache)

	for name, want := range map[string]string{"catalogs.json": "legacy", "covers-v1/a.img": "cover", "rankings.json": "current"} {
		if body, err := os.ReadFile(filepath.Join(cache, name)); err != nil || string(body) != want {
			t.Fatalf("%s = %q, %v; want %q", name, body, err, want)
		}
	}
	for _, name := range []string{"catalogs.json", "covers-v1", "rankings.json"} {
		if _, err := os.Lstat(filepath.Join(directory, name)); !os.IsNotExist(err) {
			t.Fatalf("%s 仍留在数据目录: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(directory, "resource-settings.json")); err != nil {
		t.Fatal(err)
	}

	write(cache, "catalogs.json", "kept")
	moveLegacyNativeCache(cache, cache)
	if body, _ := os.ReadFile(filepath.Join(cache, "catalogs.json")); string(body) != "kept" {
		t.Fatal("数据目录与缓存目录相同时不应删除缓存")
	}
}

type statefulTestProvider struct {
	Provider
	state json.RawMessage
}

func (p *statefulTestProvider) Descriptor() SourceDescriptor            { return SourceDescriptor{ID: "hongguo"} }
func (p *statefulTestProvider) CatalogState() json.RawMessage           { return p.state }
func (p *statefulTestProvider) RestoreCatalogState(raw json.RawMessage) { p.state = raw }

func TestProviderStatesSurviveCacheRemoval(t *testing.T) {
	directory, cache := t.TempDir(), t.TempDir()
	start := func() (*nativeEngine, *statefulTestProvider) {
		provider := &statefulTestProvider{}
		engine := &nativeEngine{directory: directory, cacheDirectory: cache,
			downloader: &Downloader{providers: map[string]Provider{"hongguo": provider}},
			catalogs:   map[string][]LibraryDrama{}, catalogStates: map[string]nativeCatalogState{}}
		engine.loadCatalogCache()
		return engine, provider
	}
	legacy := `{"version":3,"catalogs":{},"states":{},"providerStates":{"hongguo":{"deviceId":"1"}}}`
	if err := os.WriteFile(filepath.Join(cache, "catalogs.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if _, provider := start(); string(provider.state) != `{"deviceId":"1"}` {
		t.Fatalf("旧剧库缓存里的设备没有恢复: %s", provider.state)
	}
	if err := os.Remove(filepath.Join(cache, "catalogs.json")); err != nil {
		t.Fatal(err)
	}
	engine, provider := start()
	if string(provider.state) != `{"deviceId":"1"}` {
		t.Fatalf("清掉缓存后设备丢失: %s", provider.state)
	}
	provider.state = json.RawMessage(`{"deviceId":"2"}`)
	engine.commitProviderStateLocked("hongguo")
	if err := engine.writeCatalogDiskLocked(); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(cache, "catalogs.json")); strings.Contains(string(body), "deviceId") {
		t.Fatalf("设备仍写进剧库缓存: %s", body)
	}
	if err := os.RemoveAll(cache); err != nil {
		t.Fatal(err)
	}
	if _, provider := start(); string(provider.state) != `{"deviceId":"2"}` {
		t.Fatalf("更新后的设备没有保存到数据目录: %s", provider.state)
	}
}
