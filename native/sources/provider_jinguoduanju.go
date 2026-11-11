package sources

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"duanjuapp/native/core"
)

const (
	jinguoDuanjuHomeCategory = "home"
	jinguoDuanjuHeatCategory = "heat"
	jinguoDuanjuPageSize     = 24
)

func validJinguoDuanjuCategory(category string) bool {
	switch category {
	case "", jinguoDuanjuHomeCategory, jinguoDuanjuHeatCategory:
		return true
	}
	kind, id, ok := strings.Cut(category, ":")
	return ok && (kind == "nav" || kind == "tag") && core.WebProviderNumericID.MatchString(id)
}

func jinguoDuanjuActive(row map[string]any, key string) bool {
	value, ok := row[key]
	if !ok || value == nil {
		return true
	}
	flag, isBool := value.(bool)
	return isBool && flag
}

func (p *jinguoDuanjuProvider) fetchJinguoDuanjuCategories(ctx context.Context) ([]core.Category, error) {
	client := p.client
	navigation, err := client.get(ctx, "/top-navigation", nil, 1<<20)
	if err != nil {
		return nil, err
	}
	filters, filterErr := client.get(ctx, "/filter-config", nil, 1<<20)
	rows, valid := navigation.([]any)
	if !valid || len(rows) > 200 {
		return nil, errors.New("禁果短剧分类数据格式无效")
	}
	categories := []core.Category{{ID: jinguoDuanjuHomeCategory, Name: "推荐"}, {ID: jinguoDuanjuHeatCategory, Name: "热度"}}
	seenID := map[string]bool{jinguoDuanjuHomeCategory: true, jinguoDuanjuHeatCategory: true}
	seenName := map[string]bool{"推荐": true, "热度": true, "全部": true}
	add := func(id, name string) {
		name = strings.TrimSpace(name)
		if len(categories) >= 64 || !validJinguoDuanjuCategory(id) || name == "" || len([]rune(name)) > 24 || seenID[id] || seenName[name] {
			return
		}
		seenID[id] = true
		seenName[name] = true
		categories = append(categories, core.Category{ID: id, Name: name})
	}
	for _, value := range rows {
		row, _ := value.(map[string]any)
		if !jinguoDuanjuActive(row, "active") || !jinguoDuanjuActive(row, "valid") || strings.EqualFold(core.MapString(row, "targetType"), "SYSTEM") {
			continue
		}
		id := core.MapString(row, "id")
		if !core.WebProviderNumericID.MatchString(id) {
			continue
		}
		add("nav:"+id, core.MapString(row, "frontendName"))
	}
	if filterErr == nil {
		groups, _ := filters.([]any)
		if len(groups) > 80 {
			return nil, errors.New("禁果短剧分类数据格式无效")
		}
		for _, value := range groups {
			group, _ := value.(map[string]any)
			if !jinguoDuanjuActive(group, "active") || !jinguoDuanjuActive(group, "frontendVisible") {
				continue
			}
			tags, _ := group["tags"].([]any)
			if len(tags) > 300 {
				return nil, errors.New("禁果短剧分类条目过多")
			}
			for _, tagValue := range tags {
				tag, _ := tagValue.(map[string]any)
				if !jinguoDuanjuActive(tag, "active") || !jinguoDuanjuActive(tag, "frontendVisible") {
					continue
				}
				id := core.MapString(tag, "id")
				if !core.WebProviderNumericID.MatchString(id) {
					continue
				}
				add("tag:"+id, core.MapString(tag, "name"))
			}
		}
	}
	return categories, nil
}

func jinguoDuanjuCatalogQuery(category, query string) (url.Values, error) {
	query = strings.TrimSpace(query)
	if !validJinguoDuanjuCategory(category) || len(query) > 256 {
		return nil, errors.New("禁果短剧目录查询参数无效")
	}
	values := url.Values{"sort": {"latest"}}
	if query != "" {
		values.Set("q", query)
		return values, nil
	}
	switch category {
	case jinguoDuanjuHomeCategory:
		values.Set("displayArea", "HOME_FEED")
	case jinguoDuanjuHeatCategory:
		values.Set("sort", "heat")
	default:
		kind, id, _ := strings.Cut(category, ":")
		switch kind {
		case "nav":
			values.Set("navigationId", id)
		case "tag":
			values.Set("tags", id)
		}
	}
	return values, nil
}

