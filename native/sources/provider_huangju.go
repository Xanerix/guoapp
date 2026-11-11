package sources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"duanjuapp/native/core"
)

const huangjuNewestCategory = "@new"

func validHuangjuID(value string) bool {
	return value != "" && len(value) <= 450 && strings.TrimSpace(value) == value && value != "." && value != ".." &&
		!strings.ContainsAny(value, "/\\:?#%|\"<>") && strings.IndexFunc(value, unicode.IsControl) < 0
}

func huangjuInteger(value any, maximum int) (int, bool) {
	text := core.NativeText(value)
	number, err := strconv.Atoi(text)
	return number, err == nil && number >= 0 && number <= maximum
}

func huangjuCategoryNames(row map[string]any) []string {
	var names []string
	seen := map[string]bool{}
	add := func(value any) {
		name := ""
		switch value := value.(type) {
		case map[string]any:
			name, _ = value["name"].(string)
		case string:
			name = value
		}
		name = strings.TrimSpace(name)
		if name != "" && len([]rune(name)) <= 48 && !seen[name] && len(names) < 24 {
			seen[name] = true
			names = append(names, name)
		}
	}
	add(row["category"])
	if entries, ok := row["categories"].([]any); ok {
		for _, entry := range entries {
			add(entry)
		}
	}
	return names
}

func huangjuDramaFromMap(row map[string]any, site, sourceID string) (core.Drama, error) {
	id := core.MapString(row, "id")
	slug := core.MapString(row, "slug")
	title, _ := row["title"].(string)
	if !validHuangjuID(id) || strings.TrimSpace(title) == "" {
		return core.Drama{}, errors.New("剧果返回的剧集信息不完整")
	}
	if sourceID == "" {
		if !validHuangjuID(slug) {
			return core.Drama{}, errors.New("剧果返回的剧集地址无效")
		}
		sourceID = slug + "-" + id
	}
	if !validHuangjuID(sourceID) || !strings.HasSuffix(sourceID, "-"+id) {
		return core.Drama{}, errors.New("剧果详情与请求剧集不符")
	}
	episodes := 0
	if row["totalEpisodes"] != nil {
		var valid bool
		episodes, valid = huangjuInteger(row["totalEpisodes"], 100000)
		if !valid {
			return core.Drama{}, errors.New("剧果集数格式无效")
		}
	}
	categories := huangjuCategoryNames(row)
	category := "短剧"
	if len(categories) > 0 {
		category = categories[0]
	}
	tags := append([]string{}, categories...)
	if region := core.MapString(row, "region"); region != "" {
		tags = append(tags, core.Truncate(region, 48))
	}
	if year, valid := huangjuInteger(row["year"], 9999); valid && year >= 1000 {
		tags = append(tags, strconv.Itoa(year)+"年")
	}
	status := core.MapString(row, "status")
	if status == "completed" {
		status = "finished"
	}
	if status != "finished" && status != "ongoing" {
		status = ""
	}
	description, _ := row["description"].(string)
	cover := core.ProviderCoverAddress(row["coverUrl"], site+"/")
	return core.Drama{
		ID: core.ProviderDramaID(sourceHuangju, sourceID), Source: sourceHuangju, SourceID: sourceID,
		Title: core.Truncate(title, 256), Name: core.Truncate(title, 256), Desc: core.Truncate(description, 12000), Intro: core.Truncate(description, 12000),
		Cover: cover, CoverURL: cover, TotalEpisode: episodes, EpisodeCount: episodes,
		Category: category, ChannelName: "剧果", Tags: tags, ReleaseStatus: status, Score: core.MapString(row, "score"),
	}, nil
}

