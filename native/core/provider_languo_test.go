package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const languoTestCard = `<article class="ll-card" data-track-id="990622" data-track-title="吉林童话 佛精计">
<a class="ll-card__body" href="/video/990622/"><span class="ll-card__art">
<img src="/static/web/images/cover-placeholder.png" data-src="https://pic.drleao.cn/upload/cover.webp?auth_key=1#fb=x" alt="吉林童话 佛精计">
<span class="ll-card__score">8.2分</span><span class="ll-card__ep">更新至 2 集</span></span><h3>吉林童话 佛精计</h3></a>
<div class="ll-card__tags"><a href="/duanju/tags/original/">蓝果原创</a><a href="/duanju/tags/ai-drama/">AI短剧</a></div>
</article>`

const languoTestWideCard = `<article class="ll-card ll-card--wide"><a class="ll-card__body" href="/video/126410/">
<span class="ll-card__art"><img src="/static/web/images/cover-placeholder-wide.png" data-src="https://pic.drleao.cn/upload/wide.webp?auth_key=2"></span>
<h3>帅哥无套车震野战</h3></a><div class="ll-card__tags"><a href="/gv/tags/xiaoxianrou/">小鲜肉</a></div></article>`

const languoTestDetail = `<html><head><title>顾禁南囚 - 蓝果短剧</title>
<meta name="description" content="详情简介">
<meta property="og:image" content="https://pic.drleao.cn/og.jpg">
</head><body>
<nav class="lw-crumb" aria-label="面包屑"><ol><li><a href="/">首页</a></li><li><a href="/duanju/">男同短剧</a></li><li><span>顾禁南囚</span></li></ol></nav>
<main class="lw" data-lw data-vid="990607" data-ep="1" data-total="6" data-eps="1" data-title="顾禁南囚"
 data-video-url="/video/990607/" data-play-url="/video/990607/">
<a href="/video/990607/">第1集</a><a href="/video/990607/ep-2/">第2集</a><a href="/video/990607/ep-6/">第6集</a>
<script id="videoInitialData" type="application/json">{"id":"990607","title":"顾禁南囚","description":"十七岁的温海","coverSrc":"https://pic.drleao.cn/cover.jpg?auth_key=3","views":"3.7万","time":"2026-10-23 00:11:42","tags":["蓝果原创","AI短剧"],"ep":1,"epPlaySrcs":{"1":"https://yd-hls.hzjvlo.cn/a.m3u8?auth_key=1\u0026v=3"}}</script>
</main></body></html>`

func TestParseLanguoCards(t *testing.T) {
	items := parseLanguoCards(languoTestCard+languoTestWideCard, "https://languo.ai/duanju/", "男同短剧")
	if len(items) != 2 {
		t.Fatalf("unexpected languo card count: %d", len(items))
	}
	first := nativeNormalize(items[0])
	if first.ID != "languo:990622" || first.Title != "吉林童话 佛精计" || first.Episodes != 2 ||
		items[0].Score != "8.2分" || items[0].Remark != "更新至 2 集" || len(items[0].Tags) != 2 ||
		!strings.HasPrefix(first.Cover, "https://pic.drleao.cn/") {
		t.Fatalf("languo card parsed incorrectly: %+v", items[0])
	}
	wide := nativeNormalize(items[1])
	if wide.ID != "languo:126410" || wide.Title != "帅哥无套车震野战" || items[1].CategoryName != "男同短剧" ||
		!strings.HasPrefix(wide.Cover, "https://pic.drleao.cn/") || len(items[1].Tags) != 1 {
		t.Fatalf("languo wide card parsed incorrectly: %+v", items[1])
	}
}

func TestLanguoDetailAndChapters(t *testing.T) {
	drama := languoDetailMetadata(languoTestDetail, "https://languo.ai/video/990607/", "990607")
	if drama.ID != "languo:990607" || drama.Title != "顾禁南囚" || drama.Desc != "十七岁的温海" ||
		drama.Views != "3.7万次播放" || drama.OnlineDate != "2026-10-23" || drama.EpisodeCount != 6 ||
		drama.CategoryName != "男同短剧" || len(drama.Tags) != 2 || drama.Cover != "https://pic.drleao.cn/cover.jpg?auth_key=3" {
		t.Fatalf("languo detail parsed incorrectly: %+v", drama)
	}
	chapters := languoChapters(languoTestDetail, "990607")
	if len(chapters) != 6 {
		t.Fatalf("unexpected languo chapter count: %d", len(chapters))
	}
	if chapters[0].PageURL != "https://languo.ai/video/990607/" ||
		chapters[1].PageURL != "https://languo.ai/video/990607/ep-2/" ||
		chapters[5].PageURL != "https://languo.ai/video/990607/ep-6/" ||
		chapters[1].Source != sourceLanguo || chapters[1].EpisodeString(1) == "" {
		t.Fatalf("unexpected languo chapters: %+v", chapters)
	}
	media := languoPlaybackURL(languoTestDetail, "https://languo.ai/video/990607/")
	if media != "https://yd-hls.hzjvlo.cn/a.m3u8?auth_key=1&v=3" {
		t.Fatalf("languo playback address parsed incorrectly: %s", media)
	}
}

