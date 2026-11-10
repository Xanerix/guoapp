package core

import (
	"context"
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
	// 实测数据：第二季(812集)的 related_album_id 与第一季/第三季不同，不能并入。
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
	})
	items, ok := engine.officialSeasons(context.Background(), []Drama{
		seasonDrama("7663898166743288857", "家有五女第一季", 125),
		seasonDrama("7685376296183942169", "家有五女第三季", 174),
	}, current, resolve)
	if !ok {
		t.Fatal("official group should be available")
	}
	if len(items) != 1 || !items[0].Current || items[0].Number != 2 {
		t.Fatalf("seasons = %+v", items)
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
	}, current, resolve)
	if !ok {
		t.Fatal("official group should be available")
	}
	if len(items) != 3 {
		t.Fatalf("seasons = %+v", items)
	}
	for index, want := range []int{1, 3, 4} {
		if items[index].Number != want {
			t.Fatalf("season %d number = %d", index, items[index].Number)
		}
	}
	if !items[1].Current || items[0].Current || items[2].Current {
		t.Fatalf("current season marker = %+v", items)
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
	if _, ok := engine.officialSeasons(context.Background(), nil, current, resolve); ok {
		t.Fatal("missing official fields must fall back to heuristics")
	}
}
