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
	source, sourceID, valid := splitProviderDramaID(input.Drama.ID)
	if !valid {
		return nativeSeasonResult{}, errors.New("剧集信息无效")
	}

	// 红果：优先用官方多季接口 plan/v，直接返回完整季列表。
	// 不依赖搜索是否召回了全部季。
	if source == sourceHongguo && hongguoNumericID.MatchString(sourceID) {
		result, ok := engine.hongguoOfficialSeasons(ctx, sourceID, input.Drama)
		if ok {
			return result, nil
		}
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
		if items, official := engine.officialSeasons(lookup, names, input.Drama, adapter.group, adapter.search); official {
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
	// 跨系列组补齐：聚合出的其它季若属于另一个官方系列组，用它的
	// series_id 再查一次 plan/v，把同组的待上线季带出来——红果常把首季
	// 拆成独立系列组，从首季进去会缺后续季（尤其待上线季）的入口。
	result.Items = engine.hongguoExpandPlanSeasons(lookup, result.Items)
	if len(result.Items) <= 1 {
		result.Items = nil
	}
	return result, nil
}

// hongguoExpandPlanSeasons 用聚合结果里某个非当前季的 series_id 再查一次
// plan/v，补齐同一官方系列组内的其它季（含待上线季）。只查一次，避免把
// 季列表变成串行网络请求。标题 base 不一致的季直接丢弃，防止同 IP 污染。
func (engine *nativeEngine) hongguoExpandPlanSeasons(ctx context.Context, items []nativeSeason) []nativeSeason {
	if len(items) <= 1 {
		return items
	}
	seen := make(map[int]bool, len(items))
	for _, item := range items {
		seen[item.Number] = true
	}
	for _, item := range items {
		if item.Current || item.Drama.SourceID == "" {
			continue
		}
		base, _, _ := hongguoSeasonLookup(item.Title)
		seasons, ok := engine.downloader.hongguoPlanSeasons(ctx, item.Drama.SourceID)
		if !ok {
			return items
		}
		for _, season := range seasons {
			if seen[season.SeasonIndex] {
				continue
			}
			seasonBase, _, _ := hongguoSeasonLookup(season.Title)
			if base != "" && seasonBase != "" && seasonBase != base {
				continue
			}
			seen[season.SeasonIndex] = true
			items = append(items, nativeSeason{
				Number:   season.SeasonIndex,
				Unit:     firstNonEmpty(item.Unit, "季"),
				Title:    season.Title,
				Episodes: season.Episodes,
				Drama: nativeDrama{
					ID:         providerDramaID(sourceHongguo, season.SeriesID),
					Source:     sourceHongguo,
					SourceID:   season.SeriesID,
					Title:      season.Title,
					Episodes:   season.Episodes,
					Cover:      season.Cover,
					OnlineDate: season.OnlineDate,
				},
			})
		}
		break
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Number < items[j].Number
	})
	return items
}

