package core

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

func TestManagedModuleCloudUpdateFailoverVerifyAndPreserveState(t *testing.T) {
	example := filepath.Join("..", "module_example", "jsexample")
	baseManifest, err := os.ReadFile(filepath.Join(example, moduleManifest))
	if err != nil {
		t.Fatal(err)
	}
	statefulManifest := strings.Replace(
		string(baseManifest),
		`"capabilities": ["search"]`,
		`"capabilities": ["search", "catalogState"]`,
		1,
	)
	if statefulManifest == string(baseManifest) {
		t.Fatal("example manifest capabilities format changed")
	}
	withVersion := func(version string) []byte {
		return []byte(strings.Replace(statefulManifest, `"version": "1.0.0"`, `"version": "`+version+`"`, 1))
	}
	code := []byte(`
var savedState = null;
function catalogState() { return savedState; }
function restoreCatalogState(raw) { savedState = raw; }
function categories() { return [{ id: "all", name: "全部" }]; }
function catalogPage() { return { items: [], hasMore: false }; }
function detail() { return { drama: { id: "jsexample:1", title: "x" }, chapters: [] }; }
function resolveMedia() { return { url: "https://example.invalid/a.m3u8" }; }
module.exports = { catalogState, restoreCatalogState, categories, catalogPage, detail, resolveMedia };
`)

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signedZip := func(version string) ([]byte, managedModuleRelease) {
		pkg, err := zipModuleFiles(withVersion(version), code)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(pkg)
		sig := ed25519.Sign(privateKey, pkg)
		return pkg, managedModuleRelease{
			Version: version,
			SHA256:  hex.EncodeToString(sum[:]),
			Sig:     base64.StdEncoding.EncodeToString(sig),
			Path:    "jsexample-" + version + ".zip",
		}
	}
	pkg120, rel120 := signedZip("1.2.0")
	latestBody, err := json.Marshal(managedModuleManifest{
		Format: managedModuleFormat,
		APIs:   map[string]managedModuleRelease{"1": rel120},
	})
	if err != nil {
		t.Fatal(err)
	}

	var primaryHits atomic.Int32
	var fallbackHits atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		primaryHits.Add(1)
		http.Error(writer, "unavailable", http.StatusBadGateway)
	}))
	t.Cleanup(primary.Close)
	fallback := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fallbackHits.Add(1)
		if request.Header.Get("X-App-Client") != managedModuleClient {
			http.Error(writer, "missing client header", http.StatusForbidden)
			return
		}
		switch request.URL.Path {
		case "/v1/modules/jsexample/latest.json":
			_, _ = writer.Write(latestBody)
		case "/v1/modules/jsexample/" + rel120.Path:
			_, _ = writer.Write(pkg120)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(fallback.Close)

	previousBundled := bundledModules
	bundledModules = fstest.MapFS{
		"jsexample/" + moduleManifest: {Data: withVersion("1.1.0")},
		"jsexample/" + moduleEntry:    {Data: code},
	}
	t.Cleanup(func() { bundledModules = previousBundled })

	previousServer, previousKey := nativeModuleUpdateServer, nativeModuleUpdatePublicKey
	nativeModuleUpdateServer = primary.URL + "," + fallback.URL
	nativeModuleUpdatePublicKey = base64.StdEncoding.EncodeToString(publicKey)
	t.Cleanup(func() {
		nativeModuleUpdateServer = previousServer
		nativeModuleUpdatePublicKey = previousKey
	})

	dataDir := t.TempDir()
	engine := &nativeEngine{
		downloader:     &Downloader{cfg: Config{dataDir: dataDir}, client: fallback.Client(), diagnostics: newDiagnosticLog(dataDir)},
		cacheDirectory: t.TempDir(),
	}
	d := engine.downloader
	t.Cleanup(func() {
		_, _ = engine.uninstallModule("jsexample")
	})

	d.loadModules()
	first, ok := d.provider("jsexample")
	if !ok {
		t.Fatal("bundled module did not load")
	}
	stateful, ok := providerAs[CatalogStateful](first)
	if !ok {
		t.Fatal("stateful capability missing")
	}
	stateful.RestoreCatalogState(json.RawMessage(`{"deviceId":"998877"}`))

	result, err := engine.updateBundledModules(context.Background())
	if err != nil || !result.Updated || result.ID != "jsexample" || result.Version != "1.2.0" {
		t.Fatalf("update = %+v, %v", result, err)
	}
	if primaryHits.Load() == 0 || fallbackHits.Load() < 2 {
		t.Fatalf("primary hits = %d, fallback hits = %d", primaryHits.Load(), fallbackHits.Load())
	}
	installed, err := readModule(filepath.Join(d.moduleRoot(), "jsexample"))
	if err != nil || installed.manifest.Version != "1.2.0" {
		t.Fatalf("installed version = %v, %v", installed, err)
	}
	second, ok := d.provider("jsexample")
	if !ok || second == first {
		t.Fatal("provider was not replaced")
	}
	nextStateful, ok := providerAs[CatalogStateful](second)
	if !ok || string(nextStateful.CatalogState()) != `{"deviceId":"998877"}` {
		t.Fatalf("preserved state = %s", nextStateful.CatalogState())
	}

	repeat, err := engine.updateBundledModules(context.Background())
	if err != nil || repeat.Updated {
		t.Fatalf("same version triggered update: %+v, %v", repeat, err)
	}

	_, otherPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pkg130, err := zipModuleFiles(withVersion("1.3.0"), code)
	if err != nil {
		t.Fatal(err)
	}
	sum130 := sha256.Sum256(pkg130)
	badRel := managedModuleRelease{
		Version: "1.3.0",
		SHA256:  hex.EncodeToString(sum130[:]),
		Sig:     base64.StdEncoding.EncodeToString(ed25519.Sign(otherPrivate, pkg130)),
		Path:    "jsexample-1.3.0.zip",
	}
	badLatest, _ := json.Marshal(managedModuleManifest{
		Format: managedModuleFormat,
		APIs:   map[string]managedModuleRelease{"1": badRel},
	})
	tamperedServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/modules/jsexample/latest.json":
			_, _ = writer.Write(badLatest)
		case "/v1/modules/jsexample/" + badRel.Path:
			_, _ = writer.Write(pkg130)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(tamperedServer.Close)
	nativeModuleUpdateServer = tamperedServer.URL

	rejected, err := engine.updateBundledModules(context.Background())
	if err != nil || rejected.Updated {
		t.Fatalf("tampered signature was accepted: %+v, %v", rejected, err)
	}
	installed, err = readModule(filepath.Join(d.moduleRoot(), "jsexample"))
	if err != nil || installed.manifest.Version != "1.2.0" {
		t.Fatalf("installed version changed after bad signature: %v", installed)
	}

	if _, err := engine.uninstallModule("jsexample"); err != nil {
		t.Fatal(err)
	}
	nativeModuleUpdateServer = fallback.URL
	skipped, err := engine.updateBundledModules(context.Background())
	if err != nil || skipped.Updated || nativeSourceAvailable("jsexample") {
		t.Fatalf("uninstalled module was reinstalled by cloud update: %+v, %v", skipped, err)
	}
}