func TestLanguoCatalogSearchAndPublishFallback(t *testing.T) {
	var sawPublish, sawDirect bool
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		switch {
		case request.URL.Host == "1g9z14j0.cc":
			sawPublish = true
			return sourceFixtureResponse(request, http.StatusOK, `<a href="https://hkc44e.y0bsjd8v.cc">线路 01</a>`), nil
		case request.URL.Host == "languo.ai":
			return nil, errors.New("languo.ai unavailable")
		case request.URL.Host == "www.languo.ai":
			return nil, errors.New("www.languo.ai unavailable")
		case request.URL.Host == "hkc44e.y0bsjd8v.cc":
			sawDirect = true
			switch {
			case request.URL.Path == "/duanju/":
				return sourceFixtureResponse(request, http.StatusOK, languoTestCard), nil
			case request.URL.Path == "/video/990607/":
				return sourceFixtureResponse(request, http.StatusOK, languoTestDetail), nil
			case strings.HasPrefix(request.URL.Path, "/search/video/"):
				return sourceFixtureResponse(request, http.StatusOK, languoTestCard), nil
			case strings.HasPrefix(request.URL.Path, "/search/gv/"):
				return sourceFixtureResponse(request, http.StatusOK, languoTestWideCard), nil
			}
		}
		return sourceFixtureResponse(request, http.StatusNotFound, "not found"), nil
	})
	items, more, err := d.fetchLanguoCatalogPage(context.Background(), 1, "")
	if err != nil || len(items) != 1 || more || !sawPublish || !sawDirect {
		t.Fatalf("languo catalog fallback failed: items=%d more=%t publish=%t direct=%t err=%v", len(items), more, sawPublish, sawDirect, err)
	}
	drama, chapters, err := d.fetchLanguoDetail(context.Background(), "990607")
	if err != nil || drama.Title != "顾禁南囚" || len(chapters) != 6 {
		t.Fatalf("languo detail fallback failed: %+v %d %v", drama, len(chapters), err)
	}
	search, _, err := d.fetchLanguoSearchPage(context.Background(), 1, "逆徒")
	if err != nil || len(search) != 2 {
		t.Fatalf("languo search failed: %d %v", len(search), err)
	}
}

func TestProviderPublishScriptParsesRotatingDomains(t *testing.T) {
	script := `
var subdomains = ['thu', 'pku', 'fdu'];
var urls=[ 'h2na6qa39.cc/', 'ggood01z.cc/' ];
var foreverUrls = ['https://huangguoai.com'];
`
	hosts := parseProviderPublishScriptHosts(script)
	if len(hosts) < 6 || hosts[0] != "https://thu.h2na6qa39.cc" || hosts[1] != "https://thu.ggood01z.cc" {
		t.Fatalf("publish script hosts parsed incorrectly: %v", hosts)
	}
	page := `<a href="https://hkc44e.y0bsjd8v.cc">线路</a><a href="https://x.com/languodrama">x</a><a href="https://languo.ai/about/">about</a>`
	htmlHosts := parseProviderPublishHTMLHosts(page)
	if len(htmlHosts) != 1 || htmlHosts[0] != "https://hkc44e.y0bsjd8v.cc" {
		t.Fatalf("publish html hosts parsed incorrectly: %v", htmlHosts)
	}
}

func TestProviderSourceRecognizesLanguoAndRotatedHosts(t *testing.T) {
	for _, address := range []string{
		"https://languo.ai/duanju/",
		"https://www.languo.me/",
		"https://hkc44e.y0bsjd8v.cc/video/990607/",
	} {
		if got := providerSourceForURL(address); got != sourceLanguo {
			t.Fatalf("languo host not recognized: %s -> %s", address, got)
		}
	}
	rememberPublishedProviderHosts(sourceHuangguoAI, []string{"https://thu.h2na6qa39.cc"})
	if got := providerSourceForURL("https://thu.h2na6qa39.cc/detail/1/"); got != sourceHuangguoAI {
		t.Fatalf("rotated huangguo host not recognized: %s", got)
	}
	if got := canonicalProviderSource("languotv.com"); got != sourceLanguo {
		t.Fatalf("languo alias not canonicalized: %s", got)
	}
	if !validLanguoCategory("duanju-ai") || !validLanguoCategory("") || validLanguoCategory("bad:value") {
		t.Fatal("languo category validation changed")
	}
	if name := languoCategoryName("gv"); name != "男同GV" {
		t.Fatalf("unexpected languo category name: %s", name)
	}
	if path, ok := languoCatalogPath("duanju-hot", 2); !ok || path != "/duanju/hot/2/" {
		t.Fatalf("unexpected languo catalog path: %s %t", path, ok)
	}
	if _, ok := languoCatalogPath("gv", 2); ok {
		t.Fatal("gv should not paginate")
	}
	if id := languoVideoID("/video/990607/ep-2/"); id != "990607" {
		t.Fatalf("languo id parsed incorrectly: %s", id)
	}
}
