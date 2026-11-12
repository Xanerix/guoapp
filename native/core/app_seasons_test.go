package core

import (
	"context"
	"strings"
	"testing"
)

func seasonDrama(id, title string, episodes int) Drama {
	return Drama{ID: "hongguo:" + id, SourceID: id, Title: title, Episodes: episodes}
}

func TestHongguoSeasonListGroupsStrictTitles(t *testing.T) {
	current := nativeNormalize(seasonDrama("2002", "合成剧第二季", 60))
	items := hongguoSeasonList(
		[]Drama{
			seasonDrama("2001", "合成剧第一季", 80),
			seasonDrama("2003", "合成剧第三季", 40),
			// 标题只是前缀相同，必须排除。
			seasonDrama("2999", "合成剧之逆袭第一季", 30),
			// 单位不同（部），不并入季列表。
			seasonDrama("2998", "合成剧第二部", 20),
		},
		2,
		"季",
		hongguoSearchText("合成剧"),
		current,
	)
	if len(items) != 3 {
		t.Fatalf("seasons = %d, want 3", len(items))
	}
	for index, want := range []int{1, 2, 3} {
		if items[index].Number != want {
			t.Fatalf("season %d number = %d", index, items[index].Number)
		}
	}
	if !items[1].Current || items[0].Current || items[2].Current {
		t.Fatal("only the opened season may be current")
	}
	if items[1].Drama.ID != current.ID {
		t.Fatalf("current season drama = %s, want %s", items[1].Drama.ID, current.ID)
	}
	if items[1].Episodes != 60 || items[0].Episodes != 80 {
		t.Fatalf("episodes not carried over: %+v", items)
	}
}

func TestHongguoSeasonListKeepsOpenedSeasonWhenDuplicate(t *testing.T) {
	current := nativeNormalize(seasonDrama("3002", "合成剧第二季", 60))
	items := hongguoSeasonList(
		[]Drama{seasonDrama("3999", "合成剧第二季", 12)},
		2,
		"季",
		hongguoSearchText("合成剧"),
		current,
	)
	if len(items) != 1 || items[0].Drama.ID != current.ID {
		t.Fatalf("duplicate season replaced the opened drama: %+v", items)
	}
}

func seasonDramaOn(id, title, date string, episodes int) Drama {
	return Drama{ID: "hongguo:" + id, SourceID: id, Title: title, Episodes: episodes, OnlineDate: date}
}

func TestHongguoSeasonListDropsSeasonWithEarlierOnlineDate(t *testing.T) {
	// 第二季 2026-11-05 上线，搜到的“第三季”却是 2026-09-16，属于同名不同剧。
	current := nativeNormalize(seasonDramaOn("6002", "合成剧第二季", "2026-11-05", 60))
	items := hongguoSeasonList(
		[]Drama{
			seasonDramaOn("6001", "合成剧第一季", "2026-10-01", 80),
			seasonDramaOn("6003", "合成剧第三季", "2026-09-16", 70),
		},
		2,
		"季",
		hongguoSearchText("合成剧"),
		current,
	)
	if len(items) != 2 {
		t.Fatalf("seasons = %d, want 2: %+v", len(items), items)
	}
	if items[0].Number != 1 || items[1].Number != 2 {
		t.Fatalf("unexpected season numbers: %+v", items)
	}
	if !items[1].Current || items[1].Drama.ID != current.ID {
		t.Fatalf("current season was not preserved: %+v", items[1])
	}
}

func TestHongguoSeasonListKeepsAscendingOnlineDates(t *testing.T) {
	current := nativeNormalize(seasonDramaOn("7001", "合成剧第一季", "2026-01-10", 80))
	items := hongguoSeasonList(
		[]Drama{
			seasonDramaOn("7002", "合成剧第二季", "2026-05-20", 60),
			seasonDramaOn("7003", "合成剧第三季", "2026-10-08", 40),
		},
		1,
		"季",
		hongguoSearchText("合成剧"),
		current,
	)
	if len(items) != 3 {
		t.Fatalf("seasons = %d, want 3: %+v", len(items), items)
	}
}