func (p *huangjuProvider) fetchHuangjuCategories(ctx context.Context) ([]core.Category, error) {
	response, err := p.client.get(ctx, "/categories", nil, 256<<10)
	if err != nil {
		return nil, err
	}
	rows, valid := huangjuPayload(response.value).([]any)
	if !valid || len(rows) > 300 {
		return nil, errors.New("剧果分类数据格式无效")
	}
	categories := []core.Category{{ID: huangjuNewestCategory, Name: "最新"}}
	seen := map[string]bool{huangjuNewestCategory: true}
	for _, entry := range rows {
		row, _ := entry.(map[string]any)
		id, name := core.MapString(row, "slug"), core.MapString(row, "name")
		if id == "hot" {
			continue
		}
		if !core.ValidNativeCategory(sourceHuangju, id) || id == "" || name == "" || len([]rune(name)) > 48 || seen[id] {
			return nil, errors.New("剧果分类数据包含无效或重复条目")
		}
		seen[id] = true
		categories = append(categories, core.Category{ID: id, Name: name})
	}
	return categories, nil
}

func (p *huangjuProvider) fetchHuangjuCatalogPage(ctx context.Context, page int, category, query string) ([]core.Drama, bool, error) {
	if page < 1 || page > 1000000 || len(query) > 1024 || !core.ValidNativeCategory(sourceHuangju, category) {
		return nil, false, errors.New("剧果目录查询参数无效")
	}
	values := url.Values{"page": {strconv.Itoa(page)}}
	switch {
	case query != "":
		values.Set("q", query)
	case category == huangjuNewestCategory:
		values.Set("sort", "new")
	case category == "":
		values.Set("sort", "hot")
	default:
		values.Set("category", category)
	}
	response, err := p.client.get(ctx, "/dramas", values, 4<<20)
	if err != nil {
		return nil, false, err
	}
	row, _ := huangjuPayload(response.value).(map[string]any)
	rows, valid := row["items"].([]any)
	if !valid || len(rows) > 500 {
		return nil, false, errors.New("剧果目录数据格式无效")
	}
	pageSize := 20
	if row["pageSize"] != nil {
		pageSize, valid = huangjuInteger(row["pageSize"], 500)
		if !valid || pageSize < 1 {
			return nil, false, errors.New("剧果分页数据格式无效")
		}
	}
	if row["page"] != nil {
		actualPage, valid := huangjuInteger(row["page"], 1000000)
		if !valid || actualPage != page {
			return nil, false, errors.New("剧果返回的页码与请求不符")
		}
	}
	if len(rows) > pageSize {
		return nil, false, errors.New("剧果分页数据格式无效")
	}
	hasMore := len(rows) >= pageSize
	if row["total"] != nil {
		total, valid := huangjuInteger(row["total"], 100000000)
		if !valid || total < len(rows) {
			return nil, false, errors.New("剧果分页总数无效")
		}
		hasMore = page < (total+pageSize-1)/pageSize
	}
	if hasMore && len(rows) == 0 {
		return nil, false, errors.New("剧果未返回应有的目录页，请重试")
	}
	items := make([]core.Drama, 0, len(rows))
	seen := map[string]bool{}
	for _, entry := range rows {
		value, _ := entry.(map[string]any)
		drama, err := huangjuDramaFromMap(value, p.client.site, "")
		if err != nil {
			return nil, false, err
		}
		if seen[drama.ID] {
			return nil, false, errors.New("剧果目录包含重复剧集，请重试")
		}
		seen[drama.ID] = true
		items = append(items, drama)
	}
	return items, hasMore, nil
}

