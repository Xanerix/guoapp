package core

import (
	"context"
	"errors"
	"strings"
)

type Category struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func nativeCatalogKey(source, category string) string {
	source = canonicalProviderSource(source)
	if category = strings.TrimSpace(category); category != "" {
		return source + "|" + category
	}
	return source
}

func ValidNativeCategory(source, category string) bool {
	if category == "" {
		return true
	}
	if len(category) > 128 || strings.ContainsAny(category, "|/\\\x00\r\n") {
		return false
	}
	if descriptor, ok := registeredDescriptor(source); ok {
		return descriptor.ValidCategory != nil && descriptor.ValidCategory(category)
	}
	return false
}

func (engine *nativeEngine) nativeCategories(ctx context.Context, source string, force bool) ([]Category, error) {
	source = canonicalProviderSource(source)
	all := []Category{{Name: "全部"}}
	p, ok := engine.downloader.provider(source)
	if !ok {
		return nil, errors.New("请选择有效站源")
	}
	version := providerModuleVersion(p)
	engine.mu.Lock()
	cached := append([]Category{}, engine.categoryOptions[source]...)
	current := engine.categoryVersions[source] == version
	engine.mu.Unlock()
	if len(cached) > 1 && current && !force {
		return cached, nil
	}
	categories, err := p.Categories(ctx, force)
	all = append(all, categories...)
	if errors.Is(err, ErrCategoriesIncomplete) {
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	if len(all) == 1 {
		return nil, errors.New("站源暂未返回内容分类，请稍后重试")
	}
	engine.rememberCategories(source, version, all)
	return all, nil
}

func (engine *nativeEngine) rememberCategories(source, version string, all []Category) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.categoryOptions == nil {
		engine.categoryOptions = map[string][]Category{}
	}
	if engine.categoryVersions == nil {
		engine.categoryVersions = map[string]string{}
	}
	engine.categoryOptions[source] = all
	engine.categoryVersions[source] = version
	engine.writeCatalogDiskLocked()
}

func providerModuleVersion(p Provider) string {
	if module, ok := p.(*jsProvider); ok {
		return module.module.manifest.Version
	}
	return ""
}