func jinguoDuanjuDramaFromMap(row map[string]any, site, sourceID string) (core.Drama, error) {
	id, title := core.MapString(row, "id"), strings.TrimSpace(core.MapString(row, "title"))
	if !core.WebProviderNumericID.MatchString(id) || title == "" {
		return core.Drama{}, errors.New("禁果短剧剧集资料不完整")
	}
	if sourceID != "" && sourceID != id {
		return core.Drama{}, errors.New("禁果短剧详情与请求剧集不符")
	}
	episodes, _ := core.WebProviderInteger(row["episodeCount"], 100000)
	var tags []string
	seenTags := map[string]bool{}
	if values, valid := row["tags"].([]any); valid {
		for _, value := range values {
			tag := strings.TrimSpace(core.NativeText(value))
			if tag == "" || len([]rune(tag)) > 24 || seenTags[tag] || len(tags) >= 32 {
				continue
			}
			seenTags[tag] = true
			tags = append(tags, tag)
		}
	}
	description := core.Truncate(core.FirstNonEmpty(core.MapString(row, "description"), core.MapString(row, "subtitle")), 12000)
	cover := core.ProviderCoverAddress(firstPresent(row, "coverUrl", "cover"), site+"/")
	drama := core.Drama{ID: core.ProviderDramaID(sourceJinguoDuanju, id), Source: sourceJinguoDuanju, SourceID: id,
		Title: core.Truncate(title, 512), Name: core.Truncate(title, 512), Desc: description, Intro: description,
		Cover: cover, CoverURL: cover, EpisodeCount: episodes, TotalEpisode: episodes,
		CategoryName: core.FirstNonEmpty(firstTag(tags), "短剧"), ChannelName: "禁果短剧", Tags: tags,
		OnlineDate: core.ProviderReleaseDate(core.MapString(row, "publishedAt"))}
	if heat, valid := core.WebProviderInteger(row["heat"], 1000000000); valid && heat > 0 {
		drama.Heat = strconv.Itoa(heat)
	}
	switch strings.ToUpper(core.MapString(row, "filmType")) {
	case "FREE":
		vip := false
		drama.VIP = &vip
	case "":
	default:
		vip := true
		drama.VIP = &vip
	}
	return drama, nil
}

func firstTag(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return tags[0]
}

func jinguoDuanjuDramaRows(value any) ([]any, error) {
	switch data := value.(type) {
	case []any:
		if len(data) > 20000 {
			return nil, errors.New("禁果短剧目录格式无效")
		}
		return data, nil
	case map[string]any:
		for _, key := range []string{"dramas", "list", "items", "records"} {
			if rows, valid := data[key].([]any); valid {
				if len(rows) > 20000 {
					return nil, errors.New("禁果短剧目录格式无效")
				}
				return rows, nil
			}
		}
	}
	return nil, errors.New("禁果短剧目录格式无效")
}

func (p *jinguoDuanjuProvider) fetchJinguoDuanjuDramas(ctx context.Context, category, query string) ([]core.Drama, error) {
	values, err := jinguoDuanjuCatalogQuery(category, query)
	if err != nil {
		return nil, err
	}
	client := p.client
	decoded, err := client.get(ctx, "/dramas", values, 8<<20)
	if err != nil {
		return nil, err
	}
	rows, err := jinguoDuanjuDramaRows(decoded)
	if err != nil {
		return nil, err
	}
	items := make([]core.Drama, 0, len(rows))
	seen := map[string]bool{}
	for _, value := range rows {
		row, _ := value.(map[string]any)
		drama, err := jinguoDuanjuDramaFromMap(row, client.siteURL(), "")
		if err != nil || seen[drama.ID] {
			continue
		}
		seen[drama.ID] = true
		items = append(items, drama)
	}
	return items, nil
}

