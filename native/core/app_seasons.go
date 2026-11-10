package core

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

// 季列表只做"同站源、标题严格匹配"的聚合，命中不确定时宁可不显示。
// 目前只有红果具备可用的季信息（标题里的 第N季/第N部）。
const hongguoSeasonLookupTimeout = 5 * time.Second

type nativeSeason struct {
	Number   int         `json:"number"`
	Unit     string      `json:"unit,omitempty"`
	Title    string      `json:"title"`
	Episodes int         `json:"episodes"`
	Current  bool        `json:"current,omitempty"`
	Drama    nativeDrama `json:"drama"`
}

type nativeSeasonResult struct {
	Items   []nativeSeason `json:"items"`
	Warning string         `json:"warning,omitempty"`
}

func (engine *nativeEngine) nativeSeasons(ctx context.Context, input nativeInput) (nativeSeasonResult, error) {
	source, _, valid := splitProviderDramaID(input.Drama.ID)
	if !valid {
		return nativeSeasonResult{}, errors.New("剧集信息无效")
	}
	// 其它站源还没有可靠的季信息，留空让 UI 维持单季体验。
	if source != sourceHongguo {
		return nativeSeasonResult{}, nil
	}
	base, current, unit, _ := hongguoSearchSeason(input.Drama.Title)
	if current == 0 || base == "" {
		return nativeSeasonResult{}, nil
	}
	lookup, cancel := context.WithTimeout(ctx, hongguoSeasonLookupTimeout)
	defer cancel()
	names, err := engine.downloader.fetchHongguoSearchNames(lookup, base)
	result := nativeSeasonResult{}
	if err != nil {
		// 季列表属于增强信息，失败不影响详情页本身。
		result.Warning = "季列表暂时不可用"
	}
	result.Items = hongguoSeasonList(names, current, unit, hongguoSearchText(base), input.Drama)
	if len(result.Items) <= 1 {
		result.Items = nil
	}
	return result, nil
}

// hongguoSeasonList 按季号归并同一部剧，标题必须严格相等（不做包含匹配）。
func hongguoSeasonList(dramas []Drama, current int, unit, key string, currentDrama nativeDrama) []nativeSeason {
	type entry struct {
		number int
		drama  nativeDrama
	}
	byNumber := map[int]entry{}
	for _, drama := range dramas {
		title := drama.DisplayTitle()
		base, number, dramaUnit, _ := hongguoSearchSeason(title)
		if number == 0 {
			// 没有季后缀的只能是第 1 季，且标题要完全一致。
			if hongguoSearchText(title) != key {
				continue
			}
			number = 1
		} else if dramaUnit != unit || hongguoSearchText(base) != key {
			continue
		}
		if number < 1 || number > hongguoSearchSeasonLimit {
			continue
		}
		item := nativeNormalize(drama)
		if previous, exists := byNumber[number]; exists && previous.drama.ID != currentDrama.ID {
			// 同一季号命中多部剧：不合并，保留先出现的。
			continue
		}
		byNumber[number] = entry{number: number, drama: item}
	}
	// 以用户当前打开的这一季为准，避免同名剧抢占当前季。
	byNumber[current] = entry{number: current, drama: currentDrama}
	items := make([]nativeSeason, 0, len(byNumber))
	for _, item := range byNumber {
		items = append(items, nativeSeason{
			Number:   item.number,
			Unit:     unit,
			Title:    strings.TrimSpace(item.drama.Title),
			Episodes: item.drama.Episodes,
			Current:  item.number == current,
			Drama:    item.drama,
		})
	}
	sort.Slice(items, func(left, right int) bool { return items[left].Number < items[right].Number })
	return items
}
