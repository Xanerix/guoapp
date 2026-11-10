package core

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var coverMetaTag = regexp.MustCompile(`(?is)<meta\b[^>]*>`)

func (d *Downloader) nativeCoverAddress(ctx context.Context, drama nativeDrama) (string, error) {
	source, id, valid := splitProviderDramaID(drama.ID)
	if !valid {
		return "", errors.New("无效的剧集 ID")
	}
	if p, ok := d.provider(source); ok {
		if resolver, ok := p.(CoverResolver); ok {
			return resolver.CoverAddress(ctx, drama, id)
		}
		fresh, _, err := p.Detail(ctx, id)
		return nativeNormalize(fresh).Cover, err
	}
	switch source {
	case sourceHongguo:
		if !hongguoNumericID.MatchString(id) {
			return "", errors.New("无效的红果剧集 ID")
		}
		result, err := d.hongguoAppRequest(ctx, http.MethodPost, "/novel/player/video_detail/v1/", nil, map[string]any{"series_id": id})
		if err != nil {
			return "", err
		}
		row := nestedMap(result, "data", "video_data")
		if mapString(row, "series_id_str", "series_id") != id {
			return "", errors.New("红果详情与请求剧集不符")
		}
		return hongguoCoverAddress(mapString(row, "series_cover", "cover")), nil
	}
	return "", errors.New("该站源暂无封面补齐接口")
}

func providerCoverAddress(value any, pageURL string) string {
	switch item := value.(type) {
	case []any:
		for _, value := range item {
			if address := providerCoverAddress(value, pageURL); address != "" {
				return address
			}
		}
	case map[string]any:
		for _, key := range []string{"url", "contentUrl", "thumbnailUrl"} {
			if address := providerCoverAddress(item[key], pageURL); address != "" {
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
		if validNativeCoverURL(address) {
			return address.String()
		}
	}
	return ""
}

func hongguoCoverAddress(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 8192 {
			continue
		}
		if strings.HasPrefix(value, "//") {
			value = "https:" + value
		}
		parsed, err := url.Parse(value)
		if err == nil && validNativeCoverURL(parsed) {
			return value
		}
	}
	return ""
}