func TestHongguoSeasonListDropsOutlierEpisodesWithoutDate(t *testing.T) {
	// 第二季 812 集，搜到的“第三季”只有 174 集：集数相差近 5 倍，判为同名不同剧。
	current := nativeNormalize(seasonDrama("6102", "合成剧第二季", 812))
	items := hongguoSeasonList(
		[]Drama{
			seasonDrama("6101", "合成剧第一季", 800),
			seasonDrama("6103", "合成剧第三季", 174),
		},
		2,
		"季",
		hongguoSearchText("合成剧"),
		current,
	)
	if len(items) != 2 {
		t.Fatalf("seasons = %d, want 2: %+v", len(items), items)
	}
	for _, item := range items {
		if item.Number == 3 {
			t.Fatalf("outlier season kept: %+v", item)
		}
	}
}

func TestNativeSeasonAdapterCoversSearchableSources(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{sourceHongguo, sourceHuangju, sourceSorani, sourceGuipian, sourceHanxiaoquan, sourceHuangguoAI} {
		if _, ok := engine.nativeSeasonAdapter(source); !ok {
			t.Fatalf("source %s is not supported", source)
		}
	}
	for _, source := range []string{sourceBeabox, sourceHeitao, sourceXvideos, sourceDSD} {
		if _, ok := engine.nativeSeasonAdapter(source); ok {
			t.Fatalf("source %s should not be supported yet", source)
		}
	}
}

func TestHongguoSeasonLookupTreatsBareTitleAsFirstSeason(t *testing.T) {
	for _, sample := range []struct {
		title  string
		base   string
		number int
		unit   string
	}{
		{"家有五女，穿越来的他连夜搞事业啦", "家有五女，穿越来的他连夜搞事业啦", 1, "季"},
		{"家有五女，穿越来的他连夜搞事业啦第二季", "家有五女，穿越来的他连夜搞事业啦", 2, "季"},
		{"合成剧第三部", "合成剧", 3, "部"},
		{"", "", 0, ""},
	} {
		base, number, unit := hongguoSeasonLookup(sample.title)
		if hongguoSearchText(base) != hongguoSearchText(sample.base) ||
			number != sample.number ||
			unit != sample.unit {
			t.Fatalf("lookup(%q) = %q/%d/%q", sample.title, base, number, unit)
		}
	}
}

func TestHongguoSeasonListIncludesFirstSeasonWithoutSuffix(t *testing.T) {
	current := nativeNormalize(seasonDrama("5001", "合成剧", 80))
	items := hongguoSeasonList(
		[]Drama{
			seasonDrama("5002", "合成剧第二季", 60),
			// 只是前缀相同，不能并入。
			seasonDrama("5999", "合成剧番外第二季", 10),
		},
		1,
		"季",
		hongguoSearchText("合成剧"),
		current,
	)
	if len(items) != 2 {
		t.Fatalf("seasons = %d, want 2", len(items))
	}
	if items[0].Number != 1 || items[0].Drama.ID != current.ID || !items[0].Current {
		t.Fatalf("first season not the opened drama: %+v", items[0])
	}
	if items[1].Number != 2 || items[1].Drama.ID != "hongguo:5002" {
		t.Fatalf("second season = %+v", items[1])
	}
}

func TestNativeSeasonsSkipsSourcesWithoutSeasonData(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.nativeSeasons(context.Background(), nativeInput{
		Drama: nativeDrama{ID: "beabox:1001", Source: "beabox", SourceID: "1001", Title: "合成剧第二季"},
	})
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("unsupported source seasons = %+v, err = %v", result.Items, err)
	}
	result, err = engine.nativeSeasons(context.Background(), nativeInput{
		Drama: nativeDrama{ID: "hongguo:1001", Source: "hongguo", SourceID: "1001", Title: ""},
	})
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("empty title seasons = %+v, err = %v", result.Items, err)
	}
}

func seasonGroupResolver(groups map[string]struct {
	album  string
	number int
}) func(context.Context, string) (string, int, Drama, bool) {
	return func(_ context.Context, seriesID string) (string, int, Drama, bool) {
		item, ok := groups[seriesID]
		return item.album, item.number, Drama{}, ok
	}
}

