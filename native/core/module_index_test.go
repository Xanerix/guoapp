package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
)

func TestModuleIndexInstallFallsBackAndVerifies(t *testing.T) {
	example := filepath.Join("..", "module_example", "jsexample")
	files := map[string][]byte{}
	sums := map[string]string{}
	for _, name := range []string{moduleManifest, moduleEntry} {
		data, err := os.ReadFile(filepath.Join(example, name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		files[name], sums[name] = data, hex.EncodeToString(sum[:])
	}
	entry := func(id, repo string, api int, hashes map[string]string) moduleIndexEntry {
		return moduleIndexEntry{ID: id, Name: id, Version: "1.0.0", API: api, Repo: repo, Tag: "v1.0.0", SHA256: hashes}
	}
	wrong := map[string]string{moduleManifest: strings.Repeat("0", 64), moduleEntry: strings.Repeat("0", 64)}
	index, err := json.Marshal(map[string]any{"format": moduleIndexFormat, "modules": []moduleIndexEntry{
		entry("jsexample", "owner/jsexample", moduleAPIVersion, sums),
		entry("future", "owner/jsexample", moduleAPIVersion+1, sums),
		entry("renamed", "owner/jsexample", moduleAPIVersion, sums),
		entry("tampered", "owner/jsexample", moduleAPIVersion, wrong),
		entry("bad-repo", "../jsexample", moduleAPIVersion, sums),
	}})
	if err != nil {
		t.Fatal(err)
	}
	var staleHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/broken/index.json":
			http.Error(writer, "down", http.StatusBadGateway)
		case request.URL.Path == "/index.json":
			_, _ = writer.Write(index)
		case strings.HasPrefix(request.URL.Path, "/stale/"):
			staleHits.Add(1)
			_, _ = writer.Write([]byte("stale"))
		case strings.HasPrefix(request.URL.Path, "/files/owner/jsexample/v1.0.0/"):
			_, _ = writer.Write(files[filepath.Base(request.URL.Path)])
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	addresses, patterns := moduleIndexAddresses, moduleFileAddresses
	moduleIndexAddresses = []string{server.URL + "/broken/index.json", server.URL + "/index.json"}
	moduleFileAddresses = []string{server.URL + "/stale/%s/%s/%s", server.URL + "/files/%s/%s/%s"}
	t.Cleanup(func() { moduleIndexAddresses, moduleFileAddresses = addresses, patterns })

	engine := &nativeEngine{downloader: &Downloader{cfg: Config{dataDir: t.TempDir()}, client: server.Client()}, cacheDirectory: t.TempDir()}
	ctx := context.Background()
	entries, mirror, err := engine.moduleIndex(ctx)
	if err != nil || mirror != 1 || len(entries) != 3 || entries[0].ID != "jsexample" || entries[1].ID != "renamed" || entries[2].ID != "tampered" {
		t.Fatalf("index = %+v, %v", entries, err)
	}
	info, err := engine.prepareModule(ctx, "", "", "jsexample", false)
	if err != nil || info.ID != "jsexample" || info.Token == "" || staleHits.Load() != 0 {
		t.Fatalf("prepare = %+v, %v, stale hits %d", info, err, staleHits.Load())
	}
	moduleFileAddresses = []string{server.URL + "/files/%s/%s/%s", server.URL + "/stale/%s/%s/%s"}
	if info, err := engine.prepareModule(ctx, "", "", "jsexample", false); err != nil || info.ID != "jsexample" || staleHits.Load() != 2 {
		t.Fatalf("fallback prepare = %+v, %v, stale hits %d", info, err, staleHits.Load())
	}
	if _, err := engine.prepareModule(ctx, "", "", "renamed", false); err == nil || !strings.Contains(err.Error(), "实际是模块 jsexample") {
		t.Fatalf("renamed error = %v", err)
	}
	if _, err := engine.prepareModule(ctx, "", "", "tampered", false); err == nil || !strings.Contains(err.Error(), "校验不符") {
		t.Fatalf("tampered error = %v", err)
	}
	if _, err := engine.prepareModule(ctx, "", "", "future", false); err == nil || !strings.Contains(err.Error(), "没有 future") {
		t.Fatalf("future error = %v", err)
	}

	previous := bundledModules
	bundledModules = fstest.MapFS{
		"jsexample/" + moduleManifest: {Data: []byte(strings.Replace(string(files[moduleManifest]), `"version": "1.0.0"`, `"version": "0.9.0"`, 1))},
		"jsexample/" + moduleEntry:    {Data: files[moduleEntry]},
	}
	t.Cleanup(func() { bundledModules = previous })
	store, err := engine.moduleStore(ctx)
	items, _ := store["items"].([]moduleIndexEntry)
	if err != nil || len(items) != 4 || !items[0].Bundled || items[0].Version != "0.9.0" || items[1].Bundled || items[1].ID != "jsexample" {
		t.Fatalf("store with bundled and indexed copies = %+v, %v", items, err)
	}
	if info, err := engine.prepareModule(ctx, "", "", "jsexample", true); err != nil || info.Version != "0.9.0" {
		t.Fatalf("bundled prepare = %+v, %v", info, err)
	}
	if info, err := engine.prepareModule(ctx, "", "", "jsexample", false); err != nil || info.Version != "1.0.0" {
		t.Fatalf("indexed prepare beside bundled = %+v, %v", info, err)
	}
	if _, err := engine.prepareModule(ctx, "", "", "renamed", true); err == nil || !strings.Contains(err.Error(), "没有内置 renamed") {
		t.Fatalf("missing bundled error = %v", err)
	}
	if _, err := parseModuleIndex([]byte(`{"format": 2, "modules": []}`)); err == nil || !strings.Contains(err.Error(), "更新应用") {
		t.Fatalf("format error = %v", err)
	}
}
