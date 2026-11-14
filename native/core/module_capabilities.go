package core

import (
	"context"
	"encoding/json"
)

type jsRecommendationPage struct {
	Items   []Drama         `json:"items"`
	Cursor  json.RawMessage `json:"cursor"`
	HasMore bool            `json:"hasMore"`
}

type jsDanmakuIDs struct {
	Series string `json:"series"`
	Video  string `json:"video"`
}

type jsSeason struct {
	Number int   `json:"number"`
	Drama  Drama `json:"drama"`
}

func moduleRaw(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func (p *jsProvider) quickCall(function string, result any, arguments ...any) error {
	ctx, cancel := context.WithTimeout(context.Background(), moduleQuickTimeout)
	defer cancel()
	return p.call(ctx, function, result, arguments...)
}

func (p *jsProvider) Suggestions(ctx context.Context, query string) ([]string, error) {
	var names []string
	return names, p.call(ctx, "suggestions", &names, query)
}

func (p *jsProvider) ValidRecommendation(genre string, cursor json.RawMessage) error {
	var ignored any
	return p.quickCall("validRecommendation", &ignored, genre, moduleRaw(cursor))
}

func (p *jsProvider) Recommendations(ctx context.Context, genre string, cursor json.RawMessage, seen []string) (RecommendationPage, error) {
	var page jsRecommendationPage
	if err := p.call(ctx, "recommendations", &page, genre, moduleRaw(cursor), seen); err != nil {
		return RecommendationPage{}, err
	}
	return RecommendationPage{Dramas: page.Items, Cursor: page.Cursor, HasMore: page.HasMore}, nil
}

func (p *jsProvider) DanmakuIDs(task Task) (string, string, bool) {
	var ids *jsDanmakuIDs
	if p.quickCall("danmakuIds", &ids, task) != nil || ids == nil || ids.Series == "" || ids.Video == "" {
		return "", "", false
	}
	return ids.Series, ids.Video, true
}

func (p *jsProvider) Danmaku(ctx context.Context, series, video string, startMS, durationMS int64) (DanmakuPage, error) {
	var page DanmakuPage
	return page, p.call(ctx, "danmaku", &page, series, video, startMS, durationMS)
}

func (p *jsProvider) Devices(ctx context.Context, register bool) (bool, error) {
	var ready bool
	return ready, p.call(ctx, "devices", &ready, register)
}

func (p *jsProvider) RankingPage(ctx context.Context, board RankingBoard, page int) (RankingPage, error) {
	var result RankingPage
	return result, p.call(ctx, "rankingPage", &result, board.ID, page)
}

func (p *jsProvider) Seasons(ctx context.Context, sourceID string) ([]Season, error) {
	var found []jsSeason
	if err := p.call(ctx, "seasons", &found, sourceID); err != nil || len(found) == 0 {
		return nil, err
	}
	seasons := make([]Season, 0, len(found))
	for _, season := range found {
		seasons = append(seasons, Season{Number: season.Number, Drama: season.Drama})
	}
	return seasons, nil
}

func (p *jsProvider) CoverAddress(ctx context.Context, drama LibraryDrama, sourceID string) (string, error) {
	var address string
	return address, p.call(ctx, "coverAddress", &address, drama, sourceID)
}

func (p *jsProvider) NeedsExtraMetadata(drama LibraryDrama) bool {
	var needed bool
	return p.quickCall("needsExtraMetadata", &needed, drama) == nil && needed
}

func (p *jsProvider) ExtraMetadata(ctx context.Context, drama LibraryDrama, sourceID string) (LibraryDrama, error) {
	var patch *Drama
	if err := p.call(ctx, "extraMetadata", &patch, drama, sourceID); err != nil {
		return drama, err
	}
	if patch != nil && patch.ID == drama.ID {
		drama = MergeNativeDrama(drama, NativeNormalize(*patch))
	}
	return drama, ctx.Err()
}

func (p *jsProvider) CatalogState() json.RawMessage {
	var state json.RawMessage
	if p.quickCall("catalogState", &state) != nil || string(state) == "null" {
		return nil
	}
	return state
}

func (p *jsProvider) RestoreCatalogState(raw json.RawMessage) {
	var ignored any
	_ = p.quickCall("restoreCatalogState", &ignored, moduleRaw(raw))
}
