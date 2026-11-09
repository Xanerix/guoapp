package core

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const xvideosCardFixture = `<div id="video_abcdef12345" data-id="58556869" data-eid="abcdef12345"  data-is-channel="1" class="frame-block thumb-block  "><div class="thumb-inside"><div class="thumb"><a href="/video.abcdef12345/58556869/0/sample_"><img src="https://assets.example.test/img/blank.gif"
data-src="https://thumbs.example.test/aa/6/xv_18_t.jpg" data-idcdn="23" data-videoid="58556869" /><span class="top-right-tags"><span class="video-hd-mark">1080p</span></span></a></div></div><div class="thumb-under"><p class="title"><a href="/video.abcdef12345/58556869/0/sample_" title="Sample &amp; Title">Sample &amp; Title <span class="duration">13 min</span></a></p><p class="metadata"><span class="bg"><span class="duration">13 min</span></span></p></div></div>`

func TestXvideosCardParsingBuildsSourceIdentity(t *testing.T) {
	items := parseXvideosCards(xvideosCardFixture)
	if len(items) != 1 {
		t.Fatalf("unexpected card count: %d", len(items))
	}
	item := items[0]
	if item.ID != "xvideos:abcdef12345-58556869" || item.Source != sourceXvideos || item.SourceID != "abcdef12345-58556869" {
		t.Fatalf("unexpected card identity: %+v", item)
	}
	if item.Title != "Sample & Title" || item.Remark != "13 min 1080p" {
		t.Fatalf("unexpected card text: %+v", item)
	}
	if item.Cover != "https://thumbs.example.test/aa/6/xv_18_t.jpg" {
		t.Fatalf("unexpected card cover: %v", item.Cover)
	}
}

func TestXvideosDetailAndPlaybackResolve(t *testing.T) {
	page := `<html><head><meta property="og:image" content="https://thumbs.example.test/aa/6/xv_18_t.jpg" /><meta property="og:description" content="Sample description" /></head><body>
<h2 class="page-title">Sample Title <span class="duration">13 min</span><span class="video-hd-mark">1080p</span></h2>
<div class="video-metadata video-tags-list"><ul><li class="main-uploader"><a href="/channel" class="btn btn-default label main uploader-tag"><span class="name">Channel Name</span></a></li></ul></div>
<script>html5player.setVideoUrlHigh('https://mp4.example.test/aa/6/mp4_sd.mp4?secure=abc,1');
html5player.setVideoHLS('https://hls.example.test/token,1/aa/6/hls.m3u8');</script></body></html>`
	playlist := "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\nsegment.ts\n#EXT-X-ENDLIST\n"
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		if strings.Contains(request.URL.Path, "video.abcdef12345") {
			return sourceFixtureResponse(request, http.StatusOK, page), nil
		}
		if strings.HasSuffix(request.URL.Path, ".m3u8") {
			return sourceFixtureResponse(request, http.StatusOK, playlist), nil
		}
		return sourceFixtureResponse(request, http.StatusNotFound, ""), nil
	})
	drama, chapters, err := d.fetchXvideosDetail(context.Background(), "abcdef12345-58556869")
	if err != nil || len(chapters) != 1 {
		t.Fatalf("detail did not resolve: %+v %v", chapters, err)
	}
	if drama.Title != "Sample Title" || drama.Remark != "13 min" || drama.Cover != "https://thumbs.example.test/aa/6/xv_18_t.jpg" {
		t.Fatalf("unexpected detail fields: %+v", drama)
	}
	if len(drama.Tags) != 1 || drama.Tags[0] != "Channel Name" {
		t.Fatalf("unexpected detail tags: %v", drama.Tags)
	}
	if !strings.HasSuffix(chapters[0].VideoURL, "hls.m3u8") || chapters[0].Source != sourceXvideos {
		t.Fatalf("unexpected chapter: %+v", chapters[0])
	}
	task := Task{DramaID: drama.ID, Chapter: chapters[0]}
	media, err := d.resolveXvideosMedia(context.Background(), task)
	if err != nil || media.Playlist == "" || media.URL == "" {
		t.Fatalf("playback did not resolve through the playlist: %+v %v", media, err)
	}
}

func TestXvideosCategoryValidation(t *testing.T) {
	if !validXvideosCategory("") || !validXvideosCategory("AI-239") {
		t.Fatal("valid xvideos categories were rejected")
	}
	for _, value := range []string{"AI", "AI-", "../etc", "AI-239/x", strings.Repeat("a", 80) + "-1"} {
		if validXvideosCategory(value) {
			t.Fatalf("invalid xvideos category accepted: %s", value)
		}
	}
	if !validXvideosSourceID("abcdef12345-58556869") || validXvideosSourceID("abcdef12345") || validXvideosSourceID("ABCDEF12345-58556869") {
		t.Fatal("xvideos source id validation changed")
	}
}
