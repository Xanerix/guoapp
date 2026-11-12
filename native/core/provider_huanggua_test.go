package core

import (
	"encoding/base64"
	"strings"
	"testing"
)

func huangguaCardSample(alias, title, cover string) string {
	return `<article data-xpch="card-drama" class="group"><div class="ph-cover"><img src="/_img/` +
		base64.RawStdEncoding.EncodeToString([]byte(cover)) +
		`.jpeg" alt="` + title + `"><span>更新至12集</span></div><div class="truncate">` + title +
		`</div><p>69457热度</p><a href="/drama/` + alias + `">详情</a></article>`
}

func TestParseHuangguaCards(t *testing.T) {
	host := "https://abc.example.test"
	body := huangguaCardSample("dj-028472f1", "琼明神女录", "https://pic.example.test/cover/1.jpeg") +
		huangguaCardSample("18-1955-9f8f", "18岁太奶驾到", "https://pic.example.test/cover/2.jpeg")
	items := parseHuangguaCards(body, host)
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
	first := items[0]
	if first.ID != "huanggua:dj-028472f1" || first.Source != sourceHuanggua || first.Title != "琼明神女录" {
		t.Fatalf("first item = %+v", first)
	}
	// 封面必须保留站内 /_img/ 解密代理路径，不能解码成原始 CDN 地址。
	wantCover := host + "/_img/" +
		base64.RawStdEncoding.EncodeToString([]byte("https://pic.example.test/cover/1.jpeg")) +
		".jpeg"
	if first.Cover != wantCover {
		t.Fatalf("cover = %q, want %q", first.Cover, wantCover)
	}
	if first.EpisodeCount != "12" || first.Views != "69457热度" {
		t.Fatalf("metadata missing: %+v", first)
	}
	if items[1].ID != "huanggua:18-1955-9f8f" {
		t.Fatalf("second id = %s", items[1].ID)
	}
}

func TestHuangguaChaptersDeduplicatesAndSorts(t *testing.T) {
	body := `<a href="/play/dj-1/3">3</a><a href="/play/dj-1/1">1</a><a href="/play/dj-1/2">2</a><a href="/play/dj-1/2">2</a>`
	chapters := huangguaChapters(body, "https://abc.example.test", "dj-1")
	if len(chapters) != 3 {
		t.Fatalf("chapters = %+v", chapters)
	}
	for index, want := range []string{"第1集", "第2集", "第3集"} {
		if chapters[index].Title != want {
			t.Fatalf("chapter %d title = %s", index, chapters[index].Title)
		}
	}
	if chapters[1].PageURL != "https://abc.example.test/play/dj-1/2" {
		t.Fatalf("page url = %s", chapters[1].PageURL)
	}
	if chapters[0].Source != sourceHuanggua || !strings.HasPrefix(chapters[0].ID, "huanggua:dj-1:") {
		t.Fatalf("chapter identity = %+v", chapters[0])
	}
	if len(huangguaChapters("<html></html>", "https://abc.example.test", "dj-1")) != 1 {
		t.Fatal("missing episode links should fall back to a single episode")
	}
}

func TestParseHuangguaPlayURL(t *testing.T) {
	escaped := `{"source_url":"https:\u002F\u002Fhls.example.test\u002Fv\u002Fa.m3u8?auth_key=1\u0026v=3","h265_url":""}`
	if got := parseHuangguaPlayURL(escaped); got != "https://hls.example.test/v/a.m3u8?auth_key=1&v=3" {
		t.Fatalf("escaped url = %q", got)
	}
	plain := `<script>var x = {"source_url":"https://hls.example.test/b.m3u8?x=1"}</script>`
	if got := parseHuangguaPlayURL(plain); got != "https://hls.example.test/b.m3u8?x=1" {
		t.Fatalf("plain url = %q", got)
	}
	if got := parseHuangguaPlayURL("nothing here"); got != "" {
		t.Fatalf("unexpected url %q", got)
	}
}

func TestHuangguaAndDsdDomainRecognition(t *testing.T) {
	for _, address := range []string{
		"https://abc.ywwegfkd.cc/drama/dj-1",
		"https://porncz.eyrkjyudz.cc/play/dj-1/1",
		"https://hgdja1.com/",
		"https://hgdju.com/",
	} {
		if source := providerSourceForURL(address); source != sourceHuanggua {
			t.Fatalf("providerSourceForURL(%q) = %q", address, source)
		}
	}
	for _, address := range []string{"https://dsd901.lol/index.php/vod/type/id/1.html", "https://www.dsd.com.se/"} {
		if source := providerSourceForURL(address); source != sourceDSD {
			t.Fatalf("providerSourceForURL(%q) = %q", address, source)
		}
	}
	if got := canonicalProviderSource("dsd901.lol"); got != sourceDSD {
		t.Fatalf("canonicalProviderSource(dsd901.lol) = %s", got)
	}
	if got := canonicalProviderSource("hgdja1.com"); got != sourceHuanggua {
		t.Fatalf("canonicalProviderSource(hgdja1.com) = %s", got)
	}
	if !validHuangguaCategory("") || !validHuangguaCategory("aiduanju") || validHuangguaCategory("../x") {
		t.Fatal("huanggua category validation is wrong")
	}
}
