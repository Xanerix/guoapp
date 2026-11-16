package core

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserFingerprintFollowsLoadedModules(t *testing.T) {
	data := t.TempDir()
	directory := filepath.Join(data, moduleDirectory, "fingerprinttest")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"fingerprinttest","name":"指纹测试","version":"1.0.0","runtime":"js","api":1,"adult":false,
"hosts":["fp.example.invalid","www.fp.example.invalid"],"aliases":["www.fp.example.invalid"],
"baseUrl":"https://fp.example.invalid","browserFingerprint":true}`
	if err := os.WriteFile(filepath.Join(directory, moduleManifest), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, moduleEntry), []byte(`module.exports = {};`), 0o600); err != nil {
		t.Fatal(err)
	}
	engine, err := newNativeEngine(data, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	defer Unregister("fingerprinttest")
	transport := engine.downloader.client.Transport.(*huangguoBrowserTransport)
	for _, host := range []string{"fp.example.invalid", "www.fp.example.invalid", "fp.example.invalid:443"} {
		if source := transport.source(&url.URL{Scheme: "https", Host: host}); source != "fingerprinttest" {
			t.Fatalf("%s 未走浏览器指纹，得到 %q", host, source)
		}
	}
	if source := transport.source(&url.URL{Scheme: "https", Host: "other.example.invalid"}); source != "" {
		t.Fatalf("未声明的域名走了浏览器指纹：%q", source)
	}
	Unregister("fingerprinttest")
	if source := transport.source(&url.URL{Scheme: "https", Host: "fp.example.invalid"}); source != "" {
		t.Fatalf("卸载后的模块仍走浏览器指纹：%q", source)
	}
}