func TestOfficialSeasonsDropsDifferentAlbum(t *testing.T) {
	// 实测数据：「家有五女」第二季(812集)与第一季(125集)、第三季(174集)
	// 分属不同系列组，且没有上线日期。官方季号与标题都对得上，属于同一部剧，
	// 不能再因为集数差异大而丢掉；蹭名剧（base 不同）仍必须拒绝。
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	current := nativeNormalize(seasonDrama("7701588705932217406", "家有五女第二季", 812))
	resolve := seasonGroupResolver(map[string]struct {
		album  string
		number int
	}{
		"7701588705932217406": {"7689000294050450456", 2},
		"7663898166743288857": {"7674948658000841790", 1},
		"7685376296183942169": {"7674948658000841790", 3},
		"7689999999999999999": {"7674948658000841790", 3},
	})
	items, ok := engine.officialSeasons(context.Background(), []Drama{
		seasonDrama("7689999999999999999", "家有五女外传第三季", 174),
		seasonDrama("7663898166743288857", "家有五女第一季", 125),
		seasonDrama("7685376296183942169", "家有五女第三季", 174),
	}, current, resolve, nil)
	if !ok {
		t.Fatal("official group should be available")
	}
	numbers := make([]int, 0, len(items))
	for _, item := range items {
		numbers = append(numbers, item.Number)
	}
	if len(numbers) != 3 || numbers[0] != 1 || numbers[1] != 2 || numbers[2] != 3 {
		t.Fatalf("seasons = %v, want [1 2 3]: %+v", numbers, items)
	}
	if !items[1].Current {
		t.Fatalf("current marker = %+v", items)
	}
	// 第三季槽位必须是被标题校验过的正主，不能被「外传」蹭走。
	if items[2].Drama.SourceID != "7685376296183942169" {
		t.Fatalf("season 3 should keep the exact title match: %+v", items[2])
	}
}

func TestOfficialSeasonsGroupsSameAlbumBySeasonIndex(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	current := nativeNormalize(seasonDrama("2002", "合成剧第三季", 40))
	resolve := seasonGroupResolver(map[string]struct {
		album  string
		number int
	}{
		"2002": {"A", 3},
		"2001": {"A", 1},
		"2003": {"A", 4},
		"2999": {"B", 2},
	})
	items, ok := engine.officialSeasons(context.Background(), []Drama{
		seasonDrama("2001", "合成剧第一季", 80),
		seasonDrama("2001", "合成剧第一季（重复）", 80),
		seasonDrama("2003", "合成剧第四季", 30),
		seasonDrama("2999", "合成剧第二季", 60),
	}, current, resolve, nil)
	if !ok {
		t.Fatal("official group should be available")
	}
	// 2999（另一组、第2季）标题同类且集数可信，允许补进缺的第 2 季。
	if len(items) != 4 {
		t.Fatalf("seasons = %+v", items)
	}
	for index, want := range []int{1, 2, 3, 4} {
		if items[index].Number != want {
			t.Fatalf("season %d number = %d", index, items[index].Number)
		}
	}
	if items[1].Drama.SourceID != "2999" {
		t.Fatalf("season 2 should come from the other album: %+v", items[1])
	}
	if !items[2].Current || items[0].Current || items[1].Current || items[3].Current {
		t.Fatalf("current season marker = %+v", items)
	}
}

func TestOfficialSeasonsMergesSplitAlbums(t *testing.T) {
	// 实测：「持械入宋」被官方拆成两个系列组，第二、五季在另一组，但仍是同一部剧。
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	current := nativeNormalize(seasonDramaOn("1", "合成剧第一季", "2026-08-09", 100))
	details := map[string]struct {
		album  string
		number int
		drama  Drama
	}{
		"1": {"A", 1, seasonDramaOn("1", "合成剧第一季", "2026-08-09", 100)},
		"2": {"B", 2, seasonDramaOn("2", "合成剧第二季", "2026-08-19", 143)},
		"3": {"A", 3, seasonDramaOn("3", "合成剧第三季", "2026-08-27", 100)},
		"5": {"B", 5, seasonDramaOn("5", "合成剧第五季", "2026-10-05", 214)},
	}
	resolve := func(_ context.Context, sourceID string) (string, int, Drama, bool) {
		item, ok := details[sourceID]
		return item.album, item.number, item.drama, ok
	}
	items, ok := engine.officialSeasons(context.Background(), []Drama{
		seasonDramaOn("3", "合成剧第三季", "2026-08-27", 100),
		seasonDramaOn("2", "合成剧第二季", "2026-08-19", 143),
		seasonDramaOn("5", "合成剧第五季", "2026-10-05", 214),
	}, current, resolve, nil)
	if !ok {
		t.Fatal("official group should be available")
	}
	var numbers []int
	for _, item := range items {
		numbers = append(numbers, item.Number)
	}
	if len(numbers) != 4 || numbers[0] != 1 || numbers[1] != 2 || numbers[2] != 3 || numbers[3] != 5 {
		t.Fatalf("seasons = %v, want [1 2 3 5]", numbers)
	}
}

