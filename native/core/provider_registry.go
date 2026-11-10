package core

import (
	"context"
	"strings"
	"sync"
)

type SourceDescriptor struct {
	ID                 string
	Name               string
	Description        string
	Aliases            []string
	HostSuffixes       []string
	BaseURL            string
	AllowEmptyCatalog  bool
	NoHostRotation     bool
	KeepDetailEpisodes bool
	ValidCategory      func(category string) bool
}

type Provider interface {
	Descriptor() SourceDescriptor
	Categories(ctx context.Context, force bool) ([]nativeCategory, error)
	CatalogPage(ctx context.Context, page int, category string) ([]Drama, bool, error)
	Detail(ctx context.Context, sourceID string) (Drama, []Chapter, error)
	ResolveMedia(ctx context.Context, task Task) (providerMedia, error)
}

type Sited interface {
	BaseURL() string
}

type Searcher interface {
	Search(ctx context.Context, page int, query string) ([]Drama, bool, error)
}

type ProviderFactory func(d *Downloader) Provider

var providerRegistry = struct {
	sync.RWMutex
	factories   map[string]ProviderFactory
	descriptors map[string]SourceDescriptor
	aliases     map[string]string
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
	}
	return "", false
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
