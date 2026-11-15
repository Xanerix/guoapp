package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBundledModulesInstallRespectRemovalAndVersions(t *testing.T) {
	example := filepath.Join("..", "module_example", "jsexample")
	manifest, err := os.ReadFile(filepath.Join(example, moduleManifest))
	if err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(filepath.Join(example, moduleEntry))
	if err != nil {
		t.Fatal(err)
	}
	withVersion := func(version string) []byte {
		return []byte(strings.Replace(string(manifest), `"version": "1.0.0"`, `"version": "`+version+`"`, 1))
	}
	if string(withVersion("9.9.9")) == string(manifest) {
		t.Fatal("example manifest format changed")
	}
	previous := bundledModules
	bundledModules = fstest.MapFS{
		"jsexample/" + moduleManifest: {Data: withVersion("1.2.0")},
		"jsexample/" + moduleEntry:    {Data: code},
	}
	t.Cleanup(func() { bundledModules = previous })

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "down", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)
	addresses := moduleIndexAddresses
	moduleIndexAddresses = []string{server.URL + "/index.json"}
	t.Cleanup(func() { moduleIndexAddresses = addresses })

	engine := &nativeEngine{downloader: &Downloader{cfg: Config{dataDir: t.TempDir()}, client: server.Client()}, cacheDirectory: t.TempDir()}
	d := engine.downloader
	t.Cleanup(func() {
		if _, err := engine.uninstallModule("jsexample"); err != nil {
			t.Fatal(err)
		}
	})
	installedVersion := func() string {
		module, err := readModule(filepath.Join(d.moduleRoot(), "jsexample"))
		if err != nil {
			return ""
		}
		return module.manifest.Version
	}

	d.loadModules()
	if installedVersion() != "1.2.0" || !nativeSourceAvailable("jsexample") {
		t.Fatalf("first launch installed %q", installedVersion())
	}

	if _, err := engine.uninstallModule("jsexample"); err != nil {
		t.Fatal(err)
	}
	d.loadModules()
	if installedVersion() != "" || nativeSourceAvailable("jsexample") {
		t.Fatal("removed bundled module came back on launch")
	}

	store, err := engine.moduleStore(context.Background())
	items, _ := store["items"].([]moduleIndexEntry)
	if err != nil || len(items) != 1 || !items[0].Bundled || items[0].Version != "1.2.0" || store["warning"] == nil {
		t.Fatalf("store = %+v, %v", store, err)
	}
	prepared, err := engine.prepareModule(context.Background(), "", "", "jsexample")
	if err != nil || prepared.Version != "1.2.0" {
		t.Fatalf("prepare bundled = %+v, %v", prepared, err)
	}
	if _, err := engine.installModule(prepared.Token); err != nil {
		t.Fatal(err)
	}
	if d.removedBundledModules()["jsexample"] {
		t.Fatal("reinstall did not clear the removal mark")
	}

	loadedModules.Lock()
	err = d.writeModuleLocked("jsexample", withVersion("2.0.0"), code)
	loadedModules.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	d.loadModules()
	if installedVersion() != "2.0.0" {
		t.Fatalf("newer user install was replaced by %q", installedVersion())
	}

	loadedModules.Lock()
	err = d.writeModuleLocked("jsexample", withVersion("1.1.0"), code)
	loadedModules.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	d.loadModules()
	if installedVersion() != "1.2.0" {
		t.Fatalf("older install was not upgraded, got %q", installedVersion())
	}
}

func TestNewerModuleVersion(t *testing.T) {
	cases := []struct {
		candidate, current string
		newer              bool
	}{
		{"1.0.1", "1.0.0", true},
		{"1.10.0", "1.9.9", true},
		{"1.0.0", "1.0.0", false},
		{"1.0.0", "1.0.1", false},
		{"1.0.0", "dev", false},
		{"latest", "1.0.0", false},
	}
	for _, item := range cases {
		if got := newerModuleVersion(item.candidate, item.current); got != item.newer {
			t.Errorf("newerModuleVersion(%q, %q) = %v", item.candidate, item.current, got)
		}
	}
}
