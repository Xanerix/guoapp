package sources

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"duanjuapp/native/core"
)

var yeguoFilterFields = []string{"theme", "setting", "background", "time", "recommend"}

func validYeguoCategory(category string) bool {
	field, value, valid := strings.Cut(category, ":")
	if !valid || !core.WebProviderNumericID.MatchString(value) {
		return false
	}
	for _, known := range yeguoFilterFields {
		if field == known {
			return true
		}
	}
	return false
}

func yeguoFlag(value any) (bool, bool) {
	if flag, valid := value.(bool); valid {
		return flag, true
	}
	switch core.NativeText(value) {
	case "0", "false":
		return false, true
	case "1", "true":
		return true, true
	}
	return false, false
}

func firstPresent(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok && value != nil && core.NativeText(value) != "" {
			return value
		}
	}
	return nil
}

func (p *yeguoProvider) fetchYeguoCategories(ctx context.Context) ([]core.Category, error) {
	data, err := p.client.call(ctx, "/api/home/contentOptions", nil)
	if err != nil {
		return nil, err
	}
	filters, valid := data["video_filter"].(map[string]any)
	if !valid {
		return nil, errors.New("野果分类数据格式无效")
	}
	var categories []core.Category
	seen := map[string]bool{}
	for _, field := range yeguoFilterFields {
		filter, _ := filters[field].(map[string]any)
		options, _ := filter["list"].([]any)
		if len(options) > 300 {
			return nil, errors.New("野果分类条目过多")
		}
		for _, option := range options {
			row, _ := option.(map[string]any)
			value, name := core.MapString(row, "value"), core.MapString(row, "name")
			if value == "0" {
				continue
			}
			id := field + ":" + value
			if !validYeguoCategory(id) || name == "" || len([]rune(name)) > 48 || seen[id] {
				return nil, errors.New("野果分类数据包含无效或重复条目")
			}
			seen[id] = true
			categories = append(categories, core.Category{ID: id, Name: name})
		}
	}
	if len(categories) == 0 {
		return nil, errors.New("野果暂未返回分类")
	}
	return categories, nil
}

func yeguoDramaFromMap(row map[string]any, site string) (core.Drama, error) {
	id, title := core.MapString(row, "video_id", "id"), core.MapString(row, "title")
	if !core.WebProviderNumericID.MatchString(id) || title == "" {
		return core.Drama{}, errors.New("野果剧集资料不完整")
	}
	episodes := 0
	for _, key := range []string{"episode_count", "episodes", "total_serial"} {
		if count, valid := core.WebProviderInteger(row[key], 100000); valid {
			episodes = max(episodes, count)
		}
	}
	status := ""
	switch core.MapString(row, "serialize_status") {
	case "1":
		status = "ongoing"
	case "2":
		status = "finished"
	}
	var tags []string
	seenTags := map[string]bool{}
	if values, valid := row["tags"].([]any); valid {
		for _, value := range values {
			if tag, valid := value.(string); valid && len(tags) < 32 {
				tag = strings.TrimSpace(tag)
				if tag != "" && len([]rune(tag)) <= 48 && !seenTags[tag] {
					seenTags[tag] = true
					tags = append(tags, tag)
				}
			}
		}
	}
	cover := core.ProviderCoverAddress(row["cover"], site+"/")
	description := core.Truncate(core.MapString(row, "description"), 12000)
	drama := core.Drama{ID: core.ProviderDramaID(sourceYeguo, id), Source: sourceYeguo, SourceID: id,
		Title: core.Truncate(title, 512), Name: core.Truncate(title, 512), Desc: description, Intro: description,
		Cover: cover, CoverURL: cover, EpisodeCount: episodes, TotalEpisode: episodes,
		CategoryName: "短剧", ChannelName: "野果", Views: core.NormalizeViews(core.MapString(row, "play_count", "play_count_text")),
		Tags: tags, ReleaseStatus: status, OnlineDate: core.ProviderReleaseDate(core.MapString(row, "published_at", "created_at"))}
	if vip, known := yeguoFlag(row["is_vip"]); known {
		drama.VIP = &vip
	}
	return drama, nil
}

