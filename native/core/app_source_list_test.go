package core

import (
	"net/http"
	"testing"
)

func TestSourceListDescribesRegisteredSources(t *testing.T) {
	engine := sourceFixtureEngine(t, func(request *http.Request) (*http.Response, error) {
		t.Error("listing sources must not reach the network")
		return sourceFixtureResponse(request, http.StatusBadGateway, ""), nil
	})
	list := engine.sourceList()
	if len(list) != len(registeredDescriptors()) {
		t.Fatalf("listed %d of %d registered sources", len(list), len(registeredDescriptors()))
	}
	byID := map[string]nativeSourceInfo{}
	for index, info := range list {
		byID[info.ID] = info
		if info.Available != nativeSourceAvailable(info.ID) {
			t.Fatalf("%s availability disagrees with authorization", info.ID)
		}
		if index < len(activeFlavor.sources) && info.ID != activeFlavor.sources[index] {
			t.Fatalf("position %d is %s, flavor expects %s", index, info.ID, activeFlavor.sources[index])
		}
	}
	hongguo := byID[sourceHongguo]
	if !hongguo.Available || hongguo.DefaultOff || !hongguo.Search || !hongguo.Suggestions || !hongguo.Recommendations || !hongguo.Danmaku || !hongguo.Devices || hongguo.Account || !hongguo.VIPPrompt {
		t.Fatalf("wrong hongguo capabilities: %+v", hongguo)
	}
	if sorani := byID[sourceSorani]; !sorani.Account || !sorani.Search || sorani.Danmaku || sorani.DefaultOff {
		t.Fatalf("wrong sorani capabilities: %+v", sorani)
	}
	if dsd := byID[sourceDSD]; dsd.VIPPrompt || !dsd.DefaultOff {
		t.Fatalf("wrong dsd flags: %+v", dsd)
	}
	if video := byID[sourceHuangguoVideo]; video.Group != "huangguo" || video.GroupName != "黄果" || video.EntryName != "视频" || video.Search {
		t.Fatalf("wrong grouped source: %+v", video)
	}
}
