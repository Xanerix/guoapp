package sources

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"duanjuapp/native/core"
)

func (p *legacyProvider) fetchLegacyCatalogCategoryPage(ctx context.Context, page int, category string) ([]core.Drama, bool, error) {
	var tabs legacyTabList
	if err := p.fetchAPI(ctx, "/api/app/playlet-tab/all", nil, &tabs); err != nil {
		return nil, false, err
	}
	if len(tabs) == 0 {
		return nil, false, errors.New("黄果旧 API 未返回可用分类")
	}
	pageSize := max(1, legacyPageSize)
	seen := map[string]bool{}
	var items []core.Drama
	var failures []error
	hasMore := false
	for _, tab := range tabs {
		if tab.ID == "" || category != "" && category != tab.ID {
			continue
		}
		var result listResponse
		err := p.fetchAPI(ctx, "/api/app/playlet/home/tab/"+url.PathEscape(tab.ID), map[string]string{
			"pageNumber": strconv.Itoa(page), "pageSize": strconv.Itoa(pageSize), "tabSortType": "1",
		}, &result)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", tab.Name, err))
			hasMore = true
			if ctx.Err() != nil {
				break
			}
			continue
		}
		hasMore = hasMore || len(result.List) >= pageSize
		for _, drama := range result.List {
			if drama.ID == "" || seen[drama.ID] {
				continue
			}
			seen[drama.ID] = true
			drama.SourceID, drama.Source = drama.ID, sourceCloudFront
			drama.ID = core.ProviderDramaID(sourceCloudFront, drama.SourceID)
			drama.ChannelName = tab.Name
			drama.CategoryName = core.FirstNonEmpty(drama.CategoryName, drama.CategoryNameSnake, drama.Category, tab.Name)
			for _, cover := range []any{drama.CoverURL, drama.CoverURLSnake, drama.Cover, drama.ImageURL, drama.ImageURLSnake, drama.Image, drama.Img, drama.Pic, drama.Picture, drama.Poster, drama.Thumb, drama.Thumbnail} {
				if address := legacyCoverURL(cover); address != "" {
					drama.Cover, drama.CoverURL = address, address
					break
				}
			}
			items = append(items, drama)
		}
	}
	if category != "" {
		found := false
		for _, tab := range tabs {
			found = found || tab.ID == category
		}
		if !found {
			return nil, false, errors.New("此分类已调整，请刷新分类列表")
		}
	}
	return items, hasMore, errors.Join(failures...)
}

func (p *legacyProvider) fetchLegacyDetail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	var detail detailResponse
	if err := p.fetchAPI(ctx, "/api/app/playlet/detail/"+url.PathEscape(sourceID), nil, &detail); err != nil {
		return core.Drama{}, nil, err
	}
	chapters := detail.Chapters
	if len(chapters) == 0 {
		if err := p.fetchAPI(ctx, "/api/app/playlet-chapter/list/"+url.PathEscape(sourceID), nil, &chapters); err != nil {
			return core.Drama{}, nil, err
		}
	}
	chapters = core.UniqueChapters(chapters)
	for index := range chapters {
		chapter := &chapters[index]
		chapter.Source = sourceCloudFront
		chapter.ID = core.ProviderChapterID(sourceCloudFront, sourceID, core.FirstNonEmpty(chapter.ID, strconv.Itoa(index+1)))
		chapter.Referer = legacyFrontendURL + "/"
		if core.ChapterEpisodeNumber(*chapter, 0) <= 0 {
			chapter.CurrentEpisode = core.RawEpisode(index + 1)
		}
		chapter.Title = core.FirstNonEmpty(chapter.Title, "第"+chapter.EpisodeString(index+1)+"集")
	}
	core.SortProviderChapters(chapters)
	drama := detail.Drama
	drama.Title, drama.Name = detail.Title, detail.Name
	if drama.ID != "" && drama.ID != sourceID && drama.ID != core.ProviderDramaID(sourceCloudFront, sourceID) {
		return core.Drama{}, nil, errors.New("黄果旧版详情与请求剧集不符")
	}
	drama.ID, drama.Source, drama.SourceID = core.ProviderDramaID(sourceCloudFront, sourceID), sourceCloudFront, sourceID
	for _, cover := range []any{drama.CoverURL, drama.CoverURLSnake, drama.Cover, drama.ImageURL, drama.ImageURLSnake, drama.Image, drama.Img, drama.Pic, drama.Poster} {
		if address := legacyCoverURL(cover); address != "" {
			drama.Cover, drama.CoverURL = address, address
			break
		}
	}
	return drama, chapters, nil
}