// hongguoOfficialSeasons 用红果官方 plan/v 接口获取完整季列表。
// 成功（至少 1 项）直接返回；失败 ok=false 让调用方 fallback。
func (engine *nativeEngine) hongguoOfficialSeasons(
	ctx context.Context,
	sourceID string,
	current nativeDrama,
) (nativeSeasonResult, bool) {
	seasons, ok := engine.downloader.hongguoPlanSeasons(ctx, sourceID)
	if !ok || len(seasons) == 0 {
		return nativeSeasonResult{}, false
	}
	// plan/v 会基于 same_ip_series 返回同 IP 但不同作者/不同故事的剧。
	// 用标题 base 匹配过滤——只有 base title 和当前剧一致的才留。
	currentBase, _, _ := hongguoSeasonLookup(current.Title)
	if currentBase != "" {
		filtered := seasons[:0]
		for _, s := range seasons {
			sBase, _, _ := hongguoSeasonLookup(s.Title)
			// 当前季用 seriesID 确认（避免 title 解析异常误过滤）
			if s.SeriesID == sourceID || (sBase != "" && sBase == currentBase) {
				filtered = append(filtered, s)
			}
		}
		seasons = filtered
		if len(seasons) == 0 {
			return nativeSeasonResult{}, false
		}
	}

	currentIndex := current.SeasonIndex
	// 如果 plan/v 没返回当前这一季，把当前季补进去
	hasCurrent := false
	for _, s := range seasons {
		if s.SeriesID == sourceID {
			hasCurrent = true
			if currentIndex == 0 {
				currentIndex = s.SeasonIndex
			}
			break
		}
	}
	if !hasCurrent {
		// 手动补一个
		seasons = append(seasons, hongguoPlanSeason{
			SeriesID:    sourceID,
			Title:       strings.TrimSpace(current.Title),
			SeasonIndex: currentIndex,
			Episodes:    current.Episodes,
		})
		// 重新排序
		sort.Slice(seasons, func(i, j int) bool {
			return seasons[i].SeasonIndex < seasons[j].SeasonIndex
		})
	}

	var items []nativeSeason
	for _, s := range seasons {
		drama := nativeDrama{
			ID:         providerDramaID(sourceHongguo, s.SeriesID),
			Source:     sourceHongguo,
			SourceID:   s.SeriesID,
			Title:      s.Title,
			Episodes:   s.Episodes,
			Cover:      s.Cover,
			OnlineDate: s.OnlineDate,
		}
		items = append(items, nativeSeason{
			Number:   s.SeasonIndex,
			Unit:     "季",
			Title:    s.Title,
			Episodes: s.Episodes,
			Current:  s.SeriesID == sourceID,
			Drama:    drama,
		})
	}
	if len(items) <= 1 {
		// plan/v 只返回当前这一季：红果后台常把同一部剧拆成多个系列组
		// （首季上架时还没建系列，后续季单独成组），于是从第一季点进去
		// 看不到第二季，却能从第二季看到第三季。
		// 这里返回 false 让调用方 fallback 到搜索聚合，由 officialSeasons
		// 用标题严格匹配 + 上线时间单调性把跨组的季并回来；
		// 若确实只有一季，聚合结果仍是 1 项，行为与之前一致。
		return nativeSeasonResult{}, false
	}
	return nativeSeasonResult{Items: items}, true
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

// officialSeasons 用官方季字段聚合：同 related_album_id 一定并入，季号取官方 season_index。
// 官方偶尔把一部剧拆成多个系列组（如「持械入宋」的第二、五季），
// 这时只要标题严格同类、日期或集数也说得通，就并入同一部剧。
//
// 热路径：搜索 API 返回的 Drama 自带 AlbumID + SeasonIndex 时，
// 本函数完全不走网络，纯内存 <10ms；只有缺少字段的候选才 fallback 调详情。
//
// search 用于季号缺口补查：搜索联想用剧名做关键词时召回不全
// （如「持械入宋」召回 11 条缺正确的第二季，蹭名剧反而抢占季号槽位），
// 检测到 [1, maxKnown] 内缺季时用「标题：第N季」精确补查。
//
// 返回 ok=false 表示当前剧拿不到官方字段，需要回退启发式。
func (engine *nativeEngine) officialSeasons(
	ctx context.Context,
	dramas []Drama,
	current nativeDrama,
	resolve func(context.Context, string) (string, int, Drama, bool),
	search func(context.Context, string) ([]Drama, error),
) ([]nativeSeason, bool) {
	// 热路径 1：当前 Drama 自带 AlbumID/SeasonIndex 就不用 resolve。
	album := current.AlbumID
	currentNumber := current.SeasonIndex
	var detail Drama
	detailOK := false
	if album == "" || currentNumber < 1 {
		resolveAlbum, resolveNumber, resolveDetail, ok := resolve(ctx, current.SourceID)
		if !ok || resolveAlbum == "" || resolveNumber < 1 {
			return nil, false
		}
		album, currentNumber, detail = resolveAlbum, resolveNumber, resolveDetail
		detailOK = true
	}
	// 详情里的上线时间/集数比列表数据更可靠，用来补齐当前这一季。
	if current.OnlineDate == "" && detailOK {
		current.OnlineDate = providerReleaseDate(detail.OnlineDate)
	}
	if current.Episodes == 0 && detailOK {
		current.Episodes = nativeNormalize(detail).Episodes
	}
	base, _, unit := hongguoSeasonLookup(current.Title)
	titleKey := hongguoSearchText(base)
	type entry struct {
		drama     nativeDrama
		sameAlbum bool
	}
	byNumber := map[int]entry{currentNumber: {drama: current, sameAlbum: true}}
	officialNumbers := map[int]bool{currentNumber: true}
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

	// ---- 热路径 2：候选自带 AlbumID/SeasonIndex 的直接用（纯内存） ----
	var missing []Drama
	for _, drama := range candidates {
		if drama.AlbumID != "" && drama.SeasonIndex >= 1 && drama.SeasonIndex <= hongguoSearchSeasonLimit {
			itemAlbum := drama.AlbumID
			number := drama.SeasonIndex
			sameAlbum := itemAlbum == album
			if !sameAlbum {
				itemBase, _, itemUnit := hongguoSeasonLookup(drama.DisplayTitle())
				if titleKey == "" || itemUnit != unit || hongguoSearchText(itemBase) != titleKey {
					continue
				}
			}
			if number != currentNumber {
				if existing, exists := byNumber[number]; !exists || !existing.sameAlbum && sameAlbum {
					byNumber[number] = entry{drama: nativeNormalize(drama), sameAlbum: sameAlbum}
				}
			}
			if sameAlbum {
				officialNumbers[number] = true
			}
		} else {
			missing = append(missing, drama)
		}
	}

	// ---- 兜底：缺字段的候选走 resolve 通道调详情 ----
	if len(missing) > 0 {
		var mu sync.Mutex
		jobs := make(chan Drama)
		var group sync.WaitGroup
		for lane := 0; lane < hongguoSeasonGroupLanes; lane++ {
			group.Add(1)
			go func(lane int) {
				defer group.Done()
				laneCtx := context.WithValue(ctx, requestLaneKey{}, "season-"+strconv.Itoa(lane))
				for drama := range jobs {
					itemCtx, cancel := context.WithTimeout(laneCtx, hongguoSeasonGroupTimeout)
					itemAlbum, number, _, found := resolve(itemCtx, drama.SourceID)
					cancel()
					if !found || number < 1 || number > hongguoSearchSeasonLimit {
						continue
					}
					sameAlbum := itemAlbum == album
					if !sameAlbum {
						itemBase, _, itemUnit := hongguoSeasonLookup(drama.DisplayTitle())
						if titleKey == "" || itemUnit != unit || hongguoSearchText(itemBase) != titleKey {
							continue
						}
					}
					mu.Lock()
					if number != currentNumber {
						if existing, exists := byNumber[number]; !exists || !existing.sameAlbum && sameAlbum {
							byNumber[number] = entry{drama: nativeNormalize(drama), sameAlbum: sameAlbum}
						}
					}
					if sameAlbum {
						officialNumbers[number] = true
					}
					mu.Unlock()
				}
			}(lane)
		}
		for _, drama := range missing {
			jobs <- drama
		}
		close(jobs)
		group.Wait()
	}

	// ---- 补洞：官方组季号序列在 [1, maxKnown] 内有缺口时用「标题第N季」精确补查 ----
	// 联想接口用剧名做关键词召回不全（如「持械入宋」召回 11 条缺正确的第二季），
	// 缺口会被蹭名剧（同 base 标题、另一系列组）抢占季号槽位；
	// 带分隔符的精确关键词能召回缺失的官方季，同组官方季顶掉蹭名剧。
	// 缺口按官方组成员（officialNumbers）判定，而不是 byNumber 占位——
	// 蹭名剧占着槽位时官方组序列仍然缺季。必须在 resolve 兜底之后执行。
	if search != nil {
		maxKnown := 0
		for number := range officialNumbers {
			maxKnown = max(maxKnown, number)
		}
		var targets []int
		for season := 1; season <= maxKnown && len(targets) < 4; season++ {
			if !officialNumbers[season] {
				targets = append(targets, season)
			}
		}
		// 当前季已是官方组里最大的季时，向后探测一季：红果常把后续季
		// 拆到新的系列组（首季上架时尚未建立系列），plan/v 与联想都带不出来。
		// 仅在 fallback 路径执行（plan/v 返回多季时会提前 return），
		// 不会拖慢正常的完整系列。
		if maxKnown == currentNumber && maxKnown < hongguoSearchSeasonLimit {
			targets = append(targets, maxKnown+1)
		}
		requests := 0
		for _, season := range targets {
			for _, label := range []string{hongguoSearchChineseSeason(season), strconv.Itoa(season)} {
				for _, joiner := range []string{"：", " ", ""} {
					if requests >= 8 {
						break
					}
					query := base + joiner + "第" + label + unit
					if _, err := hongguoSearchKeyword(query); err != nil {
						continue
					}
					requests++
					gapNames, err := search(ctx, query)
					if err != nil {
						continue
					}
					for _, drama := range gapNames {
						if drama.SourceID == "" || seen[drama.SourceID] {
							continue
						}
						// 先按标题粗筛（base/单位/季号都对得上才值得调详情校验）
						itemBase, number, itemUnit, _ := hongguoSearchSeason(drama.DisplayTitle())
						if number != season || itemUnit != unit || hongguoSearchText(itemBase) != titleKey {
							continue
						}
						seen[drama.SourceID] = true
						itemCtx, cancel := context.WithTimeout(ctx, hongguoSeasonGroupTimeout)
						itemAlbum, officialNumber, _, found := resolve(itemCtx, drama.SourceID)
						cancel()
						if !found || officialNumber != season || officialNumber > hongguoSearchSeasonLimit {
							continue
						}
						if officialNumber == currentNumber {
							continue
						}
						sameAlbum := itemAlbum == album
						if existing, exists := byNumber[officialNumber]; !exists || !existing.sameAlbum && sameAlbum {
							byNumber[officialNumber] = entry{drama: nativeNormalize(drama), sameAlbum: sameAlbum}
						}
					}
				}
			}
		}
	}
	numbers := make([]int, 0, len(byNumber))
	for number := range byNumber {
		numbers = append(numbers, number)
	}
	sort.Ints(numbers)
	// 跨组并入的季要用日期单调性 / 集数规模兜底，拒绝同名不同剧。
	var latest time.Time
	items := make([]nativeSeason, 0, len(numbers))
	for _, number := range numbers {
		item := byNumber[number]
		date, dated := hongguoSeasonDate(item.drama.OnlineDate)
		if number != currentNumber && !item.sameAlbum {
			if dated {
				if !latest.IsZero() && date.Add(hongguoSeasonDateTolerance).Before(latest) {
					continue
				}
			} else if !hongguoSeasonEpisodesPlausible(current.Episodes, item.drama.Episodes) {
				continue
			}
		}
		if dated && date.After(latest) {
			latest = date
		}
		items = append(items, nativeSeason{
			Number:   number,
			Unit:     firstNonEmpty(unit, "季"),
			Title:    strings.TrimSpace(item.drama.Title),
			Episodes: item.drama.Episodes,
			Current:  number == currentNumber,
			Drama:    item.drama,
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
	case sourceZuid, sourceFfzy, sourceRycj:
		// 采集站沿用同一套季规则：列表已带上线日期，缺日期再查详情。
		return seasonAdapter{
			search: func(ctx context.Context, keyword string) ([]Drama, error) {
				items, _, err := engine.downloader.fetchMaccmsCatalogPage(ctx, source, 1, "", keyword)
				return items, err
			},
			date: func(ctx context.Context, sourceID string) string {
				raw, _, err := engine.downloader.fetchMaccmsDetail(ctx, source, sourceID)
				if err != nil {
					return ""
				}
				return providerReleaseDate(raw.OnlineDate)
			},
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
