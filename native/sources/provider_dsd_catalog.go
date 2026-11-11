package sources

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"duanjuapp/native/core"
	"golang.org/x/net/html"
)

const (
	dsdBaseURL   = "https://www.dsd.com.se"
	dsdUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
)

var dsdBackgroundURL = regexp.MustCompile(`(?i)url\(\s*["']?([^"')]+)`)

type dsdPageLimit struct {
	pages     int
	updatedAt time.Time
}

type dsdCatalogState struct {
	mu         sync.Mutex
	categories []core.Category
	updatedAt  time.Time
	limits     map[string]dsdPageLimit
}

func dsdRouteParameters(pageURL, reference string) (string, map[string]string, bool) {
	page, err := url.Parse(pageURL)
	if err != nil || reference == "" {
		return "", nil, false
	}
	address, err := url.Parse(reference)
	if err != nil {
		return "", nil, false
	}
	address = page.ResolveReference(address)
	if address.User != nil || core.ProviderMediaOrigin(address) != core.ProviderMediaOrigin(page) {
		return "", nil, false
	}
	path := strings.TrimPrefix(address.Path, "/index.php")
	parts := strings.Split(strings.Trim(strings.TrimSuffix(path, ".html"), "/"), "/")
	if len(parts) < 2 || parts[0] != "vod" || len(parts)%2 != 0 {
		return "", nil, false
	}
	switch parts[1] {
	case "type", "show", "search", "play", "detail":
	default:
		return "", nil, false
	}
	parameters := map[string]string{}
	for index := 2; index+1 < len(parts); index += 2 {
		parameters[parts[index]] = parts[index+1]
	}
	for key, values := range address.Query() {
		if len(values) == 1 && parameters[key] == "" {
			parameters[key] = values[0]
		}
	}
	return parts[1], parameters, true
}

func parseDSDCategories(document *html.Node, pageURL string) ([]core.Category, error) {
	var categories []core.Category
	seen := map[string]bool{}
	for _, anchor := range core.ProviderHTMLNodes(document, func(node *html.Node) bool { return node.Data == "a" }) {
		action, values, valid := dsdRouteParameters(pageURL, core.ProviderHTMLAttr(anchor, "href"))
		id, name := values["id"], core.ProviderHTMLText(anchor)
		if !valid || action != "type" || !core.WebProviderNumericID.MatchString(id) || seen[id] || name == "" || len([]rune(name)) > 48 {
			continue
		}
		seen[id] = true
		categories = append(categories, core.Category{ID: id, Name: name})
	}
	if len(categories) == 0 || len(categories) > 32 {
		return nil, errors.New("帝果未返回有效的内容分类")
	}
	return categories, nil
}

func (p *dSDProvider) fetchDSDCategories(ctx context.Context, force bool) ([]core.Category, error) {
	state := &p.catalog
	state.mu.Lock()
	if len(state.categories) > 0 && !force && time.Since(state.updatedAt) < time.Hour {
		categories := append([]core.Category(nil), state.categories...)
		state.mu.Unlock()
		return categories, nil
	}
	state.mu.Unlock()
	site := p.site
	document, address, err := core.FetchHostPage(ctx, p.host, site+"/", site+"/", dsdUserAgent)
	if err != nil {
		return nil, err
	}
	categories, err := parseDSDCategories(document, address)
	if err != nil {
		return nil, err
	}
	state.mu.Lock()
	state.categories, state.updatedAt = append([]core.Category(nil), categories...), time.Now()
	if force {
		state.limits = nil
	}
	state.mu.Unlock()
	return categories, nil
}

func dsdCardCover(card *html.Node, pageURL string) string {
	images := core.ProviderHTMLNodes(card, func(node *html.Node) bool { return node.Data == "img" })
	for _, image := range images {
		for _, attribute := range []string{"data-src", "data-original"} {
			if address := core.ProviderCoverAddress(core.ProviderHTMLAttr(image, attribute), pageURL); address != "" {
				return address
			}
		}
	}
	for _, node := range core.ProviderHTMLNodes(card, func(node *html.Node) bool {
		return node == card || core.ProviderHTMLClass(node, "img-bg") || core.ProviderHTMLClass(node, "surface-img") ||
			core.ProviderHTMLClass(node, "openVip") || core.ProviderHTMLClass(node, "video-before-ad")
	}) {
		if match := dsdBackgroundURL.FindStringSubmatch(core.ProviderHTMLAttr(node, "style")); len(match) > 1 {
			if address := core.ProviderCoverAddress(strings.TrimSpace(match[1]), pageURL); address != "" {
				return address
			}
		}
	}
	for _, image := range images {
		raw := core.ProviderHTMLAttr(image, "src")
		if strings.HasPrefix(raw, "/video") || strings.Contains(raw, "/upload") {
			if address := core.ProviderCoverAddress(raw, pageURL); address != "" {
				return address
			}
		}
	}
	return ""
}

