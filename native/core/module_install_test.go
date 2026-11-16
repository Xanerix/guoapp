package core

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testModulePackage(t *testing.T, folder string, files map[string]string) string {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	for name, content := range files {
		writer, err := archive.Create(folder + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "module.zip")
	if err := os.WriteFile(file, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestModuleInstallUpdateAndUninstall(t *testing.T) {
	example := filepath.Join("..", "module_example", "jsexample")
	manifest, err := os.ReadFile(filepath.Join(example, moduleManifest))
	if err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(filepath.Join(example, moduleEntry))
	if err != nil {
		t.Fatal(err)
	}
	engine := &nativeEngine{downloader: &Downloader{cfg: Config{dataDir: t.TempDir()}}, cacheDirectory: t.TempDir()}
	ctx := context.Background()
	t.Cleanup(func() {
		if _, err := engine.uninstallModule("jsexample"); err != nil {
			t.Fatal(err)
		}
	})

	if _, err := engine.prepareModule(ctx, testModulePackage(t, "", map[string]string{moduleManifest: string(manifest)}), "", "", false); err == nil || !strings.Contains(err.Error(), moduleEntry) {
		t.Fatalf("package without entry error = %v", err)
	}
	if _, err := engine.prepareModule(ctx, testModulePackage(t, "a/b/", map[string]string{moduleManifest: string(manifest), moduleEntry: string(code)}), "", "", false); err == nil {
		t.Fatal("deeply nested package was accepted")
	}

	first, err := engine.prepareModule(ctx, testModulePackage(t, "jsexample/", map[string]string{moduleManifest: string(manifest), moduleEntry: string(code), "README.md": "x"}), "", "", false)
	if err != nil || first.ID != "jsexample" || first.Installed || first.Token == "" {
		t.Fatalf("prepare = %+v, %v", first, err)
	}
	if registeredProviderSource("jsexample") {
		t.Fatal("prepare registered the module")
	}
	result, err := engine.installModule(first.Token)
	if err != nil || result["source"] != "jsexample" || !nativeSourceAvailable("jsexample") {
		t.Fatalf("install = %v, %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(engine.downloader.moduleRoot(), "jsexample", "README.md")); !os.IsNotExist(err) {
		t.Fatalf("extra file was extracted: %v", err)
	}
	if _, err := engine.installModule(first.Token); err == nil {
		t.Fatal("token was reusable")
	}
	before, _ := engine.downloader.provider("jsexample")

	updated := strings.Replace(string(manifest), `"id"`, `"aliases": ["jsexample-old"], "id"`, 1)
	second, err := engine.prepareModule(ctx, testModulePackage(t, "", map[string]string{moduleManifest: updated, moduleEntry: string(code)}), "", "", false)
	if err != nil || !second.Installed {
		t.Fatalf("update prepare = %+v, %v", second, err)
	}
	if _, err := engine.installModule(second.Token); err != nil {
		t.Fatal(err)
	}
	if after, _ := engine.downloader.provider("jsexample"); after == before || canonicalProviderSource("jsexample-old") != "jsexample" {
		t.Fatal("update did not replace the provider")
	}

	clash := strings.Replace(string(manifest), `"id": "jsexample"`, `"id": "other", "aliases": ["jsexample-old"]`, 1)
	if clash == string(manifest) {
		t.Fatal("example manifest format changed")
	}
	if _, err := engine.prepareModule(ctx, testModulePackage(t, "", map[string]string{moduleManifest: clash, moduleEntry: string(code)}), "", "", false); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("alias conflict error = %v", err)
	}

	items, err := engine.modules()
	if err != nil || len(items) != 1 || items[0].ID != "jsexample" || items[0].Error != "" {
		t.Fatalf("modules = %+v, %v", items, err)
	}
	if _, err := engine.uninstallModule("../jsexample"); err == nil {
		t.Fatal("path traversal was accepted")
	}
	if _, err := engine.uninstallModule("jsexample"); err != nil || nativeSourceAvailable("jsexample") {
		t.Fatalf("uninstall left the module registered: %v", err)
	}
}
