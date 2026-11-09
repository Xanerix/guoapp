package core

import (
	"context"
	"net/url"
	"sync"
	"testing"
)

func newLifecycleMedia() providerMedia {
	return providerMedia{URL: "https://media.test/lifecycle/index.m3u8", Quality: 1080}
}

func TestNativeStreamCloseReleasesListenerAndSessions(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	media := newLifecycleMedia()
	first, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoices(media, 0))
	if err != nil {
		t.Fatal(err)
	}
	stream := engine.stream
	if stream == nil || !stream.nativeAlive() {
		t.Fatal("stream server is not accepting connections")
	}
	if closed := stream.nativeClose(); closed != 1 {
		t.Fatalf("closed sessions = %d, want 1", closed)
	}
	if stream.nativeAlive() {
		t.Fatal("closed stream server still accepts connections")
	}
	// 关闭后再次打开必须换一个新监听，旧地址不能复用。
	second, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoices(media, 0))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.stream.server.Close()
	if engine.stream == stream {
		t.Fatal("closed stream server was reused")
	}
	firstURL, err := url.Parse(first.URL)
	if err != nil {
		t.Fatal(err)
	}
	secondURL, err := url.Parse(second.URL)
	if err != nil {
		t.Fatal(err)
	}
	if firstURL.Host == secondURL.Host {
		t.Fatalf("recreated stream kept the old listener address %s", firstURL.Host)
	}
	if _, ok := engine.playbacks[first.Session]; ok {
		t.Fatal("playback of the closed stream was not dropped")
	}
}

func TestNativeEnsureStreamRecreatesOnceUnderConcurrentOpens(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	media := newLifecycleMedia()
	if _, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoices(media, 0)); err != nil {
		t.Fatal(err)
	}
	stale := engine.stream
	// 模拟系统回收监听：Serve 已退出，地址还在但不再接受连接。
	stale.failed.Store(true)

	const workers = 6
	hosts := make([]string, workers)
	errs := make([]error, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			plan, err := engine.nativeOpenPlayback(context.Background(), nativePlaybackChoices(media, 0))
			errs[index] = err
			if err != nil {
				return
			}
			parsed, parseErr := url.Parse(plan.URL)
			if parseErr != nil {
				errs[index] = parseErr
				return
			}
			hosts[index] = parsed.Host
		}(index)
	}
	group.Wait()
	defer engine.stream.server.Close()
	for index, err := range errs {
		if err != nil {
			t.Fatalf("worker %d: %v", index, err)
		}
	}
	for index := 1; index < workers; index++ {
		if hosts[index] != hosts[0] {
			t.Fatalf("concurrent opens created %q and %q", hosts[0], hosts[index])
		}
	}
	if engine.stream == stale {
		t.Fatal("dead stream server was not replaced")
	}
	engine.mu.Lock()
	playbacks := len(engine.playbacks)
	engine.mu.Unlock()
	if playbacks != workers {
		t.Fatalf("playbacks = %d, want %d (stale entries left behind)", playbacks, workers)
	}
	engine.stream.mu.Lock()
	sessions := len(engine.stream.sessions)
	engine.stream.mu.Unlock()
	if sessions != workers {
		t.Fatalf("sessions = %d, want %d", sessions, workers)
	}
}
