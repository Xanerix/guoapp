package core

import (
	"context"
	"strconv"
	"time"
)

func (engine *nativeEngine) nativeBeginPlayback(ctx context.Context, sequence int64) (context.Context, func(), error) {
	engine.playbackMu.Lock()
	defer engine.playbackMu.Unlock()
	if sequence <= engine.playbackSequence {
		return nil, nil, context.Canceled
	}
	if engine.playbackCancel != nil {
		engine.playbackCancel()
	}
	engine.playbackSequence = sequence
	playback, cancel := context.WithCancel(ctx)
	engine.playbackCancel = cancel
	finish := func() {
		cancel()
		engine.playbackMu.Lock()
		if engine.playbackSequence == sequence {
			engine.playbackCancel = nil
		}
		engine.playbackMu.Unlock()
	}
	return playback, finish, nil
}

func (engine *nativeEngine) nativeCancelPlayback(sequence int64) {
	engine.playbackMu.Lock()
	defer engine.playbackMu.Unlock()
	if sequence <= engine.playbackSequence {
		return
	}
	engine.playbackSequence = sequence
	if engine.playbackCancel != nil {
		engine.playbackCancel()
		engine.playbackCancel = nil
	}
}

// nativePlaybackAlive 报告本机取流服务是否仍可接受连接。
// 应用被系统挂起后监听套接字会失效，此时旧播放器即使还停在画面上，
// 也无法再打开任何新媒体，只能重建。
func (engine *nativeEngine) nativePlaybackAlive() bool {
	engine.mu.Lock()
	stream := engine.stream
	engine.mu.Unlock()
	return stream != nil && stream.nativeAlive()
}

// nativeEnsureStreamLocked 确认取流监听可用，必要时关闭旧监听并重建。
// 调用方必须持有 engine.streamMu，避免并发探活/关闭/创建出重复的 listener。
func (engine *nativeEngine) nativeEnsureStreamLocked() (*nativeStreamServer, error) {
	engine.mu.Lock()
	current := engine.stream
	engine.mu.Unlock()
	if current != nil {
		if current.nativeAlive() {
			return current, nil
		}
		// 一次探活失败不直接判死，稍后再确认，避免误杀仍在恢复中的服务。
		time.Sleep(120 * time.Millisecond)
		if current.nativeAlive() {
			return current, nil
		}
		closed := current.nativeClose()
		engine.mu.Lock()
		if engine.stream == current {
			engine.stream = nil
			// 旧会话随监听一起作废，指向它们的播放记录必须清掉。
			engine.playbacks = map[string]nativePlaybackChoice{}
		}
		engine.mu.Unlock()
		stream, err := newNativeStreamServer(engine.downloader)
		if err != nil {
			return nil, err
		}
		engine.mu.Lock()
		engine.stream = stream
		engine.mu.Unlock()
		stream.nativeLog(nativeStreamDiagnostic{}, "listener_recreated", "", "closed_sessions="+strconv.Itoa(closed), 0, "")
		return stream, nil
	}
	stream, err := newNativeStreamServer(engine.downloader)
	if err != nil {
		return nil, err
	}
	engine.mu.Lock()
	engine.stream = stream
	engine.mu.Unlock()
	return stream, nil
}
