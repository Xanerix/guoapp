package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func testDataEngine(t *testing.T, directory, cache, root string) *nativeEngine {
	t.Helper()
	for _, folder := range []string{directory, cache, root} {
		if err := os.MkdirAll(folder, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return &nativeEngine{
		directory:      directory,
		cacheDirectory: cache,
		covers:         newNativeCoverCache(cache, nil),
		downloads:      &nativeDownloads{root: root, active: map[string]context.CancelFunc{}, converting: map[string]bool{}},
	}
}

func writeDataFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestClearCacheEmptiesCacheDirectory(t *testing.T) {
	base := t.TempDir()
	engine := testDataEngine(t, filepath.Join(base, "data"), filepath.Join(base, "data", "cache"), filepath.Join(base, "data", "downloads"))
	writeDataFile(t, filepath.Join(engine.cacheDirectory, "covers-v1", "a.img"))
	writeDataFile(t, filepath.Join(engine.cacheDirectory, "catalogs.json"))
	writeDataFile(t, filepath.Join(engine.directory, "resource-settings.json"))

	if err := engine.clearCache(); err != nil {
		t.Fatal(err)
	}
	if engine.cacheBytes() != 0 {
		t.Fatalf("缓存未清空，剩余 %d 字节", engine.cacheBytes())
	}
	if _, err := os.Stat(engine.cacheDirectory); err != nil {
		t.Fatalf("缓存目录本身不应删除: %v", err)
	}
	if _, err := os.Stat(filepath.Join(engine.directory, "resource-settings.json")); err != nil {
		t.Fatalf("清理缓存不应影响数据目录: %v", err)
	}
}

func TestClearCacheRefusesWhenCacheHoldsData(t *testing.T) {
	cache := t.TempDir()
	engine := testDataEngine(t, filepath.Join(cache, "data"), cache, filepath.Join(cache, "data", "downloads"))
	writeDataFile(t, filepath.Join(engine.directory, "resource-settings.json"))

	if err := engine.clearCache(); err == nil {
		t.Fatal("缓存目录包含数据目录时应拒绝清理")
	}
	if _, err := os.Stat(filepath.Join(engine.directory, "resource-settings.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteAllDataRemovesEverything(t *testing.T) {
	base := t.TempDir()
	external := filepath.Join(base, "external", "zhenguojian-downloads")
	engine := testDataEngine(t, filepath.Join(base, "data"), filepath.Join(base, "cache"), external)
	writeDataFile(t, filepath.Join(engine.directory, "modules", "demo", "module.json"))
	writeDataFile(t, filepath.Join(engine.cacheDirectory, "rankings.json"))
	writeDataFile(t, filepath.Join(external, "index.json"))

	leftover, err := engine.deleteAllData()
	if err != nil || len(leftover) != 0 {
		t.Fatalf("leftover = %v, err = %v", leftover, err)
	}
	if _, err := os.Stat(external); !os.IsNotExist(err) {
		t.Fatalf("下载目录应删除: %v", err)
	}
	for _, folder := range []string{engine.directory, engine.cacheDirectory} {
		if entries, err := os.ReadDir(folder); err != nil || len(entries) != 0 {
			t.Fatalf("%s 应清空且保留: %v, %v", folder, entries, err)
		}
	}
	if !engine.downloads.moving {
		t.Fatal("删除后应阻止新的下载任务")
	}
}

func TestStorageCountsCache(t *testing.T) {
	base := t.TempDir()
	engine := testDataEngine(t, filepath.Join(base, "data"), filepath.Join(base, "cache"), filepath.Join(base, "data", "downloads"))
	writeDataFile(t, filepath.Join(engine.downloads.root, "index.json"))
	writeDataFile(t, filepath.Join(engine.cacheDirectory, "covers-v1", "a.img"))

	info, err := engine.storage()
	if err != nil {
		t.Fatal(err)
	}
	if info.Bytes != 2 || info.Files != 1 {
		t.Fatalf("bytes = %d, files = %d; want 2, 1", info.Bytes, info.Files)
	}
}
