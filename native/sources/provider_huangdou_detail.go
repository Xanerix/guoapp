package sources

import (
	"context"
	"errors"
	"strings"
	"time"

	"duanjuapp/native/core"
)

type huangdouDetailEntry struct {
	row       map[string]any
	expiresAt time.Time
}

type huangdouDetailCall struct {
	done chan struct{}
	row  map[string]any
	err  error
}

func (p *huangdouProvider) huangdouDetail(ctx context.Context, id string) (map[string]any, error) {
	if !rankingSourceID.MatchString(id) {
		return nil, errors.New("无效的黄豆剧集 ID")
	}
	p.detailMu.Lock()
	if cached, found := p.details[id]; found && time.Now().Before(cached.expiresAt) {
		p.detailMu.Unlock()
		return cached.row, nil
	}
	if pending := p.detailPending[id]; pending != nil {
		p.detailMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending.done:
			if (errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded)) && ctx.Err() == nil {
				return p.huangdouDetail(ctx, id)
			}
			return pending.row, pending.err
		}
	}
	if p.detailPending == nil {
		p.detailPending = make(map[string]*huangdouDetailCall)
	}
	call := &huangdouDetailCall{done: make(chan struct{})}
	p.detailPending[id] = call
	p.detailMu.Unlock()
	var decoded any
	err := newHuangdouAPIClient(p).call(ctx, "/drama/detail", map[string]any{"id": id}, &decoded)
	row := huangdouDataMap(decoded)
	if err == nil && strings.TrimPrefix(core.MapString(row, "id", "drama_id"), "rp_") != id {
		row = nil
		err = errors.New("黄豆详情返回了其他剧集")
	}
	p.detailMu.Lock()
	if err == nil {
		if p.details == nil || len(p.details) >= 128 {
			p.details = make(map[string]huangdouDetailEntry)
		}
		p.details[id] = huangdouDetailEntry{row: row, expiresAt: time.Now().Add(core.DramaRefreshRetryDelay)}
	}
	call.row, call.err = row, err
	delete(p.detailPending, id)
	close(call.done)
	p.detailMu.Unlock()
	return row, err
}
