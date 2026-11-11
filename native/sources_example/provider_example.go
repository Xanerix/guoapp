package example

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"duanjuapp/native/core"
)

const (
	sourceExample        = "example"
	exampleDramasPerPage = 12
	examplePages         = 3
	exampleEpisodes      = 8
)

var exampleCategories = []core.Category{
	{ID: "romance", Name: "言情"},
	{ID: "suspense", Name: "悬疑"},
}

var exampleDescriptor = core.SourceDescriptor{
	ID:                sourceExample,
	Name:              "示例",
	Description:       "假数据示例站源，不访问网络",
	AllowEmptyCatalog: true,
	ValidCategory:     validExampleCategory,
}

var errExamplePlayback = errors.New("示例站源只有假数据，不能播放")

var _ core.SourceAPI1

type exampleProvider struct{}

func init() {
	core.Register(exampleDescriptor, func(core.Host) core.Provider { return exampleProvider{} })
}

func validExampleCategory(category string) bool {
	return category == "" || slices.ContainsFunc(exampleCategories, func(entry core.Category) bool { return entry.ID == category })
}

func (exampleProvider) Descriptor() core.SourceDescriptor { return exampleDescriptor }

func (exampleProvider) Categories(context.Context, bool) ([]core.Category, error) {
	return exampleCategories, nil
}

func (exampleProvider) CatalogPage(_ context.Context, page int, category string) ([]core.Drama, bool, error) {
	if !validExampleCategory(category) {
		return nil, false, fmt.Errorf("示例站源没有分类 %q", category)
	}
	if category == "" {
		category = exampleCategories[0].ID
	}
	return examplePage(page, func(index int) string { return category + "-" + strconv.Itoa(index) })
}

func (exampleProvider) Search(_ context.Context, page int, query string) ([]core.Drama, bool, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, false, nil
	}
	return examplePage(page, func(index int) string { return "search-" + strconv.Itoa(index) })
}

func (exampleProvider) Detail(_ context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	if strings.TrimSpace(sourceID) == "" {
		return core.Drama{}, nil, errors.New("示例站源剧集 ID 为空")
	}
	chapters := make([]core.Chapter, exampleEpisodes)
	for episode := range chapters {
		number := strconv.Itoa(episode + 1)
		chapters[episode] = core.Chapter{
			ID:             core.ProviderChapterID(sourceExample, sourceID, number),
			Source:         sourceExample,
			Title:          "第 " + number + " 集",
			CurrentEpisode: []byte(number),
		}
	}
	return exampleDrama(sourceID), chapters, nil
}

func (exampleProvider) ResolveMedia(context.Context, core.Task) (core.ProviderMedia, error) {
	return core.ProviderMedia{}, errExamplePlayback
}

func examplePage(page int, sourceID func(index int) string) ([]core.Drama, bool, error) {
	if page < 1 || page > examplePages {
		return nil, false, nil
	}
	dramas := make([]core.Drama, exampleDramasPerPage)
	for offset := range dramas {
		dramas[offset] = exampleDrama(sourceID((page-1)*exampleDramasPerPage + offset + 1))
	}
	return dramas, page < examplePages, nil
}

func exampleDrama(sourceID string) core.Drama {
	title := "示例短剧 " + sourceID
	return core.Drama{
		ID:           core.ProviderDramaID(sourceExample, sourceID),
		Source:       sourceExample,
		SourceID:     sourceID,
		Title:        title,
		Name:         title,
		Desc:         "示例站源生成的假数据。",
		Intro:        "示例站源生成的假数据。",
		TotalEpisode: exampleEpisodes,
	}
}
