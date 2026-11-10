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

func TestNativeSeasonsSkipsSourcesWithoutSeasonData(t *testing.T) {
	engine, err := newNativeEngine(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.nativeSeasons(context.Background(), nativeInput{
		Drama: nativeDrama{ID: "huangju:1001", Source: "huangju", SourceID: "1001", Title: "合成剧第二季"},
	})
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("non-hongguo seasons = %+v, err = %v", result.Items, err)
	}
	result, err = engine.nativeSeasons(context.Background(), nativeInput{
		Drama: nativeDrama{ID: "hongguo:1001", Source: "hongguo", SourceID: "1001", Title: "合成剧"},
	})
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("single season list = %+v, err = %v", result.Items, err)
	}
}
