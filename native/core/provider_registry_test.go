package core

import (
	"context"
	"reflect"
	"testing"
)

type registryTestProvider struct {
	descriptor SourceDescriptor
	provided   map[reflect.Type]bool
}

func (p *registryTestProvider) Descriptor() SourceDescriptor { return p.descriptor }
func (p *registryTestProvider) Categories(context.Context, bool) ([]Category, error) {
	return nil, nil
}
func (p *registryTestProvider) CatalogPage(context.Context, int, string) ([]Drama, bool, error) {
	return nil, false, nil
}
func (p *registryTestProvider) Detail(context.Context, string) (Drama, []Chapter, error) {
	return Drama{}, nil, nil
}
func (p *registryTestProvider) ResolveMedia(context.Context, Task) (ProviderMedia, error) {
	return ProviderMedia{}, nil
}
func (p *registryTestProvider) Search(context.Context, int, string) ([]Drama, bool, error) {
	return nil, false, nil
}
func (p *registryTestProvider) Suggestions(context.Context, string) ([]string, error) {
	return nil, nil
}
func (p *registryTestProvider) ProvidesCapability(capability reflect.Type) bool {
	return p.provided[capability]
}

func TestUnregisterDropsSourceAndCachedProvider(t *testing.T) {
	descriptor := SourceDescriptor{ID: "registrytest", Aliases: []string{"registry-alias"}, HostSuffixes: []string{".registrytest.invalid"}}
	factory := func(Host) Provider { return &registryTestProvider{descriptor: descriptor} }
	Register(descriptor, factory)
	t.Cleanup(func() { Unregister("registrytest") })

	downloader := &Downloader{}
	first, ok := downloader.provider("registry-alias")
	if !ok {
		t.Fatal("registered source has no provider")
	}
	if again, _ := downloader.provider("registrytest"); again != first {
		t.Fatal("provider was not cached")
	}

	Unregister("registrytest")
	if _, ok := downloader.provider("registrytest"); ok {
		t.Fatal("provider survived Unregister")
	}
	if _, ok := registeredSource("registry-alias"); ok {
		t.Fatal("alias survived Unregister")
	}
	if _, ok := registeredSource("cdn.registrytest.invalid"); ok {
		t.Fatal("host suffix survived Unregister")
	}
	for _, registered := range registeredDescriptors() {
		if registered.ID == "registrytest" {
			t.Fatal("descriptor survived Unregister")
		}
	}

	Register(descriptor, factory)
	replaced, ok := downloader.provider("registrytest")
	if !ok || replaced == first {
		t.Fatal("re-registered source reused the stale provider")
	}
}

func TestReregisteredSourceKeepsItsPlace(t *testing.T) {
	ids := []string{"registryorder-a", "registryorder-b"}
	for _, id := range ids {
		descriptor := SourceDescriptor{ID: id}
		Register(descriptor, func(Host) Provider { return &registryTestProvider{descriptor: descriptor} })
		t.Cleanup(func() { Unregister(id) })
	}
	Unregister(ids[0])
	descriptor := SourceDescriptor{ID: ids[0]}
	Register(descriptor, func(Host) Provider { return &registryTestProvider{descriptor: descriptor} })

	order := []string{}
	for _, registered := range registeredDescriptors() {
		if registered.ID == ids[0] || registered.ID == ids[1] {
			order = append(order, registered.ID)
		}
	}
	if !reflect.DeepEqual(order, ids) {
		t.Fatalf("order after re-register = %v, want %v", order, ids)
	}
}

func TestProviderAsHonoursCapabilityLimiter(t *testing.T) {
	var p Provider = &registryTestProvider{provided: map[reflect.Type]bool{reflect.TypeFor[Searcher](): true}}
	if _, ok := providerAs[Searcher](p); !ok {
		t.Fatal("declared capability was hidden")
	}
	if _, ok := providerAs[Suggester](p); ok {
		t.Fatal("undeclared capability was exposed")
	}
	if _, ok := providerAs[Ranker](p); ok {
		t.Fatal("unimplemented capability was exposed")
	}
}