func (p *yeguoProvider) fetchYeguoCatalogPage(ctx context.Context, page int, category, query string) ([]core.Drama, bool, error) {
	if page < 1 || page > 1000000 || len(query) > 1024 || category != "" && !validYeguoCategory(category) {
		return nil, false, errors.New("野果目录查询参数无效")
	}
	route := "/api/theater/exploreList"
	values := url.Values{"page": {strconv.Itoa(page)}, "limit": {"20"}}
	if query != "" {
		route = "/api/search/result"
		values.Set("keyword", query)
		values.Set("tab", "video")
	} else if category != "" {
		field, value, _ := strings.Cut(category, ":")
		values.Set(field, value)
	}
	data, err := p.client.call(ctx, route, values)
	if err != nil {
		return nil, false, err
	}
	rows, valid := data["list"].([]any)
	if !valid || len(rows) > 500 {
		return nil, false, errors.New("野果目录格式无效")
	}
	if actualPage, pageValid := core.WebProviderInteger(firstPresent(data, "page", "page_num", "pageNum", "current_page", "currentPage"), 1000000); pageValid && actualPage != page {
		return nil, false, errors.New("野果分页信息无效，请重试")
	}
	limit, limitValid := core.WebProviderInteger(firstPresent(data, "limit", "page_size", "pageSize", "per_page", "perPage"), 500)
	if !limitValid || limit < 1 || limit < len(rows) {
		limit = len(rows)
	}
	total, totalValid := core.WebProviderInteger(firstPresent(data, "total", "total_count", "totalCount"), 100000000)
	if totalValid && total < len(rows) {
		totalValid = false
	}
	hasMore := limit > 0 && len(rows) >= limit
	if totalValid && limit > 0 {
		hasMore = page < (total+limit-1)/limit
	}
	if data["has_more"] != nil {
		more, valid := yeguoFlag(data["has_more"])
		if !valid {
			return nil, false, errors.New("野果分页信息无效，请重试")
		}
		hasMore = more
	}
	if hasMore && len(rows) == 0 {
		return nil, false, errors.New("野果未返回应有的目录页，请重试")
	}
	items := make([]core.Drama, 0, len(rows))
	seen := map[string]bool{}
	for _, value := range rows {
		row, _ := value.(map[string]any)
		drama, err := yeguoDramaFromMap(row, p.client.siteURL())
		if err != nil {
			return nil, false, err
		}
		if seen[drama.ID] {
			return nil, false, errors.New("野果目录包含重复剧集")
		}
		seen[drama.ID] = true
		items = append(items, drama)
	}
	return items, hasMore, nil
}

func yeguoEpisodePage(site, id string, episode int) string {
	address := site + "/drama/video/" + id + "/"
	if episode > 1 {
		address += "ep-" + strconv.Itoa(episode) + "/"
	}
	return address
}

func (p *yeguoProvider) fetchYeguoDetail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	if !core.WebProviderNumericID.MatchString(sourceID) {
		return core.Drama{}, nil, errors.New("野果剧集 ID 无效")
	}
	row, err := p.client.call(ctx, "/api/playlet/detail", url.Values{
		"video_id": {sourceID}, "id": {sourceID}, "episode_id": {"0"}, "related_limit": {"0"},
	})
	if err != nil {
		return core.Drama{}, nil, err
	}
	site := p.client.siteURL()
	drama, err := yeguoDramaFromMap(row, site)
	if err != nil {
		return core.Drama{}, nil, err
	}
	if drama.SourceID != sourceID {
		return core.Drama{}, nil, errors.New("野果详情与请求剧集不符")
	}
	episodes, valid := row["episodes"].([]any)
	if !valid || len(episodes) > 10000 {
		return core.Drama{}, nil, errors.New("野果分集目录格式无效")
	}
	chapters := make([]core.Chapter, 0, len(episodes))
	seenIDs, seenNumbers := map[string]bool{}, map[int]bool{}
	for _, value := range episodes {
		episode, _ := value.(map[string]any)
		if advertisement, _ := yeguoFlag(episode["is_adv"]); advertisement {
			continue
		}
		id := core.MapString(episode, "id")
		number, valid := core.WebProviderInteger(episode["sort"], 100000)
		if !core.WebProviderNumericID.MatchString(id) || !valid || number < 1 || seenIDs[id] || seenNumbers[number] {
			return core.Drama{}, nil, errors.New("野果分集编号无效或重复，请刷新详情")
		}
		seenIDs[id], seenNumbers[number] = true, true
		title := core.FirstNonEmpty(core.MapString(episode, "title"), fmt.Sprintf("第 %d 集", number))
		chapters = append(chapters, core.Chapter{ID: core.ProviderChapterID(sourceYeguo, sourceID, id), Source: sourceYeguo,
			Title: core.Truncate(title, 256), CurrentEpisode: core.RawEpisode(number), VideoURL: "yeguo-play://" + id,
			PageURL: yeguoEpisodePage(site, sourceID, number), Referer: site + "/"})
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		left, _ := strconv.Atoi(chapters[i].EpisodeString(i + 1))
		right, _ := strconv.Atoi(chapters[j].EpisodeString(j + 1))
		return left < right
	})
	return drama, chapters, nil
}

func yeguoMediaQuality(resolution string) int {
	parts := strings.Split(strings.ToLower(resolution), "x")
	if len(parts) != 2 {
		return 0
	}
	width, widthValid := core.WebProviderInteger(parts[0], 16384)
	height, heightValid := core.WebProviderInteger(parts[1], 16384)
	if !widthValid || !heightValid || width < 1 || height < 1 {
		return 0
	}
	return min(width, height)
}

