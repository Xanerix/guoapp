package core

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestPlaybackReopensUnreachableLoopback(t *testing.T) {
	ctx := context.Background()
	engine := &nativeEngine{downloader: &Downloader{}}
	stream, err := engine.streamServer()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.server.Close() })

	choice := nativePlaybackChoices(ProviderMedia{
		URL:      "https://media.example/index.m3u8",
		Playlist: "#EXTM3U\n#EXTINF:4,\nsegment.ts\n#EXT-X-ENDLIST",
	}, 0)
	before, err := engine.nativeOpenPlayback(ctx, choice)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.nativeReleasePlayback(before.Session) })
	if err := stream.server.Close(); err != nil {
		t.Fatal(err)
	}

	after, err := engine.nativeOpenPlayback(ctx, choice)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.nativeReleasePlayback(after.Session) })
	oldURL, _ := url.Parse(before.URL)
	newURL, _ := url.Parse(after.URL)
	if oldURL.Host == newURL.Host {
		t.Fatalf("播放继续使用失效端口 %s", oldURL.Host)
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(after.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("新监听返回 HTTP %d", response.StatusCode)
	}
}
