package core

import (
	"context"
	"errors"
)

type nativeSeason struct {
	Number int          `json:"number"`
	Drama  LibraryDrama `json:"drama"`
}

func (engine *nativeEngine) nativeSeasons(ctx context.Context, drama LibraryDrama) (any, error) {
	source, sourceID, valid := SplitProviderDramaID(drama.ID)
	if !valid {
		return nil, errors.New("剧集信息无效")
	}
	p, ok := engine.downloader.provider(source)
	if !ok {
		return nil, errors.New("请选择有效站源")
	}
	items := []nativeSeason{}
	provider, ok := p.(SeasonProvider)
	if !ok {
		return map[string]any{"items": items}, nil
	}
	seasons, err := provider.Seasons(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	current := false
	for _, season := range seasons {
		item := NativeNormalize(season.Drama)
		if item.ID == drama.ID {
			item, current = MergeNativeDrama(drama, item), true
		}
		items = append(items, nativeSeason{Number: season.Number, Drama: item})
	}
	if !current || len(items) < 2 {
		items = items[:0]
	}
	return map[string]any{"items": items}, nil
}
