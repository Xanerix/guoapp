package core

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 季列表只做"同站源"的聚合，命中不确定时宁可不显示。
// 有官方季字段的站源（红果）直接按官方分组，其余站源沿用标题里的 第N季/第N部。
const (
	hongguoSeasonLookupTimeout = 10 * time.Second
	hongguoSeasonDateTimeout   = 2500 * time.Millisecond
	hongguoSeasonDateTTL       = 24 * time.Hour
	hongguoSeasonEnrichLimit   = 6
	hongguoSeasonEnrichWorkers = 4
	// 官方季分组：同 related_album_id 才是同一部剧的不同季。
	// 红果单通道限流是「并发 1、间隔 250ms」，20 个候选串行要 5 秒，
	// 这里用多条独立通道并行校验，单次详情结果缓存 6 小时。
	hongguoSeasonGroupTimeout = 4 * time.Second
	hongguoSeasonGroupLanes   = 3
	hongguoSeasonGroupLimit   = 24
	hongguoSeasonGroupTTL     = 6 * time.Hour
)

// seasonGroupEntry 缓存某部剧的官方季分组，避免每次打开详情都重新校验。
type seasonGroupEntry struct {
	album   string
	index   int
	drama   Drama
	fetched time.Time
}

// seasonAdapter 把「搜索同源标题」「取详情上线日期」「取官方季分组」抽象出来，
// 红果沿用原有实现，其它有搜索能力的站源各接一层。
type seasonAdapter struct {
	search func(ctx context.Context, keyword string) ([]Drama, error)
	date   func(ctx context.Context, sourceID string) string
	// group 返回官方的「同系列分组 ID + 季号 + 详情元数据」，
	// 只有提供该字段的站源才会填充。
	group func(ctx context.Context, sourceID string) (string, int, Drama, bool)
}

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
	adapter, supported := engine.nativeSeasonAdapter(source)
	if !supported {
		// 没有搜索能力的站源无法召回其它季，维持单季体验。
		return nativeSeasonResult{}, nil
	}
	base, current, unit := hongguoSeasonLookup(input.Drama.Title)
	if current == 0 || base == "" {
		return nativeSeasonResult{}, nil
	}
	// 季列表在详情页打开时同步等待，走前台通道才能并行；
	// 后台通道被限流器串行化，20 个候选会拖到 5 秒以上。
	lookup, cancel := context.WithTimeout(ctx, hongguoSeasonLookupTimeout)
	defer cancel()
	names, err := adapter.search(lookup, base)
	result := nativeSeasonResult{}
	if err != nil {
		// 季列表属于增强信息，失败不影响详情页本身。
		result.Warning = "季列表暂时不可用"
	}
	// 详情自带官方季字段时以官方为准，避免标题/日期启发式误判同名不同剧。
	if adapter.group != nil {
		if items, official := engine.officialSeasons(lookup, names, input.Drama, adapter.group); official {
			if len(items) > 1 {
				result.Items = items
			}
			return result, nil
		}
	}
	key := hongguoSearchText(base)
	if len(hongguoSeasonList(names, current, unit, key, input.Drama)) <= 1 {
		// 带标点的标题可能搜不到结果，再用去掉标点的关键词补一次。
		cleaned := hongguoSearchText(base)
		if cleaned != "" && cleaned != strings.TrimSpace(base) {
			if extra, extraErr := adapter.search(lookup, cleaned); extraErr == nil {
				names = append(names, extra...)
			}
		}
	}
	// 搜索结果常常缺上线日期，缺日期就没法判断同名不同剧；
	// 这里并发补查详情日期，并用缓存避免重复请求。
	engine.enrichSeasonDates(lookup, source, names, adapter.date)
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

// hongguoSeasonGroup 读取 App 详情里的官方季字段：
// related_album_id 标识同一部剧，season_index 是官方季号。
// 同时返回详情里的剧集元数据，季卡片据此显示上线时间和集数。
func (engine *nativeEngine) hongguoSeasonGroup(ctx context.Context, sourceID string) (string, int, Drama, bool) {
	if !hongguoNumericID.MatchString(sourceID) {
		return "", 0, Drama{}, false
	}
	engine.seasonDateMu.Lock()
	cached, found := engine.seasonGroups[sourceID]
	engine.seasonDateMu.Unlock()
	if found && time.Since(cached.fetched) < hongguoSeasonGroupTTL {
		return cached.album, cached.index, cached.drama, true
	}
	entry, err := engine.downloader.hongguoAppDetail(ctx, sourceID)
	if err != nil || entry.AlbumID == "" || entry.SeasonIndex < 1 {
		return "", 0, Drama{}, false
	}
	engine.seasonDateMu.Lock()
	if engine.seasonGroups == nil {
		engine.seasonGroups = map[string]seasonGroupEntry{}
	}
	engine.seasonGroups[sourceID] = seasonGroupEntry{album: entry.AlbumID, index: entry.SeasonIndex, drama: entry.Drama, fetched: time.Now()}
	engine.seasonDateMu.Unlock()
	return entry.AlbumID, entry.SeasonIndex, entry.Drama, true
}

