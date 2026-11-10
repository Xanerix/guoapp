package core

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type nativeRecommendationState struct {
	Query   json.RawMessage `json:"query"`
	Seen    []string        `json:"seen,omitempty"`
	Page    int             `json:"page"`
	HasMore bool            `json:"hasMore"`
}

func nativeRecommendationKey(source, genre string) string {
	return source + "|recommendation:" + genre
}

func (engine *nativeEngine) recommender(source string) (Recommender, error) {
	if engine.downloader != nil {
		if p, ok := engine.downloader.provider(source); ok {
			if recommender, ok := p.(Recommender); ok {
				return recommender, nil
			}
		}
	}
	return nil, errors.New("请选择有效站源")
}

func (engine *nativeEngine) cachedRecommendations(source, genre string) (nativeCatalogResult, error) {
	recommender, err := engine.recommender(source)
	if err != nil {
		return nativeCatalogResult{}, err
	}
	if err := recommender.ValidRecommendation(genre, nil); err != nil {
		return nativeCatalogResult{}, err
	}
	key := nativeRecommendationKey(source, genre)
	result := engine.nativeCached(key)
	engine.mu.Lock()
	_, found := engine.recommendations[key]
	engine.mu.Unlock()
	if !found && len(result.Items) > 0 {
		result.Fresh, result.HasMore = false, false
		result.Warning = joinNativeWarnings(result.Warning, "推荐位置已失效，请刷新推荐")
	}
	return result, nil
}

func (engine *nativeEngine) nativeRecommendations(ctx context.Context, input nativeInput) (nativeCatalogResult, error) {
	source, genre := nativeActionSource(input), input.Category
	recommender, err := engine.recommender(source)
	if err != nil {
		return nativeCatalogResult{}, err
	}
	if err := recommender.ValidRecommendation(genre, nil); err != nil {
		return nativeCatalogResult{}, err
	}
	key := nativeRecommendationKey(source, genre)
	unlock, err := engine.lockSourceCatalog(ctx, source)
	if err != nil {
		return nativeCatalogResult{}, err
	}
	defer unlock()
	if retried, err := engine.retryCatalogSave(); retried {
		cached, _ := engine.cachedRecommendations(source, genre)
		if err != nil || len(cached.Items) > 0 {
			return cached, nil
		}
	}
	cached, _ := engine.cachedRecommendations(source, genre)
	engine.mu.Lock()
	state, found := engine.recommendations[key]
	engine.mu.Unlock()
	if !input.Force && found && len(cached.Items) > 0 && (input.Command != "more" || !state.HasMore) {
		return cached, nil
	}
	if !input.Force && !found && len(cached.Items) > 0 {
		return cached, nil
	}
	if input.Force || !found {
		state = nativeRecommendationState{HasMore: true}
	}
	page, err := recommender.Recommendations(ctx, genre, state.Query, state.Seen)
	if err != nil {
		if len(cached.Items) == 0 || errors.Is(err, context.Canceled) {
			return nativeCatalogResult{}, err
		}
		cached.Fresh = false
		cached.Warning = joinNativeWarnings(cached.Warning, PublicError(err).Error())
		return cached, nil
	}
	if err := ctx.Err(); err != nil {
		return nativeCatalogResult{}, err
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.recommendations == nil {
		engine.recommendations = map[string]nativeRecommendationState{}
	}
	previous := engine.catalogs[key]
	if input.Force {
		previous = nil
	}
	seen := make(map[string]bool, len(previous))
	for _, drama := range previous {
		seen[drama.ID] = true
	}
	fresh := make([]LibraryDrama, 0, len(page.Dramas))
	known := make(map[string]LibraryDrama, len(engine.catalogs[source]))
	for _, drama := range engine.catalogs[source] {
		known[drama.ID] = drama
	}
	for _, drama := range page.Dramas {
		item := NativeNormalize(drama)
		if old, exists := known[item.ID]; exists {
			item = MergeNativeDrama(old, item)
		}
		if !seen[item.ID] {
			seen[item.ID] = true
			fresh = append(fresh, item)
		}
	}
	if len(fresh) == 0 && page.HasMore {
		return nativeCatalogResult{}, errors.New("暂时没有新的推荐，已保留上次位置；可重试或刷新推荐")
	}
	items := mergeNativeCatalog(previous, fresh)
	identifiers := make([]string, 0, min(540, len(items)))
	for _, drama := range items[max(0, len(items)-540):] {
		identifiers = append(identifiers, drama.ID)
	}
	state.Query, state.Seen = page.Cursor, identifiers
	state.Page++
	state.HasMore = page.HasMore
	engine.recommendations[key] = state
	engine.catalogs[key] = items
	engine.catalogs[source] = mergeNativeCatalog(engine.catalogs[source], fresh)
	engine.commitProviderStateLocked(source)
	engine.catalogStates[key] = nativeCatalogState{UpdatedAt: time.Now(), Page: state.Page, HasMore: state.HasMore}
	result := nativeCatalogResult{Items: append([]LibraryDrama{}, items...), Page: state.Page, HasMore: state.HasMore, Fresh: true}
	if err := engine.writeCatalogDiskLocked(); err != nil {
		result.Fresh, result.Warning, result.saveError = false, err.Error(), err
	}
	return result, nil
}
