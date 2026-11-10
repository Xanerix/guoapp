package core

import (
	"context"
	"errors"
	"strings"
)

type nativeCategory struct {
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

func validNativeCategory(source, category string) bool {
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

func (engine *nativeEngine) nativeCategories(ctx context.Context, source string, force bool) ([]nativeCategory, error) {
	source = canonicalProviderSource(source)
	all := []nativeCategory{{Name: "全部"}}
	engine.mu.Lock()
	cached := append([]nativeCategory{}, engine.categoryOptions[source]...)
	engine.mu.Unlock()
	if len(cached) > 1 && !force {
		return cached, nil
	}
	d := engine.downloader
	p, ok := d.provider(source)
	if !ok {
		return nil, errors.New("请选择有效站源")
	}
	categories, err := p.Categories(ctx, force)
	all = append(all, categories...)
	if errors.Is(err, errCategoriesIncomplete) {
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	if len(all) == 1 {
		return nil, errors.New("站源暂未返回内容分类，请稍后重试")
	}
	engine.rememberCategories(source, all)
	return all, nil
}

func (engine *nativeEngine) rememberCategories(source string, all []nativeCategory) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.categoryOptions == nil {
		engine.categoryOptions = map[string][]nativeCategory{}
	}
	engine.categoryOptions[source] = all
	engine.writeCatalogDiskLocked()
}
