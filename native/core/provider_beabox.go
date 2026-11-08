package core

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	beaboxBaseURL      = "https://bh4066.top"
	beaboxPublishURL   = "https://beabox.cc"
	beaboxPermanentURL = "https://beabox.live"
	beaboxUserAgent    = "Mozilla/5.0 (Linux; Android 13; Pixel 7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Mobile Safari/537.36"
	beaboxKeyHex       = "f6322fa1c064370ea40c8cdc20649f8e"
	beaboxTagPrefix    = "tag:"
	beaboxPageSize     = 20
	beaboxPoolTTL      = 30 * time.Minute
	beaboxPoolRetryTTL = time.Minute
)

var (
	beaboxCipherKey   = mustBeaboxKey(beaboxKeyHex)
	beaboxDomainsExpr = regexp.MustCompile(`__ENC_DOMAINS\s*=\s*["']([A-Za-z0-9+/=_-]+)["']`)
	beaboxKeyExpr     = regexp.MustCompile(`__KEY\s*=\s*["']([^"']+)["']`)
	beaboxIDExpr      = regexp.MustCompile(`^[A-Za-z0-9]{1,64}$`)
	beaboxHostExpr    = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?)+$`)
	beaboxEmExpr      = regexp.MustCompile(`(?i)</?em\b[^>]*>`)
)

func mustBeaboxKey(raw string) []byte {
	key, err := hex.DecodeString(raw)
	if err != nil || len(key) != aes.BlockSize {
		panic("invalid beabox key")
	}
	return key
}

func validBeaboxID(id string) bool {
	return beaboxIDExpr.MatchString(strings.TrimSpace(id))
}

func validBeaboxCategory(category string) bool {
	if category == "" {
		return true
	}
	tag, found := strings.CutPrefix(category, beaboxTagPrefix)
	if !found {
		return false
	}
	tag = strings.TrimSpace(tag)
	return tag != "" && len([]rune(tag)) <= 24 && !strings.ContainsAny(tag, "|/\\\x00\r\n")
}

func beaboxTagName(category string) string {
	return strings.TrimSpace(strings.TrimPrefix(category, beaboxTagPrefix))
}

type beaboxSiteState struct {
	Version int      `json:"version"`
	Site    string   `json:"site"`
	Pool    []string `json:"pool"`
}

func beaboxSiteURL(raw string) (string, bool) {
	address, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || address.Scheme != "https" || address.User != nil || strings.Trim(address.Path, "/") != "" ||
		address.RawQuery != "" || address.Port() != "" || !beaboxHostExpr.MatchString(address.Hostname()) {
		return "", false
	}
	return "https://" + address.Hostname(), true
}

func (d *Downloader) beaboxStatePath() string {
	if d.cfg.dataDir == "" {
		return ""
	}
	return filepath.Join(d.cfg.dataDir, "beabox-sites.json")
}

func (d *Downloader) loadBeaboxStateLocked() {
	if d.beaboxLoaded {
		return
	}
	d.beaboxLoaded = true
	path := d.beaboxStatePath()
	if path == "" {
		return
	}
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()
	var state beaboxSiteState
	if json.NewDecoder(io.LimitReader(file, 64*1024)).Decode(&state) != nil || state.Version != 1 {
		return
	}
	if site, ok := beaboxSiteURL(state.Site); ok {
		d.beaboxSite = site
	}
	for _, raw := range state.Pool {
		if site, ok := beaboxSiteURL(raw); ok && !slices.Contains(d.beaboxPool, site) && len(d.beaboxPool) < 256 {
			d.beaboxPool = append(d.beaboxPool, site)
		}
	}
}

func (d *Downloader) saveBeaboxState(state beaboxSiteState) {
	path := d.beaboxStatePath()
	if path == "" {
		return
	}
	if data, err := json.Marshal(state); err == nil {
		_ = writeNativeCacheFile(path, data)
	}
}

func (d *Downloader) rememberBeaboxSite(site string) {
	d.beaboxMu.Lock()
	changed := d.beaboxSite != site
	d.beaboxSite = site
	state := beaboxSiteState{Version: 1, Site: site, Pool: d.beaboxPool}
	d.beaboxMu.Unlock()
	if changed {
		d.saveBeaboxState(state)
	}
}

func (d *Downloader) beaboxKnownSites() []string {
	d.beaboxMu.Lock()
	d.loadBeaboxStateLocked()
	site := d.beaboxSite
	d.beaboxMu.Unlock()
	var sites []string
	for _, value := range []string{site, d.cfg.BeaboxURL, beaboxBaseURL} {
		value = strings.TrimRight(strings.TrimSpace(value), "/")
		if value != "" && !slices.Contains(sites, value) {
			sites = append(sites, value)
		}
	}
	return sites
}

func (d *Downloader) beaboxDiscoveredSites(ctx context.Context) []string {
	d.beaboxDiscoverMu.Lock()
	defer d.beaboxDiscoverMu.Unlock()
	d.beaboxMu.Lock()
	d.loadBeaboxStateLocked()
	pool, next := d.beaboxPool, d.beaboxPoolNext
	d.beaboxMu.Unlock()
	if time.Now().Before(next) {
		return pool
	}
	sites := d.discoverBeaboxSites(ctx)
	if ctx.Err() != nil {
		return pool
	}
	d.beaboxMu.Lock()
	changed := len(sites) > 0 && !slices.Equal(sites, d.beaboxPool)
	if len(sites) > 0 {
		d.beaboxPool, d.beaboxPoolNext = sites, time.Now().Add(beaboxPoolTTL)
	} else {
		d.beaboxPoolNext = time.Now().Add(beaboxPoolRetryTTL)
	}
	pool = d.beaboxPool
	state := beaboxSiteState{Version: 1, Site: d.beaboxSite, Pool: pool}
	d.beaboxMu.Unlock()
	if changed {
		d.saveBeaboxState(state)
	}
	return pool
}

func (d *Downloader) discoverBeaboxSites(ctx context.Context) []string {
	for _, seed := range []string{beaboxPublishURL, beaboxPermanentURL} {
		status, body, err := d.beaboxFetch(ctx, http.MethodGet, seed+"/", seed+"/", nil)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil || status < 200 || status >= 300 {
			continue
		}
		if sites := parseBeaboxPublishPage(string(body)); len(sites) > 0 {
			return sites
		}
	}
	return nil
}

func (d *Downloader) beaboxFetch(ctx context.Context, method, address, referer string, payload []byte) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("User-Agent", beaboxUserAgent)
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	request.Header.Set("Referer", referer)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json")
	}
	response, err := d.doCatalogRequestWithTimeout(request, providerTimeout)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, providerMaxBodyBytes+1))
	if err != nil {
		return 0, nil, err
	}
	if len(data) > providerMaxBodyBytes {
		return 0, nil, errors.New("笔盒返回的数据过大")
	}
	return response.StatusCode, data, nil
}

func parseBeaboxPublishPage(body string) []string {
	encoded := beaboxDomainsExpr.FindStringSubmatch(body)
	key := beaboxKeyExpr.FindStringSubmatch(body)
	if len(encoded) < 2 || len(key) < 2 || key[1] == "" || len(key[1]) > 64 {
		return nil
	}
	raw, err := decodeBeaboxBase64(encoded[1])
	if err != nil || len(raw) == 0 || len(raw) > 1<<20 {
		return nil
	}
	cipherKey := []byte(key[1])
	for index := range raw {
		raw[index] ^= cipherKey[index%len(cipherKey)]
	}
	var hosts []string
	if json.Unmarshal(raw, &hosts) != nil || len(hosts) > 256 {
		return nil
	}
	var sites []string
	seen := map[string]bool{}
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if !beaboxHostExpr.MatchString(host) || seen[host] {
			continue
		}
		seen[host] = true
		sites = append(sites, "https://"+host)
	}
	return sites
}

func decodeBeaboxBase64(value string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, errors.New("base64 解码失败")
}

func (d *Downloader) beaboxCall(ctx context.Context, route string, query url.Values, payload any) (any, string, error) {
	method, path := http.MethodGet, "/api"+route
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, "", errors.New("笔盒请求参数无效")
		}
		method, body = http.MethodPost, encoded
	}
	tried := map[string]bool{}
	var lastErr error
	call := func(sites []string) (map[string]any, string) {
		for _, site := range sites {
			if tried[site] || ctx.Err() != nil {
				continue
			}
			tried[site] = true
			status, data, err := d.beaboxFetch(ctx, method, site+path, site+"/home", body)
			if err != nil {
				lastErr = err
				continue
			}
			envelope, err := parseBeaboxEnvelope(data)
			if err != nil {
				lastErr = fmt.Errorf("笔盒线路 %s 不可用（HTTP %d）", strings.TrimPrefix(site, "https://"), status)
				continue
			}
			return envelope, site
		}
		return nil, ""
	}
	envelope, site := call(d.beaboxKnownSites())
	if envelope == nil && ctx.Err() == nil {
		envelope, site = call(d.beaboxDiscoveredSites(ctx))
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if envelope == nil {
		if lastErr == nil {
			lastErr = errors.New("笔盒线路暂不可用，请稍后重试")
		}
		return nil, "", lastErr
	}
	d.rememberBeaboxSite(site)
	value, err := beaboxEnvelopeData(envelope)
	return value, site, err
}

func parseBeaboxEnvelope(data []byte) (map[string]any, error) {
	value, err := decodeBeaboxJSON(data)
	envelope, valid := value.(map[string]any)
	if err != nil || !valid || envelope["code"] == nil {
		return nil, errors.New("笔盒返回的数据格式无效")
	}
	return envelope, nil
}

func beaboxEnvelopeData(envelope map[string]any) (any, error) {
	if code, ok := webProviderInteger(envelope["code"], 1000000); !ok || code != 200 {
		message := truncate(cleanText(mapString(envelope, "msg")), 180)
		if message == "" {
			message = "请稍后重试"
		}
		return nil, fmt.Errorf("笔盒请求未完成：%s", message)
	}
	data := envelope["data"]
	if encoded, ok := data.(string); ok && isBeaboxCiphertext(encoded) {
		decoded, err := beaboxDecodeData(encoded)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	}
	return data, nil
}

func isBeaboxCiphertext(value string) bool {
	if len(value) < 64 || len(value)%2 != 0 {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", char) {
			return false
		}
	}
	return true
}

func beaboxDecodeData(encoded string) (any, error) {
	raw, err := hex.DecodeString(encoded)
	if err != nil || len(raw) < 2*aes.BlockSize || (len(raw)-aes.BlockSize)%aes.BlockSize != 0 {
		return nil, errors.New("笔盒接口数据解码失败")
	}
	block, err := aes.NewCipher(beaboxCipherKey)
	if err != nil {
		return nil, errors.New("笔盒接口数据解码失败")
	}
	plain := make([]byte, len(raw)-aes.BlockSize)
	cipher.NewCBCDecrypter(block, raw[:aes.BlockSize]).CryptBlocks(plain, raw[aes.BlockSize:])
	plain, err = pkcs7Unpad(plain, aes.BlockSize)
	if err != nil {
		return nil, errors.New("笔盒接口数据解码失败")
	}
	value, err := decodeBeaboxJSON(plain)
	if err != nil {
		return nil, errors.New("笔盒接口数据解码失败")
	}
	return value, nil
}

func decodeBeaboxJSON(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func beaboxCleanText(value string) string {
	value = html.UnescapeString(html.UnescapeString(value))
	value = beaboxEmExpr.ReplaceAllString(value, "")
	return cleanText(value)
}

func beaboxDramaFromMap(row map[string]any, site, fallbackCategory string) (Drama, error) {
	id := strings.TrimSpace(mapString(row, "vodId"))
	title := truncate(beaboxCleanText(mapString(row, "vodName")), 512)
	if !validBeaboxID(id) || title == "" {
		return Drama{}, errors.New("笔盒视频资料不完整")
	}
	cover := providerCoverAddress(row["vodPic"], site+"/")
	heat := mapString(row, "heatValue")
	if heat == "0" {
		heat = ""
	}
	tags := beaboxTags(row)
	category := fallbackCategory
	if len(tags) > 0 {
		category = tags[0]
	}
	return Drama{
		ID: providerDramaID(sourceBeabox, id), Source: sourceBeabox, SourceID: id,
		Title: title, Name: title, Cover: cover, CoverURL: cover,
		CategoryName: category, ChannelName: "笔盒", Tags: tags,
		Heat: heat, OnlineDate: beaboxTime(row["vodTime"]),
	}, nil
}

func beaboxTags(row map[string]any) []string {
	var tags []string
	seen := map[string]bool{}
	for _, value := range mapStringSlice(row, "vodClass") {
		tag := strings.TrimSpace(beaboxCleanText(value))
		if tag == "" || len([]rune(tag)) > 48 || seen[tag] {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
		if len(tags) >= 32 {
			break
		}
	}
	return tags
}

func beaboxTime(value any) string {
	seconds, err := strconv.ParseInt(nativeText(value), 10, 64)
	if err != nil || seconds < 946684800 || seconds > 4102444800 {
		return ""
	}
	return time.Unix(seconds, 0).Format("2006-01-02")
}

func (d *Downloader) fetchBeaboxCategories(ctx context.Context) ([]nativeCategory, error) {
	value, _, err := d.beaboxCall(ctx, "/configs", url.Values{"platformType": {"h5"}}, nil)
	if err != nil {
		return nil, err
	}
	config, valid := value.(map[string]any)
	if !valid {
		return nil, errors.New("笔盒分类数据格式无效")
	}
	var categories []nativeCategory
	seen := map[string]bool{}
	for _, name := range strings.Split(mapString(config, "home_tags"), ",") {
		name = strings.TrimSpace(name)
		id := beaboxTagPrefix + name
		if name == "" || len([]rune(name)) > 24 || seen[id] || !validBeaboxCategory(id) {
			continue
		}
		seen[id] = true
		categories = append(categories, nativeCategory{ID: id, Name: name})
		if len(categories) >= 64 {
			break
		}
	}
	if len(categories) == 0 {
		return nil, errors.New("笔盒暂未返回内容分类")
	}
	return categories, nil
}

func (d *Downloader) fetchBeaboxCatalogPage(ctx context.Context, page int, category, query string) ([]Drama, bool, error) {
	if page < 1 || page > 1000000 || len(query) > 256 || !validBeaboxCategory(category) {
		return nil, false, errors.New("笔盒目录查询参数无效")
	}
	keyword := strings.TrimSpace(query)
	fallbackCategory := ""
	if keyword == "" {
		fallbackCategory = beaboxTagName(category)
		keyword = fallbackCategory
	}
	route := "/vod/latest"
	values := url.Values{"page": {strconv.Itoa(page)}, "limit": {strconv.Itoa(beaboxPageSize)}}
	if keyword != "" {
		route = "/vod/search"
		values.Set("keyword", keyword)
		values.Set("sort", "latest")
	}
	value, site, err := d.beaboxCall(ctx, route, values, nil)
	if err != nil {
		return nil, false, err
	}
	payload, valid := value.(map[string]any)
	if !valid {
		return nil, false, errors.New("笔盒目录格式无效")
	}
	rows := []any{}
	if raw := payload["data"]; raw != nil {
		if rows, valid = raw.([]any); !valid {
			return nil, false, errors.New("笔盒目录格式无效")
		}
	}
	if len(rows) > 500 {
		return nil, false, errors.New("笔盒目录格式无效")
	}
	totalPages, pagesValid := webProviderInteger(payload["totalPages"], 1000000)
	hasMore := len(rows) >= beaboxPageSize
	if pagesValid {
		hasMore = page < totalPages
	}
	items := make([]Drama, 0, len(rows))
	seen := map[string]bool{}
	for _, entry := range rows {
		row, _ := entry.(map[string]any)
		drama, err := beaboxDramaFromMap(row, site, fallbackCategory)
		if err != nil {
			continue
		}
		if seen[drama.ID] {
			continue
		}
		seen[drama.ID] = true
		items = append(items, drama)
	}
	return items, hasMore, nil
}

type beaboxPlayGroup struct {
	name    string
	entries []map[string]any
}

func beaboxPlayValue(row map[string]any) any {
	for _, key := range []string{"vodPlaySource", "vodPlay"} {
		switch value := row[key].(type) {
		case map[string]any:
			if len(value) > 0 {
				return value
			}
		case []any:
			if len(value) > 0 {
				return value
			}
		case string:
			if strings.TrimSpace(value) != "" {
				return value
			}
		}
	}
	return nil
}

func beaboxPlayGroups(value any) []beaboxPlayGroup {
	switch item := value.(type) {
	case map[string]any:
		names := make([]string, 0, len(item))
		for name := range item {
			names = append(names, name)
		}
		sort.Strings(names)
		groups := make([]beaboxPlayGroup, 0, len(names))
		for _, name := range names {
			groups = append(groups, beaboxPlayGroup{name: name, entries: beaboxPlayEntries(item[name])})
		}
		return groups
	case []any:
		return []beaboxPlayGroup{{entries: beaboxPlayEntries(item)}}
	case string:
		if strings.TrimSpace(item) == "" {
			return nil
		}
		return []beaboxPlayGroup{{entries: []map[string]any{{"url": item}}}}
	default:
		return nil
	}
}

func beaboxPlayEntries(value any) []map[string]any {
	rows, _ := value.([]any)
	entries := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		if entry, ok := row.(map[string]any); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

func beaboxBestGroup(groups []beaboxPlayGroup) beaboxPlayGroup {
	var best beaboxPlayGroup
	bestScore := -1
	for _, group := range groups {
		score := 0
		for _, entry := range group.entries {
			if beaboxEntryPlayable(entry) {
				score++
			}
		}
		if score > bestScore || score == bestScore && group.name == "beabox-app" && best.name != "beabox-app" {
			best, bestScore = group, score
		}
	}
	return best
}

func beaboxEntryAt(row map[string]any, group string, index int) (map[string]any, bool) {
	groups := beaboxPlayGroups(beaboxPlayValue(row))
	for _, item := range groups {
		if item.name == group && index >= 0 && index < len(item.entries) {
			return item.entries[index], true
		}
	}
	return nil, false
}

func beaboxEntryPlayable(entry map[string]any) bool {
	return strings.TrimSpace(firstNonEmpty(mapString(entry, "url"), mapString(entry, "playUrl"))) != ""
}

func beaboxEntryNeedsParse(entry, row map[string]any) bool {
	return beaboxFlag(entry["needParse"]) || beaboxFlag(row["playNeedParse"])
}

func beaboxFlag(value any) bool {
	switch item := value.(type) {
	case bool:
		return item
	case string:
		return item == "1" || strings.EqualFold(item, "true")
	case json.Number:
		number, err := item.Int64()
		return err == nil && number == 1
	case float64:
		return item == 1
	}
	return false
}

func beaboxChapters(row map[string]any, site, sourceID string) []Chapter {
	best := beaboxBestGroup(beaboxPlayGroups(beaboxPlayValue(row)))
	var chapters []Chapter
	for index, entry := range best.entries {
		if !beaboxEntryPlayable(entry) {
			continue
		}
		key := best.name + ":" + strconv.Itoa(index)
		title := strings.TrimSpace(beaboxCleanText(mapString(entry, "flag")))
		if title == "" {
			title = fmt.Sprintf("第 %d 集", len(chapters)+1)
		}
		chapters = append(chapters, Chapter{
			ID: providerChapterID(sourceBeabox, sourceID, key), Source: sourceBeabox,
			Title: truncate(title, 128), CurrentEpisode: rawEpisode(len(chapters) + 1),
			VideoURL: "beabox-play://" + key,
			PageURL:  site + "/vods/" + url.PathEscape(sourceID), Referer: site + "/",
		})
	}
	return chapters
}

func (d *Downloader) fetchBeaboxDetail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	if !validBeaboxID(sourceID) {
		return Drama{}, nil, errors.New("笔盒视频 ID 无效")
	}
	value, site, err := d.beaboxCall(ctx, "/vod/detail/"+url.PathEscape(sourceID), nil, nil)
	if err != nil {
		return Drama{}, nil, err
	}
	row, valid := value.(map[string]any)
	if !valid || strings.TrimSpace(mapString(row, "vodId")) != sourceID {
		return Drama{}, nil, errors.New("笔盒详情与请求视频不符")
	}
	drama, err := beaboxDramaFromMap(row, site, "")
	if err != nil {
		return Drama{}, nil, err
	}
	chapters := beaboxChapters(row, site, sourceID)
	if len(chapters) == 0 {
		return Drama{}, nil, errors.New("笔盒该视频暂无可播放线路")
	}
	drama.EpisodeCount, drama.TotalEpisode = len(chapters), len(chapters)
	return drama, chapters, nil
}

func (d *Downloader) resolveBeaboxMedia(ctx context.Context, task Task) (providerMedia, error) {
	source, sourceID, valid := splitProviderDramaID(task.DramaID)
	prefix := providerChapterID(sourceBeabox, sourceID, "")
	if !valid || source != sourceBeabox || !validBeaboxID(sourceID) || !strings.HasPrefix(task.Chapter.ID, prefix) {
		return providerMedia{}, errors.New("笔盒播放信息无效，请刷新详情")
	}
	key := strings.TrimPrefix(task.Chapter.ID, prefix)
	separator := strings.LastIndex(key, ":")
	if separator < 0 {
		return providerMedia{}, errors.New("笔盒播放信息无效，请刷新详情")
	}
	group, indexText := key[:separator], key[separator+1:]
	index, err := strconv.Atoi(indexText)
	if err != nil || index < 0 {
		return providerMedia{}, errors.New("笔盒播放信息无效，请刷新详情")
	}
	value, site, err := d.beaboxCall(ctx, "/vod/detail/"+url.PathEscape(sourceID), nil, nil)
	if err != nil {
		return providerMedia{}, err
	}
	row, valid := value.(map[string]any)
	if !valid {
		return providerMedia{}, errors.New("笔盒详情格式无效")
	}
	entry, found := beaboxEntryAt(row, group, index)
	if !found {
		return providerMedia{}, errors.New("笔盒分集已变化，请刷新详情")
	}
	address := firstNonEmpty(mapString(entry, "url"), mapString(entry, "playUrl"))
	if beaboxEntryNeedsParse(entry, row) {
		parsed, _, err := d.beaboxCall(ctx, "/vod/parse-url", nil, map[string]any{
			"playerFrom": strings.TrimSpace(mapString(entry, "from")),
			"playUrl":    strings.TrimSpace(mapString(entry, "playUrl")),
		})
		if err != nil {
			return providerMedia{}, err
		}
		payload, _ := parsed.(map[string]any)
		address = mapString(payload, "url")
	}
	if !isProviderHTTPMediaURL(address) {
		return providerMedia{}, errors.New("笔盒未返回有效播放地址，请刷新详情后重试")
	}
	return d.prepareWebProviderMedia(ctx, providerMedia{URL: address, Referer: site + "/"}, "笔盒")
}