// officialSeasons 用官方季字段聚合：只有 related_album_id 相同的剧才算同一部剧，
// 季号取官方 season_index。返回 ok=false 表示当前剧拿不到官方字段，需要回退启发式。
func (engine *nativeEngine) officialSeasons(
	ctx context.Context,
	dramas []Drama,
	current nativeDrama,
	resolve func(context.Context, string) (string, int, Drama, bool),
) ([]nativeSeason, bool) {
	album, currentNumber, detail, ok := resolve(ctx, current.SourceID)
	if !ok || album == "" || currentNumber < 1 {
		return nil, false
	}
	// 详情里的上线时间/集数比列表数据更可靠，用来补齐当前这一季。
	if current.OnlineDate == "" {
		current.OnlineDate = providerReleaseDate(detail.OnlineDate)
	}
	if current.Episodes == 0 {
		current.Episodes = nativeNormalize(detail).Episodes
	}
	seen := map[string]bool{current.SourceID: true}
	var candidates []Drama
	for _, drama := range dramas {
		if drama.SourceID == "" || seen[drama.SourceID] {
			continue
		}
		if len(candidates) >= hongguoSeasonGroupLimit {
			break
		}
		seen[drama.SourceID] = true
		candidates = append(candidates, drama)
	}
	byNumber := map[int]nativeDrama{currentNumber: current}
	var mu sync.Mutex
	jobs := make(chan Drama)
	var group sync.WaitGroup
	for lane := 0; lane < hongguoSeasonGroupLanes; lane++ {
		group.Add(1)
		go func(lane int) {
			defer group.Done()
			// 每条通道有自己的限流闸门，才能在 250ms 间隔下真正并行。
			laneCtx := context.WithValue(ctx, requestLaneKey{}, "season-"+strconv.Itoa(lane))
			for drama := range jobs {
				itemCtx, cancel := context.WithTimeout(laneCtx, hongguoSeasonGroupTimeout)
				itemAlbum, number, _, found := resolve(itemCtx, drama.SourceID)
				cancel()
				if !found || itemAlbum != album || number < 1 || number > hongguoSearchSeasonLimit {
					continue
				}
				mu.Lock()
				if _, exists := byNumber[number]; !exists {
					byNumber[number] = nativeNormalize(drama)
				}
				mu.Unlock()
			}
		}(lane)
	}
	for _, drama := range candidates {
		jobs <- drama
	}
	close(jobs)
	group.Wait()
	numbers := make([]int, 0, len(byNumber))
	for number := range byNumber {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	items := make([]nativeSeason, 0, len(numbers))
	for _, number := range numbers {
		drama := byNumber[number]
		items = append(items, nativeSeason{
			Number:   number,
			Unit:     "季",
			Title:    strings.TrimSpace(drama.Title),
			Episodes: drama.Episodes,
			Current:  number == currentNumber,
			Drama:    drama,
		})
	}
	return items, true
}

// seasonAdapter 把「搜索同源标题」与「取详情上线日期」抽象出来，
// 红果沿用原有实现，其它有搜索能力的站源各接一层。
func (engine *nativeEngine) nativeSeasonAdapter(source string) (seasonAdapter, bool) {
	catalogSearch := func(fetch func(context.Context, int, string, string) ([]Drama, bool, error)) func(context.Context, string) ([]Drama, error) {
		return func(ctx context.Context, keyword string) ([]Drama, error) {
			items, _, err := fetch(ctx, 1, "", keyword)
			return items, err
		}
	}
	detailDate := func(fetch func(context.Context, string) (Drama, []Chapter, error)) func(context.Context, string) string {
		return func(ctx context.Context, sourceID string) string {
			raw, _, err := fetch(ctx, sourceID)
			if err != nil {
				return ""
			}
			return providerReleaseDate(raw.OnlineDate)
		}
	}
	switch source {
	case sourceHongguo:
		return seasonAdapter{
			search: engine.downloader.fetchHongguoSearchNames,
			date:   engine.hongguoSeasonOnlineDate,
			group:  engine.hongguoSeasonGroup,
		}, true
	case sourceHuangju:
		return seasonAdapter{
			search: catalogSearch(engine.downloader.fetchHuangjuCatalogPage),
			date:   detailDate(engine.downloader.fetchHuangjuDetail),
		}, true
	case sourceSorani:
		return seasonAdapter{
			search: catalogSearch(engine.downloader.fetchSoraniCatalogPage),
			date:   detailDate(engine.downloader.fetchSoraniDetail),
		}, true
	case sourceGuipian:
		return seasonAdapter{
			search: catalogSearch(engine.downloader.fetchGuipianCatalogPage),
			date:   detailDate(engine.downloader.fetchGuipianDetail),
		}, true
	case sourceHanxiaoquan:
		return seasonAdapter{
			search: func(ctx context.Context, keyword string) ([]Drama, error) {
				items, _, err := engine.downloader.searchHanxiaoquan(ctx, 1, keyword)
				return items, err
			},
			date: detailDate(engine.downloader.fetchHanxiaoquanDetail),
		}, true
	case sourceHuangguoAI:
		return seasonAdapter{
			search: func(ctx context.Context, keyword string) ([]Drama, error) {
				items, _, err := engine.downloader.searchHuangguoAI(ctx, 1, keyword)
				return items, err
			},
			date: detailDate(engine.downloader.fetchHuangguoAIDetail),
		}, true
	}
	return seasonAdapter{}, false
}

// enrichSeasonDates 并发补齐缺失的上线日期，并写入缓存。
func (engine *nativeEngine) enrichSeasonDates(
	ctx context.Context,
	source string,
	dramas []Drama,
	date func(context.Context, string) string,
) {
	var pending []int
	for index, drama := range dramas {
		if _, dated := hongguoSeasonDate(providerReleaseDate(drama.OnlineDate)); dated {
			continue
		}
		cacheKey := source + ":" + drama.SourceID
		if cached, ok := engine.cachedSeasonDate(cacheKey); ok {
			dramas[index].OnlineDate = cached
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
			found := date(itemCtx, dramas[index].SourceID)
			if found == "" {
				return
			}
			dramas[index].OnlineDate = found
			engine.rememberSeasonDate(source+":"+dramas[index].SourceID, found)
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
