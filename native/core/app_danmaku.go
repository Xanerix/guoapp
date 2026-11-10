package core

import (
	"context"
	"errors"
	"time"
)

func (engine *nativeEngine) nativeDanmaku(ctx context.Context, input nativeInput) (DanmakuPage, error) {
	if input.StartMS < 0 || input.DurationMS <= input.StartMS || input.DurationMS > DanmakuMaxDurationMS {
		return DanmakuPage{}, errors.New("弹幕时间范围无效")
	}
	engine.mu.Lock()
	choice, exists := engine.playbacks[input.PlaybackSession]
	engine.mu.Unlock()
	if !exists || time.Since(choice.created) > 12*time.Hour {
		return DanmakuPage{}, errors.New("播放会话已过期")
	}
	if choice.danmakuSeries == "" || choice.danmakuVideo == "" {
		return DanmakuPage{}, errors.New("本集暂不支持弹幕")
	}
	p, ok := engine.downloader.provider(choice.danmakuSource)
	if !ok {
		return DanmakuPage{}, errors.New("本集暂不支持弹幕")
	}
	danmaku, ok := p.(DanmakuProvider)
	if !ok {
		return DanmakuPage{}, errors.New("本集暂不支持弹幕")
	}
	page, err := danmaku.Danmaku(ctx, choice.danmakuSeries, choice.danmakuVideo, input.StartMS, input.DurationMS)
	if err != nil {
		return DanmakuPage{}, err
	}
	engine.mu.Lock()
	_, exists = engine.playbacks[input.PlaybackSession]
	engine.mu.Unlock()
	if !exists {
		return DanmakuPage{}, errors.New("播放会话已过期")
	}
	return page, ctx.Err()
}

const DanmakuMaxDurationMS = 24 * 60 * 60 * 1000
