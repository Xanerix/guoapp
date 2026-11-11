package sources

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"duanjuapp/native/core"
)

const (
	hongguoTagPrefix        = "tag:"
	hongguoTagLandingPath   = "/reading/bookapi/new_category/landing/v/"
	hongguoTagPanelCategory = "5000"
	hongguoTagPageSize      = 12
)

var hongguoTagPattern = regexp.MustCompile(`^tag:cate_[0-9]{1,9}$`)

type hongguoTagCursor struct {
	page    int
	offset  int
	session string
}

func hongguoTagQuery(offset int, session string) url.Values {
	query := url.Values{
		"genre_type": {"130"}, "query_gender": {"1"}, "category_type": {"5"}, "page_version": {"2"},
		"source": {"detail"}, "limit": {strconv.Itoa(hongguoTagPageSize)}, "offset": {strconv.Itoa(offset)},
		"client_req_type": {"3"},
	}
	if offset > 0 {
		query.Set("client_req_type", "2")
	}
	if session != "" {
		query.Set("session_id", session)
	}
	return query
}

func (p *hongguoProvider) fetchHongguoTagCategories(ctx context.Context) ([]core.Category, error) {
	query := hongguoTagQuery(0, "")
	query.Set("category_id", hongguoTagPanelCategory)
	query.Set("selected_item_from_front_page", hongguoTagPanelCategory)
	query.Set("is_merged_landing_page", "true")
	query.Set("category_new_page_715", "1")
	result, err := p.hongguoAppRequest(ctx, http.MethodGet, hongguoTagLandingPath, query, nil)
	if err != nil {
		return nil, err
	}
	var categories []core.Category
	seen := map[string]bool{}
	for _, value := range anyList(nestedMap(result, "data", "selector")["rows"]) {
		row, _ := value.(map[string]any)
		if core.MapString(row, "type") != "dialog_bottom" {
			continue
		}
		for _, value := range anyList(row["items"]) {
			item, _ := value.(map[string]any)
			id := hongguoTagPrefix + core.MapString(item, "selector_item_id")
			name := strings.TrimSpace(core.MapString(item, "show_name"))
			if name == "" || seen[id] || !hongguoTagPattern.MatchString(id) {
				continue
			}
			seen[id] = true
			categories = append(categories, core.Category{ID: id, Name: name})
		}
	}
	if len(categories) == 0 {
		return nil, errors.New("红果题材列表为空")
	}
	return categories, nil
}

func (p *hongguoProvider) fetchHongguoTagPage(ctx context.Context, category string, page int) ([]core.Drama, bool, error) {
	client := p.client
	client.mu.Lock()
	cursor := client.tagCursors[category]
	client.mu.Unlock()
	if cursor.page != page-1 {
		cursor = hongguoTagCursor{page: page - 1, offset: (page - 1) * hongguoTagPageSize}
	}
	query := hongguoTagQuery(cursor.offset, cursor.session)
	query.Set("category_id", "0")
	query.Set("selected_items", strings.TrimPrefix(category, hongguoTagPrefix))
	query.Set("selected_order", "new_sort_hot")
	result, err := p.hongguoAppRequest(ctx, http.MethodGet, hongguoTagLandingPath, query, nil)
	if err != nil {
		return nil, false, err
	}
	data := nestedMap(result, "data")
	rows := anyList(data["video_info"])
	dramas := make([]core.Drama, 0, len(rows))
	for _, row := range rows {
		if drama := hongguoDramaFromAny(row, "真人剧"); drama.ID != "" {
			dramas = append(dramas, drama)
		}
	}
	if len(rows) > 0 && len(dramas) == 0 {
		return nil, false, errors.New("红果题材未返回可识别的剧集")
	}
	more, _ := data["has_more"].(bool)
	next, parseErr := strconv.Atoi(core.MapString(data, "offset"))
	if parseErr != nil || next <= cursor.offset {
		next, more = cursor.offset+len(rows), more && len(rows) > 0
	}
	client.mu.Lock()
	client.tagCursors[category] = hongguoTagCursor{page: page, offset: next, session: core.MapString(data, "session_id")}
	client.mu.Unlock()
	return dramas, more, nil
}