func (p *huangjuProvider) fetchHuangjuDetail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	if !validHuangjuID(sourceID) {
		return core.Drama{}, nil, errors.New("剧果剧集地址无效")
	}
	response, err := p.client.get(ctx, "/dramas/"+url.PathEscape(sourceID), nil, 8<<20)
	if err != nil {
		return core.Drama{}, nil, err
	}
	row, _ := huangjuPayload(response.value).(map[string]any)
	drama, err := huangjuDramaFromMap(row, p.client.site, sourceID)
	if err != nil {
		return core.Drama{}, nil, err
	}
	rows, valid := row["episodes"].([]any)
	if !valid || len(rows) > 10000 {
		return core.Drama{}, nil, errors.New("剧果分集目录格式无效")
	}
	chapters := make([]core.Chapter, 0, len(rows))
	seenIDs, seenNumbers := map[string]bool{}, map[int]bool{}
	for index, entry := range rows {
		episode, _ := entry.(map[string]any)
		if playable, ok := episode["playable"].(bool); ok && !playable {
			continue
		}
		id := core.MapString(episode, "id")
		if !validHuangjuID(id) || seenIDs[id] {
			return core.Drama{}, nil, errors.New("剧果分集目录包含无效或重复分集")
		}
		number := index + 1
		if episode["epNo"] != nil {
			var valid bool
			number, valid = huangjuInteger(episode["epNo"], 100000)
			if !valid {
				return core.Drama{}, nil, errors.New("剧果分集编号无效")
			}
			if number == 0 {
				number = index + 1
			}
		}
		if seenNumbers[number] {
			return core.Drama{}, nil, errors.New("剧果分集编号重复，请刷新目录")
		}
		seenIDs[id], seenNumbers[number] = true, true
		chapters = append(chapters, core.Chapter{
			ID: core.ProviderChapterID(sourceHuangju, sourceID, id), Source: sourceHuangju,
			Title: fmt.Sprintf("第 %d 集", number), CurrentEpisode: json.RawMessage(strconv.Itoa(number)),
			VideoURL: "huangju-play://" + url.PathEscape(id), Referer: p.client.site + "/",
		})
	}
	sort.SliceStable(chapters, func(i, j int) bool {
		left, _ := strconv.Atoi(chapters[i].EpisodeString(i + 1))
		right, _ := strconv.Atoi(chapters[j].EpisodeString(j + 1))
		return left < right
	})
	return drama, chapters, nil
}

var huangjuSignedCookie = regexp.MustCompile(`(?:^|[,;\s])CloudFront-(Policy|Signature|Key-Pair-Id)\s*=\s*([^;,\s"']+)`)

func huangjuExpiration(value any) time.Time {
	text := core.NativeText(value)
	if stamp, err := strconv.ParseInt(text, 10, 64); err == nil && stamp > 0 && stamp <= 253402300799000 {
		if stamp > 100000000000 {
			return time.UnixMilli(stamp)
		}
		return time.Unix(stamp, 0)
	}
	parsed, _ := time.Parse(time.RFC3339Nano, text)
	return parsed
}

func huangjuMediaCredentials(headers http.Header, address, site string, expiration any) (*core.MediaCredentials, error) {
	cookies := map[string]string{}
	expires := huangjuExpiration(expiration)
	rememberExpiry := func(value time.Time) {
		if !value.IsZero() && (expires.IsZero() || value.Before(expires)) {
			expires = value
		}
	}
	response := &http.Response{Header: headers}
	for _, cookie := range response.Cookies() {
		switch cookie.Name {
		case "CloudFront-Policy", "CloudFront-Signature", "CloudFront-Key-Pair-Id":
			cookies[cookie.Name] = cookie.Value
			rememberExpiry(cookie.Expires)
			if cookie.MaxAge != 0 {
				rememberExpiry(time.Now().Add(time.Duration(min(cookie.MaxAge, 86400*365)) * time.Second))
			}
		}
	}
	for _, header := range headers.Values("Set-Cookie") {
		for _, match := range huangjuSignedCookie.FindAllStringSubmatch(header, 12) {
			cookies["CloudFront-"+match[1]] = match[2]
		}
	}
	var values []string
	for _, name := range []string{"CloudFront-Policy", "CloudFront-Signature", "CloudFront-Key-Pair-Id"} {
		value := cookies[name]
		if value == "" || len(value) > 8192 || strings.ContainsAny(value, ",;\"\\") || strings.IndexFunc(value, func(r rune) bool { return r <= 32 || r >= 127 }) >= 0 {
			return nil, errors.New("剧果未返回完整的媒体凭证，请重试")
		}
		values = append(values, name+"="+value)
	}
	if !expires.IsZero() && !time.Now().Before(expires) {
		return nil, errors.New("剧果媒体凭证已过期，请检查设备时间后重试")
	}
	parsed, err := url.Parse(address)
	if err != nil || !core.IsProviderHTTPMediaURL(address) || parsed.User != nil {
		return nil, errors.New("剧果播放地址无效")
	}
	return &core.MediaCredentials{
		Cookie: strings.Join(values, "; "), Origin: core.ProviderMediaOrigin(parsed),
		Referer: site + "/", UserAgent: huangjuUserAgent, Expires: expires,
	}, nil
}