func TestOfficialSeasonsRejectsCrossAlbumOutOfOrder(t *testing.T) {
	// 实测：「家有五女」第二季(11-05) 与第三季(09-16) 分属不同系列组，日期倒挂应剔除。
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	current := nativeNormalize(seasonDramaOn("2", "合成剧第二季", "2026-11-05", 812))
	details := map[string]struct {
		album  string
		number int
		drama  Drama
	}{
		"2": {"B", 2, seasonDramaOn("2", "合成剧第二季", "2026-11-05", 812)},
		"3": {"A", 3, seasonDramaOn("3", "合成剧第三季", "2026-09-16", 174)},
	}
	resolve := func(_ context.Context, sourceID string) (string, int, Drama, bool) {
		item, ok := details[sourceID]
		return item.album, item.number, item.drama, ok
	}
	items, ok := engine.officialSeasons(context.Background(), []Drama{
		seasonDramaOn("3", "合成剧第三季", "2026-09-16", 174),
	}, current, resolve, nil)
	if !ok {
		t.Fatal("official group should be available")
	}
	if len(items) != 1 || items[0].Number != 2 {
		t.Fatalf("seasons = %+v, want only the opened season", items)
	}
}

func TestOfficialSeasonsProbesNextSeasonAcrossAlbums(t *testing.T) {
	// 实测：红果把「首季」单独建一个系列组，后续季在另一个组。
	// plan/v 只返回第一季、联想也只召回第一季时，必须靠「剧名：第N季」
	// 向后探测把第二季补回来，否则从第一季点进去永远看不到第二季。
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	current := nativeNormalize(seasonDramaOn("101", "合成剧第一季", "2026-09-23", 100))
	details := map[string]struct {
		album  string
		number int
		drama  Drama
	}{
		"101": {"A", 1, seasonDramaOn("101", "合成剧第一季", "2026-09-23", 100)},
		"102": {"B", 2, seasonDramaOn("102", "合成剧第二季", "2026-10-28", 100)},
	}
	resolve := func(_ context.Context, sourceID string) (string, int, Drama, bool) {
		item, ok := details[sourceID]
		return item.album, item.number, item.drama, ok
	}
	var keywords []string
	search := func(_ context.Context, keyword string) ([]Drama, error) {
		keywords = append(keywords, keyword)
		if strings.Contains(keyword, "第二季") || strings.Contains(keyword, "第2季") {
			return []Drama{seasonDramaOn("102", "合成剧第二季", "2026-10-28", 100)}, nil
		}
		return nil, nil
	}
	items, ok := engine.officialSeasons(context.Background(), nil, current, resolve, search)
	if !ok {
		t.Fatal("official group should be available")
	}
	numbers := make([]int, 0, len(items))
	for _, item := range items {
		numbers = append(numbers, item.Number)
	}
	if len(numbers) != 2 || numbers[0] != 1 || numbers[1] != 2 {
		t.Fatalf("seasons = %v (keywords=%v), want [1 2]", numbers, keywords)
	}
}

func TestOfficialSeasonsFallsBackWithoutFields(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	current := nativeNormalize(seasonDrama("1", "合成剧第二季", 60))
	resolve := func(context.Context, string) (string, int, Drama, bool) {
		return "", 0, Drama{}, false
	}
	if _, ok := engine.officialSeasons(context.Background(), nil, current, resolve, nil); ok {
		t.Fatal("missing official fields must fall back to heuristics")
	}
}