func parseDSDCards(document *html.Node, pageURL, category string) ([]core.Drama, error) {
	items := []core.Drama{}
	seen := map[string]bool{}
	for _, card := range core.ProviderHTMLNodes(document, func(node *html.Node) bool {
		return node.Data == "a" && (core.ProviderHTMLClass(node, "video-item") || core.ProviderHTMLClass(node, "video-surface"))
	}) {
		action, values, valid := dsdRouteParameters(pageURL, core.ProviderHTMLAttr(card, "href"))
		id := values["id"]
		if !valid || action != "play" && action != "detail" || !core.WebProviderNumericID.MatchString(id) || seen[id] {
			continue
		}
		title := core.FirstNonEmpty(core.ProviderHTMLText(core.ProviderHTMLFirstClass(card, "video-desc", "surface-title", "video-title", "title")), core.ProviderHTMLAttr(card, "title"))
		if title == "" {
			for _, image := range core.ProviderHTMLNodes(card, func(node *html.Node) bool { return node.Data == "img" }) {
				if title = strings.TrimSpace(core.ProviderHTMLAttr(image, "alt")); title != "" {
					break
				}
			}
		}
		if title == "" {
			return nil, errors.New("帝果目录缺少视频名称")
		}
		seen[id] = true
		cover := dsdCardCover(card, pageURL)
		views := strings.TrimSuffix(core.ProviderHTMLText(core.ProviderHTMLFirstClass(card, "video-item-tag-hits", "counts")), "次")
		drama := core.Drama{ID: core.ProviderDramaID(sourceDSD, id), Source: sourceDSD, SourceID: id,
			Title: core.Truncate(title, 1024), Name: core.Truncate(title, 1024), Cover: cover, CoverURL: cover,
			CategoryName: core.FirstNonEmpty(category, "视频"), ChannelName: "帝果", Views: views}
		if core.ProviderHTMLFirstClass(card, "video-item-tag-is-vip", "vip-tag") != nil {
			vip := true
			drama.VIP = &vip
		}
		items = append(items, drama)
		if len(items) > 500 {
			return nil, errors.New("帝果目录条目过多")
		}
	}
	return items, nil
}

func dsdPagination(document *html.Node, pageURL, category, query string, page int) (int, error) {
	pagination := core.ProviderHTMLFirstClass(document, "el-pagination", "pagination", "mac_pages")
	if pagination == nil {
		if page != 1 {
			return 0, errors.New("帝果未返回请求页码，请重试")
		}
		return 1, nil
	}
	activePage := 0
	for _, node := range core.ProviderHTMLNodes(pagination, func(node *html.Node) bool {
		return core.ProviderHTMLClass(node, "active") && core.ProviderHTMLClass(node, "number") || core.ProviderHTMLClass(node, "page-current")
	}) {
		if current, valid := core.WebProviderInteger(core.ProviderHTMLText(node), 1000000); valid {
			activePage = current
			break
		}
	}
	if activePage != page {
		return 0, errors.New("帝果返回的页码与请求不符")
	}
	pages := page
	for _, anchor := range core.ProviderHTMLNodes(pagination, func(node *html.Node) bool { return node.Data == "a" }) {
		action, parameters, valid := dsdRouteParameters(pageURL, core.ProviderHTMLAttr(anchor, "href"))
		if !valid {
			continue
		}
		if query != "" {
			if action != "search" || parameters["wd"] != query {
				continue
			}
		} else if (action != "type" && action != "show") || parameters["id"] != category {
			continue
		}
		if value, valid := core.WebProviderInteger(parameters["page"], 1000000); valid {
			pages = max(pages, value)
		}
	}
	return pages, nil
}

