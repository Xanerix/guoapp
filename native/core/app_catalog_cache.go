package core

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const nativeCatalogTTL = 15 * time.Minute

type nativeCatalogState struct {
	UpdatedAt time.Time `json:"updatedAt"`
	Page      int       `json:"page"`
	HasMore   bool      `json:"hasMore"`
	Warning   string    `json:"warning,omitempty"`
}

type nativeCatalogDisk struct {
	Version         int                                  `json:"version"`
	Catalogs        map[string][]LibraryDrama            `json:"catalogs"`
	States          map[string]nativeCatalogState        `json:"states"`
	Categories      map[string][]Category                `json:"categories,omitempty"`
	HongguoApp      json.RawMessage                      `json:"hongguoApp,omitempty"`
	ProviderStates  map[string]json.RawMessage           `json:"providerStates,omitempty"`
	Recommendations map[string]nativeRecommendationState `json:"recommendations,omitempty"`
}

func (engine *nativeEngine) commitProviderStateLocked(source string) {
	source, _, _ = strings.Cut(source, "|")
	p, ok := engine.downloader.provider(source)
	if !ok {
		return
	}
	stateful, ok := p.(CatalogStateful)
	if !ok {
		return
	}
	if engine.providerStates == nil {
		engine.providerStates = map[string]json.RawMessage{}
	}
	if raw := stateful.CatalogState(); len(raw) > 0 {
		engine.providerStates[p.Descriptor().ID] = raw
	} else {
		delete(engine.providerStates, p.Descriptor().ID)
	}
}

func (engine *nativeEngine) restoreProviderStates(disk nativeCatalogDisk) {
	if engine.downloader == nil {
		return
	}
	states := disk.ProviderStates
	if _, ok := states["hongguo"]; !ok && len(disk.HongguoApp) > 0 {
		if states == nil {
			states = map[string]json.RawMessage{}
		}
		states["hongguo"] = disk.HongguoApp
	}
	for source, raw := range states {
		if p, ok := engine.downloader.provider(source); ok {
			if stateful, ok := p.(CatalogStateful); ok {
				stateful.RestoreCatalogState(raw)
				engine.commitProviderStateLocked(source)
			}
		}
	}
}

func (engine *nativeEngine) loadCatalogCache() {
	file, err := os.Open(filepath.Join(engine.directory, "catalogs.json"))
	if err != nil {
		return
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, nativeCatalogMaxBytes+1))
	if err != nil || len(body) > nativeCatalogMaxBytes {
		return
	}
	var disk nativeCatalogDisk
	if json.Unmarshal(body, &disk) == nil && (disk.Version == 2 || disk.Version == 3) {
		if disk.Catalogs != nil {
			engine.catalogs = disk.Catalogs
			trimNativeCatalogs(engine.catalogs)
			for _, items := range engine.catalogs {
				for index := range items {
					items[index] = migrateNativeDrama(items[index])
				}
			}
		}
		if disk.States != nil {
			engine.catalogStates = disk.States
		}
		engine.categoryOptions = disk.Categories
		engine.recommendations = make(map[string]nativeRecommendationState)
		for key, state := range disk.Recommendations {
			source, genre, current := strings.Cut(key, "|recommendation:")
			if !current {
				source, genre = "hongguo", key
			}
			if recommender, err := engine.recommender(source); err == nil && state.Page > 0 && recommender.ValidRecommendation(genre, state.Query) == nil {
				engine.recommendations[nativeRecommendationKey(source, genre)] = state
			}
		}
		engine.restoreProviderStates(disk)
		return
	}
	var legacy map[string][]LibraryDrama
	if json.Unmarshal(body, &legacy) == nil && legacy != nil {
		engine.catalogs = legacy
		trimNativeCatalogs(engine.catalogs)
		for _, items := range engine.catalogs {
			for index := range items {
				items[index] = migrateNativeDrama(items[index])
			}
		}
	}
}

func trimNativeCatalogs(catalogs map[string][]LibraryDrama) {
	for key, items := range catalogs {
		if len(items) > nativeCatalogMaxItems {
			catalogs[key] = append([]LibraryDrama(nil), items[:nativeCatalogMaxItems]...)
		}
	}
}

func migrateNativeDrama(drama LibraryDrama) LibraryDrama {
	if drama.MetadataSchema == 0 && drama.Source == "huangdou" && drama.VIP != nil && !*drama.VIP {
		drama.VIP = nil
	}
	drama.MetadataSchema = 1
	return drama
}

func (engine *nativeEngine) nativeCached(source string) nativeCatalogResult {
	source = canonicalProviderSource(source)
	engine.mu.Lock()
	defer engine.mu.Unlock()
	items := append([]LibraryDrama{}, engine.catalogs[source]...)
	for index := range items {
		items[index].Cover = engine.downloader.repairCover(items[index])
	}
	state, found := engine.catalogStates[source]
	age := time.Since(state.UpdatedAt)
	result := nativeCatalogResult{
		Items: items, Page: max(1, state.Page), HasMore: !found || state.HasMore,
		Fresh:   len(items) > 0 && state.Warning == "" && !state.UpdatedAt.IsZero() && age >= 0 && age < nativeCatalogTTL,
		Warning: state.Warning,
	}
	if engine.catalogSaveError != nil {
		result.Warning = joinNativeWarnings(result.Warning, engine.catalogSaveError.Error())
		result.saveError = engine.catalogSaveError
		result.Fresh, result.HasMore = false, true
	}
	return result
}