func jinguoDuanjuPage(items []core.Drama, page int) ([]core.Drama, bool, error) {
	if page < 1 || page > 1000000 {
		return nil, false, errors.New("禁果短剧目录页码无效")
	}
	start := (page - 1) * jinguoDuanjuPageSize
	if start >= len(items) {
		return []core.Drama{}, false, nil
	}
	end := min(start+jinguoDuanjuPageSize, len(items))
	pageItems := append([]core.Drama(nil), items[start:end]...)
	return pageItems, end < len(items), nil
}

func (p *jinguoDuanjuProvider) fetchJinguoDuanjuCatalogPage(ctx context.Context, page int, category, query string) ([]core.Drama, bool, error) {
	client := p.client
	key := category + "\x00" + strings.TrimSpace(query)
	items, cached := client.cachedListing(key)
	if page == 1 || !cached {
		var err error
		if items, err = p.fetchJinguoDuanjuDramas(ctx, category, query); err != nil {
			return nil, false, err
		}
		client.storeListing(key, items)
	}
	return jinguoDuanjuPage(items, page)
}

func jinguoDuanjuChapters(site, sourceID string, rows []any) ([]core.Chapter, error) {
	if len(rows) > 10000 {
		return nil, errors.New("禁果短剧分集目录格式无效")
	}
	chapters := make([]core.Chapter, 0, len(rows))
	seenIDs, seenNumbers := map[string]bool{}, map[int]bool{}
	for _, value := range rows {
		row, _ := value.(map[string]any)
		id := core.MapString(row, "id")
		number, valid := core.WebProviderInteger(row["number"], 100000)
		if !core.WebProviderNumericID.MatchString(id) || !valid || number < 1 || seenIDs[id] || seenNumbers[number] {
			return nil, errors.New("禁果短剧分集编号无效或重复，请刷新详情")
		}
		seenIDs[id], seenNumbers[number] = true, true
		title := core.FirstNonEmpty(core.MapString(row, "title"), fmt.Sprintf("第%d集", number))
		locked, _ := row["locked"].(bool)
		chapters = append(chapters, core.Chapter{ID: core.ProviderChapterID(sourceJinguoDuanju, sourceID, id), Source: sourceJinguoDuanju,
			Title: core.Truncate(title, 256), CurrentEpisode: core.RawEpisode(number), VideoURL: "jinguoduanju-play://" + id,
			Referer: site + "/", VIP: locked})
	}
	return chapters, nil
}

func (p *jinguoDuanjuProvider) fetchJinguoDuanjuDetail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	if !core.WebProviderNumericID.MatchString(sourceID) {
		return core.Drama{}, nil, errors.New("禁果短剧剧集 ID 无效")
	}
	client := p.client
	decoded, err := client.get(ctx, "/dramas/"+url.PathEscape(sourceID), nil, 4<<20)
	if err != nil {
		return core.Drama{}, nil, err
	}
	object, _ := decoded.(map[string]any)
	row, _ := object["drama"].(map[string]any)
	if row == nil {
		row = object
	}
	if description := core.MapString(object, "description"); description != "" && core.MapString(row, "description") == "" {
		row["description"] = description
	}
	drama, err := jinguoDuanjuDramaFromMap(row, client.siteURL(), sourceID)
	if err != nil {
		return core.Drama{}, nil, err
	}
	episodes, valid := object["episodes"].([]any)
	if !valid {
		return core.Drama{}, nil, errors.New("禁果短剧分集目录格式无效")
	}
	chapters, err := jinguoDuanjuChapters(client.siteURL(), sourceID, episodes)
	if err != nil {
		return core.Drama{}, nil, err
	}
	if count := len(chapters); count > 0 {
		if listed, valid := core.WebProviderInteger(drama.EpisodeCount, 100000); !valid || count > listed {
			drama.EpisodeCount, drama.TotalEpisode = count, count
		}
	}
	return drama, chapters, nil
}