func (p *dSDProvider) dsdPageLimit(category string) (int, bool) {
	state := &p.catalog
	state.mu.Lock()
	defer state.mu.Unlock()
	limit, found := state.limits[category]
	return limit.pages, found && time.Since(limit.updatedAt) < 15*time.Minute
}

func (p *dSDProvider) fetchDSDPage(ctx context.Context, page int, category core.Category, query string) ([]core.Drama, bool, error) {
	if query == "" && page > 1 {
		limit, known := p.dsdPageLimit(category.ID)
		if !known {
			if _, _, err := p.fetchDSDPage(ctx, 1, category, ""); err != nil {
				return nil, false, err
			}
			limit, known = p.dsdPageLimit(category.ID)
		}
		if known && page > limit {
			return []core.Drama{}, false, nil
		}
	}
	site := p.site
	address := fmt.Sprintf("%s/index.php/vod/type/id/%s/page/%d.html", site, category.ID, page)
	if query != "" {
		address = site + "/index.php/vod/search.html?" + url.Values{"wd": {query}, "page": {strconv.Itoa(page)}}.Encode()
	}
	document, address, err := core.FetchHostPage(ctx, p.host, address, site+"/", dsdUserAgent)
	if err != nil {
		return nil, false, err
	}
	items, err := parseDSDCards(document, address, category.Name)
	if err != nil {
		return nil, false, err
	}
	if len(items) == 0 && core.ProviderHTMLFirstClass(document, "video-contaner", "nvyou-wrap", "van-list", "search-page", "lists") == nil {
		return nil, false, errors.New("帝果未返回内容列表，页面可能已变化")
	}
	pages, err := dsdPagination(document, address, category.ID, query, page)
	if err != nil {
		return nil, false, err
	}
	if len(items) == 0 && pages > page {
		return nil, false, errors.New("帝果未返回应有的视频条目，请重试")
	}
	if query == "" {
		state := &p.catalog
		state.mu.Lock()
		if state.limits == nil {
			state.limits = map[string]dsdPageLimit{}
		}
		state.limits[category.ID] = dsdPageLimit{pages: pages, updatedAt: time.Now()}
		state.mu.Unlock()
	}
	return items, pages > page, nil
}

func (p *dSDProvider) fetchDSDCatalogPage(ctx context.Context, page int, category, query string) ([]core.Drama, bool, error) {
	if page < 1 || page > 1000000 || len(query) > 1024 || category != "" && !core.WebProviderNumericID.MatchString(category) {
		return nil, false, errors.New("帝果目录查询参数无效")
	}
	if query != "" {
		return p.fetchDSDPage(ctx, page, core.Category{}, query)
	}
	categories, err := p.fetchDSDCategories(ctx, false)
	if err != nil {
		return nil, false, err
	}
	if category != "" {
		for _, item := range categories {
			if item.ID == category {
				return p.fetchDSDPage(ctx, page, item, "")
			}
		}
		return nil, false, errors.New("帝果分类已变化，请刷新分类")
	}
	type result struct {
		items []core.Drama
		more  bool
		err   error
	}
	results := make([]result, len(categories))
	slots := make(chan struct{}, 2)
	var group sync.WaitGroup
	for index, category := range categories {
		group.Add(1)
		go func(index int, category core.Category) {
			defer group.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				results[index].err = ctx.Err()
				return
			}
			results[index].items, results[index].more, results[index].err = p.fetchDSDPage(ctx, page, category, "")
		}(index, category)
	}
	group.Wait()
	items := []core.Drama{}
	seen := map[string]bool{}
	var failures []error
	hasMore, count := false, 0
	for index, result := range results {
		hasMore = hasMore || result.more || result.err != nil
		count = max(count, len(result.items))
		if result.err != nil {
			failures = append(failures, fmt.Errorf("%s：%w", categories[index].Name, result.err))
		}
	}
	for index := 0; index < count; index++ {
		for _, result := range results {
			if index < len(result.items) && !seen[result.items[index].ID] {
				item := result.items[index]
				seen[item.ID] = true
				items = append(items, item)
			}
		}
	}
	return items, hasMore, errors.Join(failures...)
}

func validDSDCategory(category string) bool { return core.WebProviderNumericID.MatchString(category) }
