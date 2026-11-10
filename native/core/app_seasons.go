package core

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// 季列表只做"同站源、标题严格匹配"的聚合，命中不确定时宁可不显示。
// 目前只有红果具备可用的季信息（标题里的 第N季/第N部）。
const (
	hongguoSeasonLookupTimeout = 10 * time.Second
	hongguoSeasonDateTimeout   = 2500 * time.Millisecond
	hongguoSeasonDateTTL       = 24 * time.Hour
	hongguoSeasonEnrichLimit   = 6
	hongguoSeasonEnrichWorkers = 4
)

// seasonDateEntry 缓存某部剧的上线日期，避免每次打开详情页都补查。
type seasonDateEntry struct {
	date    string
	fetched time.Time
}

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

// hongguoSeasonLookup 解析标题对应的季度信息。
// 没有季后缀时视为第 1 季，这样「X」与「X第二季」也能归到一起。
func hongguoSeasonLookup(title string) (string, int, string) {
	if base, current, unit, _ := hongguoSearchSeason(title); current > 0 {
		return base, current, unit
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return "", 0, ""
	}
	return title, 1, "季"
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
	base, current, unit := hongguoSeasonLookup(input.Drama.Title)
	if current == 0 || base == "" {
		return nativeSeasonResult{}, nil
	}
	lookup, cancel := context.WithTimeout(context.WithValue(ctx, backgroundCatalogKey{}, true), hongguoSeasonLookupTimeout)
	defer cancel()
	names, err := engine.downloader.fetchHongguoSearchNames(lookup, base)
	result := nativeSeasonResult{}
	if err != nil {
		// 季列表属于增强信息，失败不影响详情页本身。
		result.Warning = "季列表暂时不可用"
	}
	key := hongguoSearchText(base)
	if len(hongguoSeasonList(names, current, unit, key, input.Drama)) <= 1 {
		// 带标点的标题可能搜不到结果，再用去掉标点的关键词补一次。
		cleaned := hongguoSearchText(base)
		if cleaned != "" && cleaned != strings.TrimSpace(base) {
			if extra, extraErr := engine.downloader.fetchHongguoSearchNames(lookup, cleaned); extraErr == nil {
				names = append(names, extra...)
			}
		}
	}
	// 搜索结果常常缺上线日期，缺日期就没法判断同名不同剧；
	// 这里并发补查详情日期，并用缓存避免重复请求。
	engine.enrichSeasonDates(lookup, names)
	result.Items = hongguoSeasonList(names, current, unit, key, input.Drama)
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
	entries := make([]entry, 0, len(byNumber))
	for _, item := range byNumber {
		entries = append(entries, item)
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].number < entries[right].number })
	// 同名不同剧的典型特征是「高季号反而上线更早」，用上线日期单调性剔除；
	// 当前打开的季永远保留，避免把用户正在看的那一条过滤掉。
	var latest time.Time
	kept := entries[:0]
	for _, item := range entries {
		date, dated := hongguoSeasonDate(item.drama.OnlineDate)
		if item.number != current {
			if dated {
				if !latest.IsZero() && date.Add(hongguoSeasonDateTolerance).Before(latest) {
					continue
				}
			} else if !hongguoSeasonEpisodesPlausible(currentDrama.Episodes, item.drama.Episodes) {
				// 没有上线日期时的兜底：集数相差三倍以上，判为同名不同剧。
				continue
			}
		}
		kept = append(kept, item)
		if dated && date.After(latest) {
			latest = date
		}
	}
	items := make([]nativeSeason, 0, len(kept))
	for _, item := range kept {
		items = append(items, nativeSeason{
			Number:   item.number,
			Unit:     unit,
			Title:    strings.TrimSpace(item.drama.Title),
			Episodes: item.drama.Episodes,
			Current:  item.number == current,
			Drama:    item.drama,
		})
	}
	return items
}

// hongguoSeasonDateTolerance 允许相邻季的上线日期有小幅倒挂（重上架等情况），
// 只有明显早于前一季（超过一个月）才判定为同名不同剧。
const hongguoSeasonDateTolerance = 30 * 24 * time.Hour

func hongguoSeasonDate(value string) (time.Time, bool) {
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// hongguoSeasonEpisodesPlausible 在没有上线日期时用集数粗筛：
// 同一部剧各季集数会有波动，但不会相差三倍以上。
func hongguoSeasonEpisodesPlausible(current, candidate int) bool {
	if current <= 0 || candidate <= 0 {
		return true
	}
	return candidate <= current*3 && candidate*3 >= current
}

// hongguoSeasonOnlineDate 只取详情页的上线日期，用于补齐搜索结果缺失的日期。
func (engine *nativeEngine) hongguoSeasonOnlineDate(ctx context.Context, sourceID string) string {
	if !hongguoNumericID.MatchString(sourceID) {
		return ""
	}
	body, err := engine.downloader.fetchProviderText(ctx, hongguoBaseURL+"/detail?series_id="+url.QueryEscape(sourceID), hongguoBaseURL+"/")
	if err != nil {
		return ""
	}
	raw, err := parseHongguoSortDetail(body, sourceID)
	if err != nil {
		return ""
	}
	return providerReleaseDate(raw.OnlineDate)
}

// enrichSeasonDates 并发补齐缺失的上线日期，并写入缓存。
func (engine *nativeEngine) enrichSeasonDates(ctx context.Context, dramas []Drama) {
	var pending []int
	for index, drama := range dramas {
		if _, dated := hongguoSeasonDate(providerReleaseDate(drama.OnlineDate)); dated {
			continue
		}
		if date, ok := engine.cachedSeasonDate(drama.SourceID); ok {
			dramas[index].OnlineDate = date
			continue
		}
		if len(pending) >= hongguoSeasonEnrichLimit {
			continue
		}
		pending = append(pending, index)
	}
	if len(pending) == 0 {
		return
	}
	limit := make(chan struct{}, hongguoSeasonEnrichWorkers)
	var group sync.WaitGroup
	for _, index := range pending {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			itemCtx, cancel := context.WithTimeout(ctx, hongguoSeasonDateTimeout)
			defer cancel()
			date := engine.hongguoSeasonOnlineDate(itemCtx, dramas[index].SourceID)
			if date == "" {
				return
			}
			dramas[index].OnlineDate = date
			engine.rememberSeasonDate(dramas[index].SourceID, date)
		}(index)
	}
	group.Wait()
}

func (engine *nativeEngine) cachedSeasonDate(sourceID string) (string, bool) {
	engine.seasonDateMu.Lock()
	defer engine.seasonDateMu.Unlock()
	entry, ok := engine.seasonDates[sourceID]
	if !ok || time.Since(entry.fetched) > hongguoSeasonDateTTL {
		return "", false
	}
	return entry.date, true
}

func (engine *nativeEngine) rememberSeasonDate(sourceID, date string) {
	engine.seasonDateMu.Lock()
	defer engine.seasonDateMu.Unlock()
	if engine.seasonDates == nil {
		engine.seasonDates = map[string]seasonDateEntry{}
	}
	engine.seasonDates[sourceID] = seasonDateEntry{date: date, fetched: time.Now()}
}