func TestOfficialSeasonsFillsSeasonGapAndEvictsImpostor(t *testing.T) {
	// 实测：「持械入宋」用剧名做联想关键词召回不到正确的第二季，
	// 蹭名剧（另一系列组、季号同为 2）抢占了槽位；带分隔符的精确关键词能召回正确季。
	// 补查回来的同组官方季要顶掉蹭名剧。
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	current := nativeNormalize(seasonDramaOn("1", "合成剧第一季", "2026-08-09", 100))
	details := map[string]struct {
		album  string
		number int
	}{
		"1":  {"A", 1},
		"2x": {"B", 2}, // 蹭名剧：另一系列组、季号同为 2
		"2":  {"A", 2}, // 正确的第二季
		"3":  {"A", 3},
		"4":  {"A", 4},
	}
	resolve := func(_ context.Context, sourceID string) (string, int, Drama, bool) {
		item, ok := details[sourceID]
		return item.album, item.number, Drama{}, ok
	}
	// 模拟联想搜索：剧名关键词召回不到正确第二季，精确关键词可以。
	search := func(_ context.Context, keyword string) ([]Drama, error) {
		if strings.Contains(keyword, "第二季") || strings.Contains(keyword, "第2季") {
			return []Drama{seasonDramaOn("2", "合成剧第二季", "2026-08-22", 100)}, nil
		}
		return nil, nil
	}
	items, ok := engine.officialSeasons(context.Background(), []Drama{
		seasonDramaOn("2x", "合成剧第二季", "2026-08-19", 143),
		seasonDramaOn("3", "合成剧第三季", "2026-08-27", 100),
		seasonDramaOn("4", "合成剧第四季", "2026-09-02", 100),
	}, current, resolve, search)
	if !ok {
		t.Fatal("official group should be available")
	}
	if len(items) != 4 {
		t.Fatalf("seasons = %+v", items)
	}
	for index, want := range []int{1, 2, 3, 4} {
		if items[index].Number != want {
			t.Fatalf("season %d number = %d", index, items[index].Number)
		}
	}
	// 第 2 季槽位必须由同组官方季（正确的第二季）占据，蹭名剧被顶掉。
	if items[1].Drama.SourceID != "2" {
		t.Fatalf("season 2 should be the same-album official season: %+v", items[1])
	}
	if items[1].Drama.Episodes != 100 {
		t.Fatalf("season 2 should keep official metadata: %+v", items[1])
	}
}

func TestOfficialSeasonsFillsEarlierSeasonsFromLaterEntry(t *testing.T) {
	// 从第三季进入：第三季被官方单独建组，第一、二季在另一个组。
	// plan/v 和联想都只认得第三季时，必须靠「剧名 第N季」向前补查，
	// 否则从后面的季点进详情永远看不到前面的季。
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	current := nativeNormalize(seasonDrama("103", "合成剧第三季", 100))
	details := map[string]struct {
		album  string
		number int
	}{
		"103": {"B", 3},
		"101": {"A", 1},
		"102": {"A", 2},
	}
	resolve := seasonGroupResolver(details)
	search := func(_ context.Context, keyword string) ([]Drama, error) {
		if strings.Contains(keyword, "第一季") || strings.Contains(keyword, "第1季") {
			return []Drama{seasonDrama("101", "合成剧第一季", 100)}, nil
		}
		if strings.Contains(keyword, "第二季") || strings.Contains(keyword, "第2季") {
			return []Drama{seasonDrama("102", "合成剧第二季", 100)}, nil
		}
		return nil, nil
	}
	items, ok := engine.officialSeasons(context.Background(), nil, current, resolve, search)
	if !ok {
		t.Fatal("official group should be available")
	}
	numbers := make([]int, 0, len(items))
	for _, item := range items {
		numbers = append(numbers, item.Number)
	}
	if len(numbers) != 3 || numbers[0] != 1 || numbers[1] != 2 || numbers[2] != 3 {
		t.Fatalf("seasons = %v, want [1 2 3]", numbers)
	}
	if !items[2].Current || items[0].Current || items[1].Current {
		t.Fatalf("current marker = %+v", items)
	}
}

func TestOfficialSeasonsKeepsCrossAlbumEpisodeOutlier(t *testing.T) {
	// 实测：短剧各季集数差异很大（812 集后面接 174 集），没有上线日期时
	// 不能再按「集数相差 3 倍」把真实的季当成同名不同剧丢掉。
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	current := nativeNormalize(seasonDrama("202", "合成剧第二季", 812))
	resolve := seasonGroupResolver(map[string]struct {
		album  string
		number int
	}{
		"202": {"A", 2},
		"203": {"B", 3},
	})
	items, ok := engine.officialSeasons(context.Background(), []Drama{
		seasonDrama("203", "合成剧第三季", 174),
	}, current, resolve, nil)
	if !ok {
		t.Fatal("official group should be available")
	}
	if len(items) != 2 || items[0].Number != 2 || items[1].Number != 3 {
		t.Fatalf("seasons = %+v, want [2 3]", items)
	}
}

