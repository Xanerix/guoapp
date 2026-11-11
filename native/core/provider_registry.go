package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"sync"
)

type SourceDescriptor struct {
	ID                 string
	Name               string
	Description        string
	Group              string
	GroupName          string
	EntryName          string
	DefaultOff         bool
	SkipVIPPrompt      bool
	Aliases            []string
	HostSuffixes       []string
	MatchesHost        func(host string) bool
	BaseURL            string
	AllowEmptyCatalog  bool
	NoHostRotation     bool
	KeepDetailEpisodes bool
	ContinuousFeed     bool
	BrowserFingerprint bool
	ValidCategory      func(category string) bool
	Rankings           []RankingBoard
}

type Provider interface {
	Descriptor() SourceDescriptor
	Categories(ctx context.Context, force bool) ([]Category, error)
	CatalogPage(ctx context.Context, page int, category string) ([]Drama, bool, error)
	Detail(ctx context.Context, sourceID string) (Drama, []Chapter, error)
	ResolveMedia(ctx context.Context, task Task) (ProviderMedia, error)
}

var ErrSourceLogin = errors.New("站源需要登录")

type SourceLoginError string

func (err SourceLoginError) Error() string { return string(err) }

func (err SourceLoginError) Is(target error) bool { return target == ErrSourceLogin }

type Sited interface {
	BaseURL() string
}

type URLRotator interface {
	URLCandidates(rawURL string) []string
}

type CoverRepairer interface {
	RepairCover(drama LibraryDrama) string
}

type MediaAuthorizer interface {
	AuthorizesMedia(address *url.URL) bool
	MediaAuthorization(ctx context.Context, address *url.URL, referer string) (header, value string, err error)
	RejectMediaAuthorization(address *url.URL, value string)
}

type Searcher interface {
	Search(ctx context.Context, page int, query string) ([]Drama, bool, error)
}

type WarningSearcher interface {
	SearchWithWarning(ctx context.Context, page int, query string) ([]Drama, bool, string, error)
}

type CatalogStateful interface {
	CatalogState() json.RawMessage
	RestoreCatalogState(raw json.RawMessage)
}

type LibraryMoreKey struct{}

type LibraryKnownKey struct{}

type Suggester interface {
	Suggestions(ctx context.Context, query string) ([]string, error)
}

type RecommendationPage struct {
	Dramas  []Drama
	Cursor  json.RawMessage
	HasMore bool
}

type Recommender interface {
	ValidRecommendation(genre string, cursor json.RawMessage) error
	Recommendations(ctx context.Context, genre string, cursor json.RawMessage, seen []string) (RecommendationPage, error)
}

type DanmakuItem struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	TimeMS int64  `json:"timeMs"`
}

type DanmakuPage struct {
	EpisodeID string        `json:"episodeId"`
	Items     []DanmakuItem `json:"items"`
	StartMS   int64         `json:"startMs"`
	NextMS    int64         `json:"nextMs"`
	Total     int64         `json:"total"`
}

type DanmakuProvider interface {
	DanmakuIDs(task Task) (series, video string, ok bool)
	Danmaku(ctx context.Context, series, video string, startMS, durationMS int64) (DanmakuPage, error)
}

type DeviceProvider interface {
	Devices(ctx context.Context, register bool) (bool, error)
}

type AccountProvider interface {
	Account(ctx context.Context, command string, payload json.RawMessage) (any, error)
}

type Ranker interface {
	RankingPage(ctx context.Context, board RankingBoard, page int) (RankingPage, error)
}

type CoverResolver interface {
	CoverAddress(ctx context.Context, drama LibraryDrama, sourceID string) (string, error)
}

type CoverRefererProvider interface {
	CoverReferer(address string) string
}

type MetadataEnricher interface {
	NeedsExtraMetadata(drama LibraryDrama) bool
	ExtraMetadata(ctx context.Context, drama LibraryDrama, sourceID string) (LibraryDrama, error)
}

type Season struct {
	Number int
	Drama  Drama
}

type SeasonProvider interface {
	Seasons(ctx context.Context, sourceID string) ([]Season, error)
}

