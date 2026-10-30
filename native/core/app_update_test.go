package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func withUpdateServer(t *testing.T, handler http.HandlerFunc) *nativeEngine {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	previous := nativeUpdateServer
	nativeUpdateServer = server.URL
	t.Cleanup(func() { nativeUpdateServer = previous })
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestNativeCheckUpdate(t *testing.T) {
	latest := `{"version":"1.0.2+102","url":"https://github.com/Xanerix/guoapp/releases/tag/app-v1.0.2-102"}`
	engine := withUpdateServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/v1/latest" || request.URL.RawQuery != "" {
			http.Error(writer, "invalid", http.StatusBadRequest)
			return
		}
		writer.Write([]byte(latest))
	})

	update, err := engine.checkUpdate(context.Background(), "1.0.1+101")
	if err != nil || !update.Available || update.Version != "1.0.2+102" {
		t.Fatalf("update = %+v, err = %v", update, err)
	}
	if update, err = engine.checkUpdate(context.Background(), "1.0.2+102"); err != nil || update.Available {
		t.Fatalf("same build update = %+v, err = %v", update, err)
	}
	latest = `{"version":"9.0.0+900","url":"https://example.test/app.apk"}`
	if update, err = engine.checkUpdate(context.Background(), "1.0.1+101"); err != nil || update.Available {
		t.Fatalf("foreign url update = %+v, err = %v", update, err)
	}
	latest = `{}`
	if update, err = engine.checkUpdate(context.Background(), "1.0.1+101"); err != nil || update.Available {
		t.Fatalf("empty update = %+v, err = %v", update, err)
	}
	if _, err = engine.checkUpdate(context.Background(), "1.0.1"); err == nil {
		t.Fatal("version without build number was accepted")
	}
}

func TestNativeUpdateServerUnset(t *testing.T) {
	previous := nativeUpdateServer
	nativeUpdateServer = ""
	defer func() { nativeUpdateServer = previous }()
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if update, err := engine.checkUpdate(context.Background(), "1.0.1+101"); err != nil || update.Available {
		t.Fatalf("update = %+v, err = %v", update, err)
	}
	if err := engine.reportUsage(context.Background(), "1.0.1+101"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(engine.directory, "install-id")); !os.IsNotExist(err) {
		t.Fatalf("install id created without update server: %v", err)
	}
}

func TestNativeReportUsage(t *testing.T) {
	var payloads []map[string]string
	engine := withUpdateServer(t, func(writer http.ResponseWriter, request *http.Request) {
		var payload map[string]string
		if request.Method != http.MethodPost || request.URL.Path != "/v1/ping" || json.NewDecoder(request.Body).Decode(&payload) != nil {
			http.Error(writer, "invalid", http.StatusBadRequest)
			return
		}
		payloads = append(payloads, payload)
		writer.WriteHeader(http.StatusNoContent)
	})

	for range 2 {
		if err := engine.reportUsage(context.Background(), "1.0.1+101"); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.reportUsage(context.Background(), "1.0.1"); err == nil {
		t.Fatal("version without build number was accepted")
	}

	if len(payloads) != 2 {
		t.Fatalf("payloads = %d", len(payloads))
	}
	first := payloads[0]
	if !nativeInstallIDPattern.MatchString(first["id"]) || first["version"] != "1.0.1+101" || first["platform"] != runtime.GOOS || first["build"] != "hongguojian" && first["build"] != "zhenguojian" {
		t.Fatalf("payload = %+v", first)
	}
	if payloads[1]["id"] != first["id"] {
		t.Fatalf("install id changed: %q -> %q", first["id"], payloads[1]["id"])
	}
}

func TestNativeUpdateServerFallback(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "unavailable", http.StatusBadGateway)
	}))
	t.Cleanup(down.Close)
	pings := 0
	engine := withUpdateServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/ping" {
			pings++
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		writer.Write([]byte(`{"version":"1.0.2+102","url":"https://github.com/Xanerix/guoapp/releases/tag/app-v1.0.2-102"}`))
	})
	nativeUpdateServer = down.URL + "/ , " + nativeUpdateServer

	if update, err := engine.checkUpdate(context.Background(), "1.0.1+101"); err != nil || !update.Available {
		t.Fatalf("update = %+v, err = %v", update, err)
	}
	if err := engine.reportUsage(context.Background(), "1.0.1+101"); err != nil || pings != 1 {
		t.Fatalf("pings = %d, err = %v", pings, err)
	}
}

func TestNativeUpdateDownload(t *testing.T) {
	abi, found := nativeUpdateABIs[runtime.GOARCH]
	if !found {
		t.Skip("no update package for " + runtime.GOARCH)
	}
	name := nativeAppBuildSlug() + "-1.0.2+102-" + abi + ".apk"
	missing := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(missing.Close)
	engine := withUpdateServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/apk/"+name {
			http.NotFound(writer, request)
			return
		}
		writer.Write([]byte("package"))
	})
	nativeUpdateServer = missing.URL + "," + nativeUpdateServer
	directory := filepath.Join(t.TempDir(), "update")

	if _, err := engine.updateDownload(nativeInput{Command: "start", Version: "1.0.2", Directory: directory}); err == nil {
		t.Fatal("version without build number was accepted")
	}
	if _, err := engine.updateDownload(nativeInput{Command: "start", Version: "1.0.2+102", Directory: "update"}); err == nil {
		t.Fatal("relative directory was accepted")
	}
	if _, err := engine.updateDownload(nativeInput{Command: "start", Version: "1.0.2+102", Directory: directory}); err != nil {
		t.Fatal(err)
	}
	var status map[string]any
	for range 200 {
		result, err := engine.updateDownload(nativeInput{Command: "status"})
		if err != nil {
			t.Fatal(err)
		}
		if status = result.(map[string]any); status["done"] == true {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status["done"] != true || status["error"] != nil || status["path"] != filepath.Join(directory, name) || status["received"] != int64(7) {
		t.Fatalf("status = %+v", status)
	}
	if data, err := os.ReadFile(filepath.Join(directory, name)); err != nil || string(data) != "package" {
		t.Fatalf("package = %q, err = %v", data, err)
	}
	if _, err := engine.updateDownload(nativeInput{Command: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.updateDownload(nativeInput{Command: "status"}); err == nil {
		t.Fatal("status after cancel was accepted")
	}
}