func mergeNativeCatalog(first, second []LibraryDrama) []LibraryDrama {
	items := append([]LibraryDrama{}, first...)
	indices := make(map[string]int, len(items))
	for index, item := range items {
		indices[item.ID] = index
	}
	for _, item := range second {
		if index, found := indices[item.ID]; found {
			items[index] = MergeNativeDrama(items[index], item)
		} else {
			indices[item.ID] = len(items)
			items = append(items, item)
		}
	}
	return items
}

func MergeNativeDrama(previous, fresh LibraryDrama) LibraryDrama {
	previous, fresh = migrateNativeDrama(previous), migrateNativeDrama(fresh)
	if previous.ID != "" && fresh.ID != previous.ID {
		return fresh
	}
	if fresh.Source == "" {
		fresh.Source = previous.Source
	}
	if fresh.SourceID == "" {
		fresh.SourceID = previous.SourceID
	}
	if fresh.Title == "" || fresh.Title == "短剧" {
		fresh.Title = previous.Title
	}
	if fresh.Description == "" {
		fresh.Description = previous.Description
	}
	if fresh.Cover == "" {
		fresh.Cover = previous.Cover
	}
	if fresh.Category == "" || nativeGenericCategory(fresh.Category) && previous.Category != "" && !nativeGenericCategory(previous.Category) {
		fresh.Category = previous.Category
	}
	if fresh.Episodes == 0 {
		fresh.Episodes = previous.Episodes
	}
	if fresh.VIP == nil {
		fresh.VIP = previous.VIP
	}
	if fresh.Heat == "" {
		fresh.Heat = previous.Heat
	}
	if fresh.Views == "" {
		fresh.Views = previous.Views
	}
	if fresh.OnlineDate == "" {
		fresh.OnlineDate = previous.OnlineDate
	}
	if fresh.Score == "" {
		fresh.Score = previous.Score
	}
	if len(fresh.Tags) == 0 {
		fresh.Tags = previous.Tags
	}
	if fresh.ReleaseStatus == "" || fresh.ReleaseStatus == "unknown" {
		fresh.ReleaseStatus = previous.ReleaseStatus
	}
	if fresh.Badge == "" {
		fresh.Badge = previous.Badge
	}
	return fresh
}

func nativeGenericCategory(category string) bool {
	switch category {
	case "短剧", "真人剧", "漫剧", "AI剧", "AI 剧", "AI短剧", "AI 短剧", "AI漫剧", "AI 漫剧":
		return true
	}
	return false
}

func (engine *nativeEngine) saveCatalogCache(source string, result *nativeCatalogResult) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	previous := engine.catalogs[source]
	state := engine.catalogStates[source]
	var items []LibraryDrama
	if result.Page == 1 {
		items = result.Items
		if len(previous) > 0 {
			old := make(map[string]LibraryDrama, len(previous))
			for _, item := range previous {
				old[item.ID] = item
			}
			for index, item := range items {
				items[index] = MergeNativeDrama(old[item.ID], item)
			}
			fresh := make(map[string]bool, len(items))
			for _, item := range items {
				fresh[item.ID] = true
			}
			for _, item := range previous {
				if !fresh[item.ID] {
					items = append(items, item)
				}
			}
			result.Items = items
			result.Page = max(1, state.Page)
			descriptor, _ := registeredDescriptor(source)
			if !descriptor.ContinuousFeed && state.Page > 1 {
				result.HasMore = state.HasMore
			}
		}
	} else {
		items = mergeNativeCatalog(previous, result.Items)
		if result.Page < state.Page {
			result.Page, result.HasMore = state.Page, state.HasMore
		}
	}
	engine.catalogs[source] = items
	engine.commitProviderStateLocked(source)
	if result.Warning != "" {
		result.Page = max(1, state.Page)
		result.HasMore = true
	}
	state.Page, state.HasMore = result.Page, result.HasMore
	state.Warning = result.Warning
	if result.Warning == "" {
		state.UpdatedAt = time.Now()
		result.Fresh = true
	}
	engine.catalogStates[source] = state
	if base, _, categorized := strings.Cut(source, "|"); categorized {
		engine.catalogs[base] = mergeNativeCatalog(engine.catalogs[base], items)
	}
	err := engine.writeCatalogDiskLocked()
	result.saveError = err
	if err != nil {
		result.Warning = joinNativeWarnings(result.Warning, err.Error())
		result.Fresh, result.HasMore = false, true
	}
	return err
}

func (engine *nativeEngine) writeCatalogDiskLocked() error {
	trimNativeCatalogs(engine.catalogs)
	body, err := json.Marshal(nativeCatalogDisk{Version: 3, Catalogs: engine.catalogs, States: engine.catalogStates, Categories: engine.categoryOptions, ProviderStates: engine.providerStates, Recommendations: engine.recommendations})
	if err == nil && len(body) > nativeCatalogMaxBytes {
		err = errNativeCatalogLimit
	}
	if err == nil {
		err = writeNativeCacheFile(filepath.Join(engine.directory, "catalogs.json"), body)
	}
	engine.catalogSaveError = nativeSaveError("剧库", err)
	return engine.catalogSaveError
}

func writeNativeCacheFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cache-write-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.Write(data)
	if writeErr == nil {
		writeErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(temporary.Name(), path)
}

func (engine *nativeEngine) saveDetailMetadata(drama LibraryDrama) error {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	changed := false
	for _, items := range engine.catalogs {
		for index := range items {
			if items[index].ID == drama.ID {
				items[index] = MergeNativeDrama(items[index], drama)
				changed = true
			}
		}
	}
	if changed {
		return engine.writeCatalogDiskLocked()
	}
	return nil
}
