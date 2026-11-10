package core

import (
	"context"
	"errors"
	"time"
)

func (engine *nativeEngine) nativeNeedsExtraMetadata(drama nativeDrama) bool {
	if p, ok := engine.downloader.provider(drama.Source); ok {
		enricher, ok := p.(MetadataEnricher)
		return ok && enricher.NeedsExtraMetadata(drama)
	}
	return false
}

func (engine *nativeEngine) nativeExtraMetadata(ctx context.Context, drama nativeDrama) (nativeDrama, error) {
	source, id, valid := splitProviderDramaID(drama.ID)
	if !valid {
		return drama, errors.New("剧集信息无效")
	}
	if !engine.nativeNeedsExtraMetadata(drama) {
		return drama, nil
	}
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, backgroundCatalogKey{}, true), 15*time.Second)
	defer cancel()
	p, ok := engine.downloader.provider(source)
	if !ok {
		return drama, errors.New("请选择有效站源")
	}
	return p.(MetadataEnricher).ExtraMetadata(ctx, drama, id)
}

func (engine *nativeEngine) nativeFollowCheck(ctx context.Context, drama nativeDrama) (any, error) {
	ctx, cancel := context.WithTimeout(context.WithValue(ctx, backgroundCatalogKey{}, true), 20*time.Second)
	defer cancel()
	result, err := engine.nativeDetail(ctx, drama)
	if err != nil {
		return nil, err
	}
	return map[string]any{"drama": result.(map[string]any)["drama"]}, nil
}

func (engine *nativeEngine) nativeMetadata(ctx context.Context, drama nativeDrama) (any, error) {
	fresh, err := engine.nativeExtraMetadata(ctx, drama)
	if err != nil {
		return nil, err
	}
	warning := ""
	if err := engine.saveDetailMetadata(fresh); err != nil {
		warning = err.Error()
	}
	return map[string]any{"drama": fresh, "warning": warning}, nil
}
