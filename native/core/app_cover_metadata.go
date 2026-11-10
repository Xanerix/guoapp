package core

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var CoverMetaTag = regexp.MustCompile(`(?is)<meta\b[^>]*>`)

func (d *Downloader) nativeCoverAddress(ctx context.Context, drama LibraryDrama) (string, error) {
	source, id, valid := SplitProviderDramaID(drama.ID)
	if !valid {
		return "", errors.New("无效的剧集 ID")
	}
	if p, ok := d.provider(source); ok {
		if resolver, ok := p.(CoverResolver); ok {
			return resolver.CoverAddress(ctx, drama, id)
		}
		fresh, _, err := p.Detail(ctx, id)
		return NativeNormalize(fresh).Cover, err
	}
	return "", errors.New("该站源暂无封面补齐接口")
}

func ProviderCoverAddress(value any, pageURL string) string {
	switch item := value.(type) {
	case []any:
		for _, value := range item {
			if address := ProviderCoverAddress(value, pageURL); address != "" {
				return address
			}
		}
	case map[string]any:
		for _, key := range []string{"url", "contentUrl", "thumbnailUrl"} {
			if address := ProviderCoverAddress(item[key], pageURL); address != "" {
				return address
			}
		}
	case string:
		if strings.TrimSpace(item) == "" {
			return ""
		}
		address, err := url.Parse(strings.TrimSpace(item))
		if err != nil {
			return ""
		}
		if base, err := url.Parse(pageURL); err == nil && base.IsAbs() {
			address = base.ResolveReference(address)
		}
		if ValidNativeCoverURL(address) {
			return address.String()
		}
	}
	return ""
}
