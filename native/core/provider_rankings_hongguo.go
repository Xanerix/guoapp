package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
)

var hongguoRankingMetric = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?[万亿]?热度$`)

func (p *hongguoProvider) fetchHongguoRankingPage(ctx context.Context, board RankingBoard, page int) (RankingPage, error) {
	ctx, cancel := context.WithTimeout(ctx, 18*time.Second)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(attempt*attempt) * 300 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return RankingPage{}, ctx.Err()
			}
		}
		base := hongguoBaseURL
		address := base + "/rank/" + board.Path
		if page > 1 {
			address += "?page=" + strconv.Itoa(page)
		}
		response, err := p.host.FetchText(ctx, TextRequest{URL: address, Referer: base + "/", NoCache: attempt > 0})
		if err != nil {
			return RankingPage{}, err
		}
		result, err := parseHongguoRanking(response.Body, board, page)
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, errHongguoRankingIncomplete) {
			return RankingPage{}, err
		}
		lastErr = err
	}
	return RankingPage{}, lastErr
}

func hongguoRankingURLPage(address string, board RankingBoard) (int, bool) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.User != nil || strings.TrimRight(parsed.Path, "/") != "/rank/"+board.Path {
		return 0, false
	}
	if parsed.Host != "" && parsed.Hostname() != "hongguoduanju.com" && parsed.Hostname() != "www.hongguoduanju.com" {
		return 0, false
	}
	value := parsed.Query().Get("page")
	if value == "" {
		return 1, true
	}
	page, err := strconv.Atoi(value)
	return page, err == nil && page >= 1 && page <= 500
}

func hongguoRankingDramaID(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.User != nil || parsed.Path != "/detail" {
		return ""
	}
	if parsed.Host != "" && parsed.Hostname() != "hongguoduanju.com" && parsed.Hostname() != "www.hongguoduanju.com" {
		return ""
	}
	id := parsed.Query().Get("series_id")
	if !hongguoNumericID.MatchString(id) {
		return ""
	}
	return id
}

func parseHongguoRankingDocument(body string, board RankingBoard, page int, loader map[string]any) (RankingPage, error) {
	if len(body) > 4<<20 {
		return RankingPage{}, errHongguoRankingFormat
	}
	document, err := html.Parse(strings.NewReader(body))
	if err != nil {
		return RankingPage{}, errHongguoRankingIncomplete
	}
	identified := len(loader) > 0
	for _, node := range ProviderHTMLNodes(document, func(node *html.Node) bool {
		return node.Data == "link" && ProviderHTMLAttr(node, "rel") == "canonical"
	}) {
		actual, valid := hongguoRankingURLPage(ProviderHTMLAttr(node, "href"), board)
		if !valid || actual != page {
			return RankingPage{}, errHongguoRankingFormat
		}
		identified = true
	}
	if !identified {
		return RankingPage{}, errHongguoRankingIncomplete
	}
	pagination := ProviderHTMLNodes(document, func(node *html.Node) bool {
		return node.Data == "nav" && ProviderHTMLAttr(node, "aria-label") == "榜单分页"
	})
	if len(pagination) != 1 {
		return RankingPage{}, errHongguoRankingIncomplete
	}
	current := ProviderHTMLNodes(pagination[0], func(node *html.Node) bool {
		return ProviderHTMLAttr(node, "aria-current") == "page"
	})
	if len(current) != 1 || ProviderHTMLText(current[0]) != strconv.Itoa(page) {
		return RankingPage{}, errHongguoRankingFormat
	}
	result := RankingPage{Items: []RankingItem{}, UpdatedText: MapString(loader, "updatedText")}
	maximumPage := page
	for _, link := range ProviderHTMLNodes(pagination[0], func(node *html.Node) bool { return node.Data == "a" }) {
		actual, valid := hongguoRankingURLPage(ProviderHTMLAttr(link, "href"), board)
		if !valid {
			return RankingPage{}, errHongguoRankingFormat
		}
		if actual > maximumPage {
			maximumPage = actual
		}
		if ProviderHTMLAttr(link, "rel") == "next" {
			if actual != page+1 {
				return RankingPage{}, errHongguoRankingFormat
			}
			result.HasMore = true
		}
	}
	if !result.HasMore {
		if maximumPage != page {
			return RankingPage{}, errHongguoRankingFormat
		}
		result.TotalPages = page
	}
	if items, found := parseHongguoRankingJSONLD(body, board, page); found {
		result.Items = items
	} else {
		for _, list := range ProviderHTMLNodes(document, func(node *html.Node) bool {
			return node.Data == "ol" && ProviderHTMLAttr(node, "aria-label") != ""
		}) {
			articles := ProviderHTMLNodes(list, func(node *html.Node) bool {
				return node.Data == "article" && strings.HasPrefix(ProviderHTMLAttr(node, "aria-labelledby"), "rank-title-")
			})
			if len(articles) == 0 {
				continue
			}
			if len(result.Items) != 0 || len(articles) > 20 {
				return RankingPage{}, errHongguoRankingFormat
			}
			for _, article := range articles {
				item, valid := parseHongguoRankingArticle(article)
				if !valid {
					return RankingPage{}, errHongguoRankingIncomplete
				}
				result.Items = append(result.Items, item)
			}
		}
	}
	if len(result.Items) == 0 {
		return RankingPage{}, errHongguoRankingIncomplete
	}
	seen, previous := map[string]bool{}, (page-1)*20
	for _, item := range result.Items {
		if !hongguoNumericID.MatchString(item.Drama.SourceID) || item.Drama.Title == "" || item.Rank <= previous || item.Rank > page*20 || seen[item.Drama.SourceID] {
			return RankingPage{}, errHongguoRankingFormat
		}
		seen[item.Drama.SourceID], previous = true, item.Rank
	}
	if result.HasMore && len(result.Items) != 20 {
		return RankingPage{}, errHongguoRankingIncomplete
	}
	return result, nil
}

func parseHongguoRankingJSONLD(body string, board RankingBoard, page int) ([]RankingItem, bool) {
	for _, match := range rankingJSONLD.FindAllStringSubmatch(body, -1) {
		var value struct {
			Type  string `json:"@type"`
			URL   string `json:"url"`
			Items []struct {
				Position int    `json:"position"`
				Name     string `json:"name"`
				URL      string `json:"url"`
			} `json:"itemListElement"`
		}
		if json.Unmarshal([]byte(match[1]), &value) != nil || value.Type != "ItemList" {
			continue
		}
		actual, valid := hongguoRankingURLPage(value.URL, board)
		if !valid || actual != page || len(value.Items) == 0 || len(value.Items) > 20 {
			continue
		}
		items := make([]RankingItem, 0, len(value.Items))
		for _, row := range value.Items {
			id := hongguoRankingDramaID(row.URL)
			title := strings.TrimSpace(row.Name)
			items = append(items, RankingItem{Rank: row.Position, Drama: Drama{
				ID: ProviderDramaID(sourceHongguo, id), Source: sourceHongguo, SourceID: id,
				Title: title, Name: title, ChannelName: "红果",
			}})
		}
		return items, true
	}
	return nil, false
}

func parseHongguoRankingArticle(article *html.Node) (RankingItem, bool) {
	label := ProviderHTMLAttr(article, "aria-labelledby")
	id := strings.TrimPrefix(label, "rank-title-")
	if !hongguoNumericID.MatchString(id) {
		return RankingItem{}, false
	}
	headings := ProviderHTMLNodes(article, func(node *html.Node) bool {
		return (node.Data == "h2" || node.Data == "h3") && ProviderHTMLAttr(node, "id") == label
	})
	if len(headings) != 1 || headings[0].Parent == nil || hongguoRankingDramaID(ProviderHTMLAttr(headings[0].Parent, "href")) != id {
		return RankingItem{}, false
	}
	title := ProviderHTMLText(headings[0])
	if title == "" || len([]rune(title)) > 200 {
		return RankingItem{}, false
	}
	drama := Drama{ID: ProviderDramaID(sourceHongguo, id), Source: sourceHongguo, SourceID: id, Title: title, Name: title, ChannelName: "红果"}
	rank := 0
	for _, link := range ProviderHTMLNodes(article, func(node *html.Node) bool {
		return node.Data == "a" && strings.HasPrefix(ProviderHTMLAttr(node, "aria-label"), "查看")
	}) {
		if hongguoRankingDramaID(ProviderHTMLAttr(link, "href")) != id {
			return RankingItem{}, false
		}
		value, err := strconv.Atoi(ProviderHTMLText(link))
		if err == nil && value > 0 && (rank == 0 || rank == value) {
			rank = value
		}
		for _, image := range ProviderHTMLNodes(link, func(node *html.Node) bool { return node.Data == "img" }) {
			cover := hongguoCoverAddress(ProviderHTMLAttr(image, "src"))
			if cover != "" {
				drama.Cover, drama.CoverURL = cover, cover
				break
			}
		}
	}
	for _, node := range ProviderHTMLNodes(article, func(node *html.Node) bool { return node.Type == html.TextNode }) {
		text := strings.TrimSpace(node.Data)
		if hongguoRankingMetric.MatchString(text) {
			drama.Heat = text
		}
		if strings.HasPrefix(text, "评分") {
			drama.Score = strings.TrimSpace(strings.TrimPrefix(text, "评分"))
		}
	}
	for _, paragraph := range ProviderHTMLNodes(article, func(node *html.Node) bool { return node.Data == "p" }) {
		var tags []string
		for child := paragraph.FirstChild; child != nil; child = child.NextSibling {
			if child.Data == "span" && child.FirstChild != nil && child.FirstChild == child.LastChild && child.FirstChild.Type == html.TextNode {
				value := ProviderHTMLText(child)
				if value != "" && len([]rune(value)) <= 20 && !strings.Contains(value, "热度") {
					tags = append(tags, value)
				}
			}
		}
		if len(tags) > 0 {
			drama.Tags, drama.CategoryName = tags, tags[0]
		} else if paragraph.FirstChild != nil && paragraph.FirstChild == paragraph.LastChild && paragraph.FirstChild.Type == html.TextNode {
			drama.Desc = ProviderHTMLText(paragraph)
			drama.Intro = drama.Desc
		}
	}
	return RankingItem{Rank: rank, Drama: drama, Metric: drama.Heat}, rank > 0
}
