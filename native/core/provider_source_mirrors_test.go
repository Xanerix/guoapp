package core

import (
	"strings"
	"testing"
)

func TestParseHuangguoAIPublishHosts(t *testing.T) {
	// 取自 https://huangguoai.pages.dev/publish.js 的真实结构（域名带尾斜杠）。
	body := `
var subdomains = [
    'thu',  // 清华大学
    'pku',  // 北京大学
    'fdu',
];
var urls=[
	'adihsppe.cc/',
	'adihsppe.cc/',
];
`
	hosts := parseHuangguoAIPublishHosts(body)
	if len(hosts) != 3 {
		t.Fatalf("hosts = %v", hosts)
	}
	for _, host := range hosts {
		if !strings.HasSuffix(host, ".adihsppe.cc") || !strings.HasPrefix(host, "https://") {
			t.Fatalf("unexpected host: %s", host)
		}
	}
	if parseHuangguoAIPublishHosts("var urls=[];") != nil {
		t.Fatal("empty publish script should produce no hosts")
	}
}

func TestHuangguoAIMirrorRecognitionCoversCurrentDomain(t *testing.T) {
	for _, address := range []string{
		"https://thu.adihsppe.cc/api/videos/category/ai-duanju",
		"https://huangguoai.com/api/videos/1",
	} {
		if source := providerSourceForURL(address); source != sourceHuangguoAI {
			t.Fatalf("providerSourceForURL(%q) = %q", address, source)
		}
	}
	// 随机子域也要能识别，否则取流成功后无法记住可用镜像。
	if !isHuangguoAIMirrorHost("zju.adihsppe.cc") {
		t.Fatal("adihsppe.cc subdomain not recognized")
	}
}

func TestGeneratedHuangguoAIMirrorsUsesCurrentDomain(t *testing.T) {
	mirrors := generatedHuangguoAIMirrors("https://huangguoai.com/api/videos/1", 6)
	if len(mirrors) == 0 {
		t.Fatal("no mirrors generated")
	}
	for _, mirror := range mirrors {
		if !strings.HasSuffix(mirror, "."+huangguoAIMirrorDomains[0]) {
			t.Fatalf("mirror %s is not on the current domain", mirror)
		}
	}
}

func TestYeguoDomainRecognitionCoversCurrentSites(t *testing.T) {
	for _, address := range []string{
		"https://yeguodj.com/",
		"https://delta.ygrwdsgt.cc/",
		"https://534.zxcrirxx.cc/",
		"https://adviser.ocdjlxow.cc/",
	} {
		if source := providerSourceForURL(address); source != sourceYeguo {
			t.Fatalf("providerSourceForURL(%q) = %q", address, source)
		}
		if !isYeguoActiveSite(address) {
			t.Fatalf("isYeguoActiveSite(%q) = false", address)
		}
	}
	if isYeguoActiveSite("https://example.com/") {
		t.Fatal("unrelated site should not be treated as yeguo")
	}
}