func (p *jinguoDuanjuProvider) resolveJinguoDuanjuMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	source, sourceID, valid := core.SplitProviderDramaID(task.DramaID)
	prefix := core.ProviderChapterID(sourceJinguoDuanju, sourceID, "")
	if !valid || source != sourceJinguoDuanju || !core.WebProviderNumericID.MatchString(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return core.ProviderMedia{}, errors.New("禁果短剧播放分集信息无效，请刷新详情")
	}
	episodeID := strings.TrimPrefix(task.Chapter.ID, prefix)
	if !core.WebProviderNumericID.MatchString(episodeID) {
		return core.ProviderMedia{}, errors.New("禁果短剧播放分集 ID 无效")
	}
	client := p.client
	decoded, err := client.get(ctx, "/episodes/"+url.PathEscape(episodeID)+"/play", nil, 256<<10)
	if err != nil {
		return core.ProviderMedia{}, err
	}
	row, _ := decoded.(map[string]any)
	if id := core.MapString(row, "episodeId"); id != "" && id != episodeID {
		return core.ProviderMedia{}, errors.New("禁果短剧返回的播放地址与请求分集不符")
	}
	if allowance, valid := row["allowance"].(map[string]any); valid {
		if allowed, isBool := allowance["allowed"].(bool); isBool && !allowed {
			return core.ProviderMedia{}, errors.New("禁果短剧该集需要会员或解锁后才能播放")
		}
	}
	referer := client.siteURL() + "/"
	address := core.NormalizeProviderMediaURL(referer, core.MapString(row, "videoUrl"))
	if !core.IsProviderHTTPMediaURL(address) {
		return core.ProviderMedia{}, errors.New("禁果短剧未提供该集播放地址，请确认是否需要会员或解锁")
	}
	return p.host.PrepareWebMedia(ctx, core.ProviderMedia{URL: address, Referer: referer}, "禁果短剧")
}

type jinguoDuanjuProvider struct {
	host   core.Host
	client *jinguoDuanjuAPIClient
}

var jinguoDuanjuDescriptor = core.SourceDescriptor{
	ID:                 sourceJinguoDuanju,
	Name:               "禁果短剧",
	Description:        "推荐 · 分类短剧 · 在线搜索",
	DefaultOff:         true,
	Aliases:            []string{"jinguoduanju.app", "www.jinguoduanju.app", "play.jinguoduanju.app", "jgdj.app", "www.jgdj.app"},
	BaseURL:            jinguoDuanjuBaseURL,
	AllowEmptyCatalog:  true,
	KeepDetailEpisodes: true,
	ValidCategory:      validJinguoDuanjuCategory,
}

func init() {
	core.Register(jinguoDuanjuDescriptor, func(host core.Host) core.Provider {
		return &jinguoDuanjuProvider{host: host, client: &jinguoDuanjuAPIClient{host: host}}
	})
}

func (p *jinguoDuanjuProvider) Descriptor() core.SourceDescriptor { return jinguoDuanjuDescriptor }

func (p *jinguoDuanjuProvider) BaseURL() string {
	return core.FirstNonEmpty(jinguoDuanjuContentSite(p.client.configured), jinguoDuanjuBaseURL)
}

func (p *jinguoDuanjuProvider) Categories(ctx context.Context, force bool) ([]core.Category, error) {
	return p.fetchJinguoDuanjuCategories(ctx)
}

func (p *jinguoDuanjuProvider) CatalogPage(ctx context.Context, page int, category string) ([]core.Drama, bool, error) {
	return p.fetchJinguoDuanjuCatalogPage(ctx, page, category, "")
}

func (p *jinguoDuanjuProvider) Search(ctx context.Context, page int, query string) ([]core.Drama, bool, error) {
	return p.fetchJinguoDuanjuCatalogPage(ctx, page, "", query)
}

func (p *jinguoDuanjuProvider) Detail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	return p.fetchJinguoDuanjuDetail(ctx, sourceID)
}

func (p *jinguoDuanjuProvider) ResolveMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	return p.resolveJinguoDuanjuMedia(ctx, task)
}

const sourceJinguoDuanju = "jinguoduanju"