func (p *yeguoProvider) resolveYeguoMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	source, sourceID, valid := core.SplitProviderDramaID(task.DramaID)
	prefix := core.ProviderChapterID(sourceYeguo, sourceID, "")
	if !valid || source != sourceYeguo || !core.WebProviderNumericID.MatchString(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return core.ProviderMedia{}, errors.New("野果播放分集信息无效，请刷新详情")
	}
	episodeID := strings.TrimPrefix(task.Chapter.ID, prefix)
	if !core.WebProviderNumericID.MatchString(episodeID) {
		return core.ProviderMedia{}, errors.New("野果播放分集 ID 无效")
	}
	row, err := p.client.call(ctx, "/api/playlet/play", url.Values{
		"playlet_id": {sourceID}, "video_id": {sourceID}, "episode_id": {episodeID},
	})
	if err != nil {
		return core.ProviderMedia{}, err
	}
	if core.MapString(row, "playlet_id") != sourceID || core.MapString(row, "id") != episodeID {
		return core.ProviderMedia{}, errors.New("野果返回的播放地址与请求分集不符")
	}
	if advertisement, _ := yeguoFlag(row["is_adv"]); advertisement {
		return core.ProviderMedia{}, errors.New("野果未返回该集正片，请重试")
	}
	number, valid := core.WebProviderInteger(row["episode_sort"], 100000)
	if !valid || number < 1 {
		return core.ProviderMedia{}, errors.New("野果播放分集编号无效")
	}
	referer := yeguoEpisodePage(p.client.siteURL(), sourceID, number)
	quality := yeguoMediaQuality(core.MapString(row, "resolution"))
	var options []core.ProviderMedia
	seen := map[string]bool{}
	for _, field := range []string{"video_url", "video_url_h265"} {
		raw := core.MapString(row, field)
		if raw == "" {
			continue
		}
		address := core.ResolveProviderURL(referer, raw)
		if !core.IsProviderHTTPMediaURL(address) || seen[address] {
			continue
		}
		seen[address] = true
		media := core.ProviderMedia{URL: address, Referer: referer, Quality: quality}
		if duration, err := strconv.ParseFloat(core.MapString(row, "episode_duration"), 64); err == nil && duration > 0 && duration <= 604800 {
			media.Duration = time.Duration(duration * float64(time.Second))
		}
		options = append(options, media)
	}
	if len(options) == 0 {
		return core.ProviderMedia{}, errors.New("野果未提供该集播放地址，请重试或确认站源权限")
	}
	media := options[0]
	media.Variants = options
	return p.host.PrepareWebMedia(ctx, media, "野果")
}

type yeguoProvider struct {
	host   core.Host
	client *yeguoAPIClient
}

var yeguoDescriptor = core.SourceDescriptor{
	ID:                 sourceYeguo,
	Name:               "野果",
	Description:        "分类短剧 · 在线搜索",
	DefaultOff:         true,
	Aliases:            []string{"ygdj7.com", "www.ygdj7.com", "ygdj15.com", "www.ygdj15.com", "analyze.buxefaex.cc", "attract.vgbyziuz.cc", "delta.ygrwdsgt.cc", "yeguodj.com", "www.yeguodj.com"},
	HostSuffixes:       []string{".buxefaex.cc", ".fzchosdi.cc", ".lwblvxvy.cc", ".vgbyziuz.cc"},
	BaseURL:            yeguoBaseURL,
	AllowEmptyCatalog:  true,
	NoHostRotation:     true,
	KeepDetailEpisodes: true,
	ValidCategory:      validYeguoCategory,
}

func init() {
	core.Register(yeguoDescriptor, func(host core.Host) core.Provider {
		return &yeguoProvider{host: host, client: &yeguoAPIClient{host: host, site: yeguoBaseURL}}
	})
}

func (p *yeguoProvider) Descriptor() core.SourceDescriptor { return yeguoDescriptor }

func (p *yeguoProvider) BaseURL() string { return p.client.site }

func (p *yeguoProvider) Categories(ctx context.Context, force bool) ([]core.Category, error) {
	return p.fetchYeguoCategories(ctx)
}

func (p *yeguoProvider) CatalogPage(ctx context.Context, page int, category string) ([]core.Drama, bool, error) {
	return p.fetchYeguoCatalogPage(ctx, page, category, "")
}

func (p *yeguoProvider) Search(ctx context.Context, page int, query string) ([]core.Drama, bool, error) {
	return p.fetchYeguoCatalogPage(ctx, page, "", query)
}

func (p *yeguoProvider) Detail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	return p.fetchYeguoDetail(ctx, sourceID)
}

func (p *yeguoProvider) ResolveMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	return p.resolveYeguoMedia(ctx, task)
}

const sourceYeguo = "yeguo"
