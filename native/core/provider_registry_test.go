package core

import "testing"

func TestRegisteredProviderResolvesAliasesAndCapabilities(t *testing.T) {
	for _, alias := range []string{"guipian", "GUIPIAN", "www.guipianwu.com", " guipianwu.com "} {
		if got := canonicalProviderSource(alias); got != sourceGuipian {
			t.Fatalf("canonicalProviderSource(%q) = %q", alias, got)
		}
	}
	if !registeredProviderSource(sourceGuipian) {
		t.Fatal("registered source must be recognised as a provider source")
	}
	if got := ProviderSourceForURL("https://www.guipianwu.com/nv/1.html"); got != sourceGuipian {
		t.Fatalf("providerSourceForURL = %q", got)
	}
	d := &Downloader{cfg: DefaultConfig()}
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
	if !ValidNativeCategory(sourceGuipian, "3") || ValidNativeCategory(sourceGuipian, "nope") {
		t.Fatal("category validation must come from the descriptor")
	}
	if _, ok := d.provider("unknown-source"); ok {
		t.Fatal("unknown source must not resolve to a provider")
	}
}

func TestRegisteredProviderHostSuffixesAndConfiguredBaseURL(t *testing.T) {
	if got := ProviderSourceForURL("https://some-line.buxefaex.cc/drama/video/1/"); got != sourceYeguo {
		t.Fatalf("yeguo suffix host resolved to %q", got)
	}
	d := &Downloader{cfg: DefaultConfig()}
	huangjuFixtureProvider(d, huangjuAPIBaseURL, "https://huangju.test/")
	if got := d.providerBaseURL(sourceHuangju); got != "https://huangju.test" {
		t.Fatalf("configured huangju base ignored: %q", got)
	}
	if got := d.providerURLCandidates("https://huangju.net/x"); len(got) != 1 {
		t.Fatalf("huangju must not rotate hosts, got %v", got)
	}
	if !ValidNativeCategory(sourceHuangju, huangjuNewestCategory) || ValidNativeCategory(sourceDSD, "abc") {
		t.Fatal("descriptor category validation mismatch")
	}
}