func (p *huangjuProvider) resolveHuangjuMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	source, sourceID, valid := core.SplitProviderDramaID(task.DramaID)
	prefix := core.ProviderChapterID(sourceHuangju, sourceID, "")
	if !valid || source != sourceHuangju || !validHuangjuID(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return core.ProviderMedia{}, errors.New("剧果播放分集信息无效，请刷新详情")
	}
	id := strings.TrimPrefix(task.Chapter.ID, prefix)
	if !validHuangjuID(id) {
		return core.ProviderMedia{}, errors.New("剧果分集地址无效")
	}
	response, err := p.client.get(ctx, "/play/"+url.PathEscape(id), nil, 64<<10)
	if err != nil {
		return core.ProviderMedia{}, err
	}
	row, _ := huangjuPayload(response.value).(map[string]any)
	address, _ := row["url"].(string)
	credentials, err := huangjuMediaCredentials(response.headers, address, p.client.site, row["expiresAt"])
	if err != nil {
		return core.ProviderMedia{}, err
	}
	media := core.ProviderMedia{URL: address, Referer: credentials.Referer, Credentials: credentials}
	parsed, _ := url.Parse(address)
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		media.Playlist, media.URL, err = p.host.FetchPlaylist(ctx, media.URL, media.Referer, credentials)
		if err != nil {
			return core.ProviderMedia{}, fmt.Errorf("获取剧果播放列表失败：%w", err)
		}
		media.Duration = core.M3u8Duration(media.Playlist)
	}
	return media, nil
}

type huangjuProvider struct {
	host   core.Host
	client *huangjuAPIClient
}

var huangjuDescriptor = core.SourceDescriptor{
	ID:                 sourceHuangju,
	Name:               "剧果",
	Description:        "热门 · 最新 · 分类短剧",
	DefaultOff:         true,
	Aliases:            []string{"huangju.net", "www.huangju.net", "api.huangju.net"},
	BaseURL:            huangjuBaseURL,
	AllowEmptyCatalog:  true,
	NoHostRotation:     true,
	KeepDetailEpisodes: true,
	ValidCategory:      validHuangjuCategory,
}

func init() {
	core.Register(huangjuDescriptor, func(host core.Host) core.Provider {
		return &huangjuProvider{host: host, client: &huangjuAPIClient{host: host, base: huangjuAPIBaseURL, site: huangjuBaseURL}}
	})
}

func (p *huangjuProvider) Descriptor() core.SourceDescriptor { return huangjuDescriptor }

func (p *huangjuProvider) BaseURL() string { return p.client.site }

func (p *huangjuProvider) Categories(ctx context.Context, force bool) ([]core.Category, error) {
	return p.fetchHuangjuCategories(ctx)
}

func (p *huangjuProvider) CatalogPage(ctx context.Context, page int, category string) ([]core.Drama, bool, error) {
	return p.fetchHuangjuCatalogPage(ctx, page, category, "")
}

func (p *huangjuProvider) Search(ctx context.Context, page int, query string) ([]core.Drama, bool, error) {
	return p.fetchHuangjuCatalogPage(ctx, page, "", query)
}

func (p *huangjuProvider) Detail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	return p.fetchHuangjuDetail(ctx, sourceID)
}

func (p *huangjuProvider) ResolveMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	return p.resolveHuangjuMedia(ctx, task)
}

func validHuangjuCategory(category string) bool {
	return category == huangjuNewestCategory || validHuangjuID(category) && !strings.HasPrefix(category, "@")
}

const sourceHuangju = "huangju"