func (p *legacyProvider) resolveLegacyMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	if task.Chapter.VideoURL == "" {
		return core.ProviderMedia{}, errors.New("此分集没有可用的播放地址")
	}
	var key []byte
	if p.cfg.AESKeyHex != "" {
		var err error
		key, err = hex.DecodeString(p.cfg.AESKeyHex)
		if err != nil || len(key) != 16 {
			return core.ProviderMedia{}, errors.New("aesKeyHex 必须是 16 字节 AES 密钥的 hex 编码")
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		access, err := p.legacyCredentials(ctx)
		if err != nil {
			return core.ProviderMedia{}, err
		}
		base, err := p.apiEndpoint(ctx)
		if err != nil {
			return core.ProviderMedia{}, err
		}
		address := task.Chapter.VideoURL
		if !core.IsProviderHTTPMediaURL(address) {
			address = base + "/api/app/vid/h5/m3u8/" + url.PathEscape(strings.Trim(address, "/")) + "?" + url.Values{
				"token": {access.Token}, "c": {core.FirstNonEmpty(p.cfg.CDNURL, core.DefaultCDNURL)},
			}.Encode()
		}
		playlist, finalURL, err := p.host.FetchPlaylist(ctx, address, legacyFrontendURL+"/", nil)
		if err = legacyPlaylistError(err); err != nil {
			var expired *legacyAPIError
			if attempt == 0 && access.Anonymous && errors.As(err, &expired) && expired.code == "5005" {
				p.invalidateLegacyToken(access)
				continue
			}
			return core.ProviderMedia{}, err
		}
		return core.ProviderMedia{URL: finalURL, Playlist: playlist, Referer: legacyFrontendURL + "/", HLSKey: key, Duration: core.M3u8Duration(playlist)}, nil
	}
	return core.ProviderMedia{}, errors.New("黄果旧 API 的播放会话已失效，请稍后重试")
}

const (
	legacySessionFile = "huangguo-session.json"
	legacyPageSize    = 30
)

type legacyConfig struct {
	Token        string
	AESKeyHex    string
	InterfaceKey string
	ParamKey     string
	ParamIV      string
	APIBase      string
	CDNURL       string
	Retries      int
}

type legacyProvider struct {
	host        core.Host
	cfg         legacyConfig
	client      *legacyAPIClient
	apiMu       sync.Mutex
	apiBase     string
	apiFailures map[string]time.Time
}

func (p *legacyProvider) text(ctx context.Context, address, referer string) (string, error) {
	response, err := p.host.FetchText(ctx, core.TextRequest{URL: address, Referer: referer})
	return response.Body, err
}

var legacyDescriptor = core.SourceDescriptor{
	ID:            sourceCloudFront,
	Name:          "黄果旧版",
	Description:   "旧 API 剧库",
	Group:         "huangguo",
	GroupName:     "黄果",
	EntryName:     "旧版",
	DefaultOff:    true,
	BaseURL:       "https://d2pypzndaqisk.cloudfront.net",
	ValidCategory: func(category string) bool { return strings.TrimSpace(category) == category },
}

func init() {
	core.Register(legacyDescriptor, func(host core.Host) core.Provider {
		return &legacyProvider{host: host, cfg: legacyConfig{Retries: 2}, client: &legacyAPIClient{}}
	})
}

func (p *legacyProvider) Descriptor() core.SourceDescriptor { return legacyDescriptor }

func (p *legacyProvider) Categories(ctx context.Context, force bool) ([]core.Category, error) {
	var tabs legacyTabList
	if err := p.fetchAPI(ctx, "/api/app/playlet-tab/all", nil, &tabs); err != nil {
		return nil, err
	}
	var categories []core.Category
	for _, tab := range tabs {
		if core.ValidNativeCategory(sourceCloudFront, tab.ID) && tab.ID != "" && tab.Name != "" {
			categories = append(categories, core.Category{ID: tab.ID, Name: tab.Name})
		}
	}
	return categories, nil
}

func (p *legacyProvider) CatalogPage(ctx context.Context, page int, category string) ([]core.Drama, bool, error) {
	return p.fetchLegacyCatalogCategoryPage(ctx, page, category)
}

func (p *legacyProvider) Detail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	return p.fetchLegacyDetail(ctx, sourceID)
}

func (p *legacyProvider) ResolveMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	return p.resolveLegacyMedia(ctx, task)
}

func (p *legacyProvider) CoverAddress(ctx context.Context, drama core.LibraryDrama, id string) (string, error) {
	if !rankingSourceID.MatchString(id) {
		return "", errors.New("无效的黄果剧集 ID")
	}
	var raw core.Drama
	if err := p.fetchAPI(ctx, "/api/app/playlet/detail/"+url.PathEscape(id), nil, &raw); err != nil {
		return "", err
	}
	if raw.ID != "" && raw.ID != id && raw.ID != drama.ID {
		return "", errors.New("黄果详情与请求剧集不符")
	}
	for _, value := range []any{raw.Cover, raw.CoverURL, raw.CoverURLSnake, raw.Image, raw.ImageURL, raw.ImageURLSnake, raw.Img, raw.Pic, raw.Picture, raw.Poster, raw.Thumb, raw.Thumbnail} {
		if address := legacyCoverURL(value); address != "" {
			return address, nil
		}
	}
	return "", errors.New("详情暂未返回海报地址")
}

func (p *legacyProvider) RepairCover(drama core.LibraryDrama) string {
	return repairLegacyCoverURL(drama)
}

func (p *legacyProvider) CoverReferer(address string) string {
	if parsed, err := url.Parse(address); err == nil && (strings.HasSuffix(strings.ToLower(parsed.Hostname()), ".zdmhyg.cn") || strings.EqualFold(parsed.Hostname(), "pic.tuafjz.cn")) {
		return p.host.BaseURL(sourceHuangguoAI) + "/"
	}
	return p.host.BaseURL(sourceCloudFront) + "/"
}

const sourceCloudFront = "cloudfront"
