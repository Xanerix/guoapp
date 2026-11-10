package core

import "testing"

func TestRegisteredProviderResolvesAliasesAndCapabilities(t *testing.T) {
	for _, alias := range []string{"guipian", "GUIPIAN", "www.guipianwu.com", " guipianwu.com "} {
		if got := canonicalProviderSource(alias); got != sourceGuipian {
			t.Fatalf("canonicalProviderSource(%q) = %q", alias, got)
		}
	}
	if !isHuangguoProviderSource(sourceGuipian) {
		t.Fatal("registered source must be recognised as a provider source")
	}
	if got := providerSourceForURL("https://www.guipianwu.com/nv/1.html"); got != sourceGuipian {
		t.Fatalf("providerSourceForURL = %q", got)
	}
	d := &Downloader{cfg: defaultConfig()}
	if got := d.providerBaseURL(sourceGuipian); got != guipianSiteBaseURL {
		t.Fatalf("providerBaseURL = %q", got)
	}
	p, ok := d.provider("guipianwu.com")
	if !ok {
		t.Fatal("provider lookup by alias failed")
	}
	if _, searchable := p.(Searcher); !searchable {
		t.Fatal("guipian provider must support search")
	}
	if again, _ := d.provider(sourceGuipian); again != p {
		t.Fatal("provider instances must be cached per Downloader")
	}
	if !validNativeCategory(sourceGuipian, "3") || validNativeCategory(sourceGuipian, "nope") {
		t.Fatal("category validation must come from the descriptor")
	}
	if _, ok := d.provider("unknown-source"); ok {
		t.Fatal("unknown source must not resolve to a provider")
	}
}
