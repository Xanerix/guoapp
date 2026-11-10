package core

import (
	"context"
	"errors"
	"net/url"
	"time"
)

func (engine *nativeEngine) nativeNeedsExtraMetadata(drama nativeDrama) bool {
	if p, ok := engine.downloader.provider(drama.Source); ok {
		enricher, ok := p.(MetadataEnricher)
		return ok && enricher.NeedsExtraMetadata(drama)
	}
	return drama.Source == sourceHongguo && (drama.OnlineDate == "" || drama.Score == "")
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
	if p, ok := engine.downloader.provider(source); ok {
		return p.(MetadataEnricher).ExtraMetadata(ctx, drama, id)
	}
	var raw Drama
	switch source {
	case sourceHongguo:
		if !hongguoNumericID.MatchString(id) {
			return drama, errors.New("红果剧集 ID 无效")
		}
		if drama.OnlineDate == "" {
			body, err := engine.downloader.fetchProviderText(ctx, hongguoBaseURL+"/detail?series_id="+url.QueryEscape(id), hongguoBaseURL+"/")
			if err != nil {
				return drama, err
			}
			raw, err = parseHongguoSortDetail(body, id)
			if err != nil {
				return drama, err
			}
		}
	}
	if raw.ID == drama.ID {
		drama = mergeNativeDrama(drama, nativeNormalize(raw))
	}
	if source == sourceHongguo && drama.Score == "" {
		drama.Score = nativeDramaScore(engine.downloader.hongguoSearchScore(ctx, drama.ID, drama.Title))
	}
	return drama, ctx.Err()
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