var ErrCategoriesIncomplete = errors.New("categories incomplete")

type ProviderFactory func(host Host) Provider

var providerRegistry = struct {
	sync.RWMutex
	factories   map[string]ProviderFactory
	descriptors map[string]SourceDescriptor
	aliases     map[string]string
	order       []string
}{factories: map[string]ProviderFactory{}, descriptors: map[string]SourceDescriptor{}, aliases: map[string]string{}}

func Register(descriptor SourceDescriptor, factory ProviderFactory) {
	id := strings.ToLower(strings.TrimSpace(descriptor.ID))
	if id == "" || factory == nil {
		panic("core.Register: empty source id or nil factory")
	}
	descriptor.ID = id
	providerRegistry.Lock()
	defer providerRegistry.Unlock()
	if _, dup := providerRegistry.factories[id]; dup {
		panic("core.Register: duplicate source " + id)
	}
	providerRegistry.factories[id] = factory
	providerRegistry.descriptors[id] = descriptor
	providerRegistry.order = append(providerRegistry.order, id)
	providerRegistry.aliases[id] = id
	for _, alias := range descriptor.Aliases {
		providerRegistry.aliases[strings.ToLower(strings.TrimSpace(alias))] = id
	}
}

func registeredSource(alias string) (string, bool) {
	providerRegistry.RLock()
	defer providerRegistry.RUnlock()
	alias = strings.ToLower(strings.TrimSpace(alias))
	if id, ok := providerRegistry.aliases[alias]; ok {
		return id, true
	}
	for id, descriptor := range providerRegistry.descriptors {
		for _, suffix := range descriptor.HostSuffixes {
			if strings.HasSuffix(alias, suffix) {
				return id, true
			}
		}
		if descriptor.MatchesHost != nil && descriptor.MatchesHost(alias) {
			return id, true
		}
	}
	return "", false
}

func registeredDescriptors() []SourceDescriptor {
	providerRegistry.RLock()
	defer providerRegistry.RUnlock()
	out := make([]SourceDescriptor, 0, len(providerRegistry.order))
	for _, id := range providerRegistry.order {
		out = append(out, providerRegistry.descriptors[id])
	}
	return out
}

func registeredDescriptor(source string) (SourceDescriptor, bool) {
	providerRegistry.RLock()
	defer providerRegistry.RUnlock()
	descriptor, ok := providerRegistry.descriptors[source]
	return descriptor, ok
}

func (d *Downloader) provider(source string) (Provider, bool) {
	source = canonicalProviderSource(source)
	d.providersMu.Lock()
	defer d.providersMu.Unlock()
	if p, ok := d.providers[source]; ok {
		return p, true
	}
	providerRegistry.RLock()
	factory, ok := providerRegistry.factories[source]
	providerRegistry.RUnlock()
	if !ok {
		return nil, false
	}
	if d.providers == nil {
		d.providers = map[string]Provider{}
	}
	p := factory(d)
	d.providers[source] = p
	return p, true
}

func ProviderDramaID(source, sourceID string) string {
	return source + ":" + strings.TrimSpace(sourceID)
}

func ProviderChapterID(source, sourceID, chapterKey string) string {
	return source + ":" + strings.TrimSpace(sourceID) + ":" + strings.TrimSpace(chapterKey)
}

func SplitProviderDramaID(id string) (source, sourceID string, ok bool) {
	source, sourceID, ok = strings.Cut(strings.TrimSpace(id), ":")
	source = canonicalProviderSource(source)
	if !ok || strings.TrimSpace(sourceID) == "" || !registeredProviderSource(source) {
		return "", "", false
	}
	return source, strings.TrimSpace(sourceID), true
}

func registeredProviderSource(source string) bool {
	_, ok := registeredDescriptor(canonicalProviderSource(source))
	return ok
}

func canonicalProviderSource(source string) string {
	trimmed := strings.ToLower(strings.TrimSpace(source))
	if id, ok := registeredSource(trimmed); ok {
		return id
	}
	return strings.TrimSpace(source)
}
