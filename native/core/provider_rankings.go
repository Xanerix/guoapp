package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"
)

const rankingCacheTTL = 5 * time.Minute

type RankingBoard struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"-"`
	UpstreamKey string `json:"-"`
}

type RankingItem struct {
	Rank   int    `json:"rank"`
	Drama  Drama  `json:"drama"`
	Metric string `json:"metric,omitempty"`
}

type RankingPage struct {
	BoardID     string        `json:"boardId"`
	Page        int           `json:"page"`
	Items       []RankingItem `json:"items"`
	HasMore     bool          `json:"hasMore"`
	TotalPages  int           `json:"totalPages,omitempty"`
	UpdatedText string        `json:"updatedText,omitempty"`
	FetchedAt   time.Time     `json:"fetchedAt"`
	Stale       bool          `json:"stale,omitempty"`
	Warning     string        `json:"warning,omitempty"`
}

type rankingCall struct {
	done chan struct{}
	page RankingPage
	err  error
}

type rankingCache struct {
	mu      sync.Mutex
	pages   map[string]RankingPage
	pending map[string]*rankingCall
}

func allRankingBoards() []RankingBoard {
	var boards []RankingBoard
	for _, descriptor := range registeredDescriptors() {
		boards = append(boards, descriptor.Rankings...)
	}
	return boards
}

func findRankingBoard(id string) (RankingBoard, bool) {
	for _, board := range allRankingBoards() {
		if board.ID == id {
			return board, true
		}
	}
	return RankingBoard{}, false
}

func cloneRankingPage(page RankingPage) RankingPage {
	page.Items = append([]RankingItem{}, page.Items...)
	return page
}

func (d *Downloader) loadRankingPage(ctx context.Context, board RankingBoard, page int, refresh bool) (RankingPage, error) {
	key := board.ID + ":" + strconv.Itoa(page)
	cache := &d.rankings
	cache.mu.Lock()
	if cache.pages == nil {
		cache.pages = map[string]RankingPage{}
		cache.pending = map[string]*rankingCall{}
	}
	cached, exists := cache.pages[key]
	if exists && !refresh && time.Since(cached.FetchedAt) >= 0 && time.Since(cached.FetchedAt) < rankingCacheTTL {
		cache.mu.Unlock()
		return cloneRankingPage(cached), nil
	}
	if pending := cache.pending[key]; pending != nil {
		cache.mu.Unlock()
		select {
		case <-ctx.Done():
			return RankingPage{}, ctx.Err()
		case <-pending.done:
			if (errors.Is(pending.err, context.Canceled) || errors.Is(pending.err, context.DeadlineExceeded)) && ctx.Err() == nil {
				return d.loadRankingPage(ctx, board, page, refresh)
			}
			return cloneRankingPage(pending.page), pending.err
		}
	}
	pending := &rankingCall{done: make(chan struct{})}
	cache.pending[key] = pending
	cache.mu.Unlock()

	result, err := d.fetchRankingPage(ctx, board, page)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err == nil {
		result.BoardID, result.Page, result.FetchedAt = board.ID, page, time.Now()
		if refresh && page == 1 {
			for cachedKey, old := range cache.pages {
				if old.BoardID == board.ID {
					delete(cache.pages, cachedKey)
				}
			}
		}
		if len(cache.pages) >= 128 {
			oldestKey, oldest := "", time.Now()
			for cachedKey, old := range cache.pages {
				if old.FetchedAt.Before(oldest) {
					oldestKey, oldest = cachedKey, old.FetchedAt
				}
			}
			delete(cache.pages, oldestKey)
		}
		cache.pages[key] = cloneRankingPage(result)
		d.writeRankingCacheLocked()
	} else if exists && ctx.Err() == nil && time.Since(cached.FetchedAt) >= 0 && time.Since(cached.FetchedAt) < 24*time.Hour {
		result = cloneRankingPage(cached)
		result.Stale = true
		result.Warning = "榜单刷新失败（" + PublicError(err).Error() + "），显示上次取得的榜单。"
		err = nil
	}
	pending.page, pending.err = cloneRankingPage(result), err
	delete(cache.pending, key)
	close(pending.done)
	return result, err
}

func (d *Downloader) fetchRankingPage(ctx context.Context, board RankingBoard, page int) (RankingPage, error) {
	if p, ok := d.provider(board.Source); ok {
		if ranker, ok := p.(Ranker); ok {
			return ranker.RankingPage(ctx, board, page)
		}
	}
	return RankingPage{}, fmt.Errorf("不支持的榜单站源 %s", board.Source)
}