func TestMergeSeasonItemsPrefersOfficialSkeleton(t *testing.T) {
	// plan/v 骨架（第二、五季）与搜索聚合（第一、二、四季）合并：
	// 同季号以骨架为准，缺的季号由聚合补上。
	official := []nativeSeason{
		{Number: 2, Title: "官方第二季"},
		{Number: 5, Title: "官方第五季"},
	}
	found := []nativeSeason{
		{Number: 1, Title: "搜索第一季"},
		{Number: 2, Title: "搜索第二季"},
		{Number: 4, Title: "搜索第四季"},
	}
	items := mergeSeasonItems(official, found)
	numbers := make([]int, 0, len(items))
	for _, item := range items {
		numbers = append(numbers, item.Number)
	}
	if len(numbers) != 4 || numbers[0] != 1 || numbers[1] != 2 || numbers[2] != 4 || numbers[3] != 5 {
		t.Fatalf("seasons = %v, want [1 2 4 5]", numbers)
	}
	if items[1].Title != "官方第二季" {
		t.Fatalf("official skeleton should win: %+v", items[1])
	}
	if len(mergeSeasonItems(nil, found)) != 3 || len(mergeSeasonItems(official, nil)) != 2 {
		t.Fatal("empty side must fall back to the other list")
	}
}

func TestSeasonsCompleteFromFirst(t *testing.T) {
	for _, sample := range []struct {
		numbers []int
		want    bool
	}{
		{nil, false},
		{[]int{1}, true},
		{[]int{1, 2, 3}, true},
		{[]int{2, 3}, false}, // 缺第 1 季
		{[]int{1, 3}, false}, // 中间缺季
	} {
		items := make([]nativeSeason, 0, len(sample.numbers))
		for _, number := range sample.numbers {
			items = append(items, nativeSeason{Number: number})
		}
		if got := seasonsCompleteFromFirst(items); got != sample.want {
			t.Fatalf("seasonsCompleteFromFirst(%v) = %v, want %v", sample.numbers, got, sample.want)
		}
	}
}

func TestOfficialSeasonsSeedDrivesBidirectionalGapFill(t *testing.T) {
	// plan/v 只返回 [3,4]（当前第 3 季），第一、二季在另一个组。
	// 种子里的季直接可信，不需要再校验；缺口按 [1, maxKnown] 向前补，
	// 再从 maxKnown 向后探一季，从任何一季进入都能拿到完整列表。
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	current := nativeNormalize(seasonDrama("103", "合成剧第三季", 100))
	details := map[string]struct {
		album  string
		number int
	}{
		"103": {"B", 3},
		"104": {"B", 4},
		"101": {"A", 1},
		"102": {"A", 2},
		"105": {"C", 5},
	}
	resolve := seasonGroupResolver(details)
	search := func(_ context.Context, keyword string) ([]Drama, error) {
		switch {
		case strings.Contains(keyword, "第一季") || strings.Contains(keyword, "第1季"):
			return []Drama{seasonDrama("101", "合成剧第一季", 100)}, nil
		case strings.Contains(keyword, "第二季") || strings.Contains(keyword, "第2季"):
			return []Drama{seasonDrama("102", "合成剧第二季", 100)}, nil
		case strings.Contains(keyword, "第五季") || strings.Contains(keyword, "第5季"):
			return []Drama{seasonDrama("105", "合成剧第五季", 100)}, nil
		}
		return nil, nil
	}
	seed := []nativeSeason{
		{Number: 3, Title: "合成剧第三季", Drama: current},
		{Number: 4, Title: "合成剧第四季", Drama: nativeNormalize(seasonDrama("104", "合成剧第四季", 100))},
	}
	items, ok := engine.officialSeasonsWithSeed(context.Background(), nil, current, resolve, search, seed)
	if !ok {
		t.Fatal("official group should be available")
	}
	numbers := make([]int, 0, len(items))
	for _, item := range items {
		numbers = append(numbers, item.Number)
	}
	if len(numbers) != 5 || numbers[0] != 1 || numbers[1] != 2 || numbers[2] != 3 || numbers[3] != 4 || numbers[4] != 5 {
		t.Fatalf("seasons = %v, want [1 2 3 4 5]", numbers)
	}
	for _, item := range items {
		if item.Number == 3 && !item.Current {
			t.Fatalf("current marker = %+v", items)
		}
	}
}
