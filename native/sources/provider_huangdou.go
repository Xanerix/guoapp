package sources

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"duanjuapp/native/core"
)

const (
	huangdouVersion     = "2.0.0"
	huangdouDeviceType  = "web"
	huangdouPlatformKey = "7961beb44246e3012ce228d6b5ced05a"
)

const huangdouBaseHost = "ferncider.cc"

var huangdouHostPool = [...]string{
	huangdouBaseHost,
	"onyxripple.cc",
	"reeflumen.cc",
	"niniharbor.top",
	"halomeadow.top",
	"larksummit.icu",
}

const huangdouBaseURL = "https://" + huangdouBaseHost

const (
	huangdouAttemptTimeout      = 8 * time.Second
	huangdouFirstAttemptTimeout = 12 * time.Second
)

func huangdouHostname(candidate string) string {
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	if index := strings.Index(candidate, "://"); index >= 0 {
		candidate = candidate[index+3:]
	}
	candidate = strings.TrimPrefix(candidate, "//")
	if index := strings.IndexAny(candidate, "/?#"); index >= 0 {
		candidate = candidate[:index]
	}
	if index := strings.LastIndex(candidate, "@"); index >= 0 {
		candidate = candidate[index+1:]
	}
	if index := strings.LastIndex(candidate, ":"); index >= 0 {
		candidate = candidate[:index]
	}
	return strings.TrimSuffix(candidate, ".")
}

func isHuangdouHost(candidate string) bool {
	host := huangdouHostname(candidate)
	if host == "" {
		return false
	}
	for _, pooled := range huangdouHostPool {
		if pooled == host {
			return true
		}
	}
	return false
}

func huangdouNextHostURL(current string) string {
	host := huangdouHostname(current)
	for index, pooled := range huangdouHostPool {
		if pooled == host {
			return "https://" + huangdouHostPool[(index+1)%len(huangdouHostPool)]
		}
	}
	return "https://" + huangdouHostPool[0]
}

type huangdouAPIClient struct {
	p         *huangdouProvider
	host      string
	sessionID string
	deviceID  string
}

func newHuangdouAPIClient(p *huangdouProvider) *huangdouAPIClient {
	id := randomHex(16)
	if id == "" {
		id = strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	host := core.FirstNonEmpty(p.host.PreferredSite(sourceHuangdou), p.site())
	return &huangdouAPIClient{p: p, host: host, sessionID: id, deviceID: id}
}

func (p *huangdouProvider) fetchHuangdouChapters(ctx context.Context, sourceID string) (string, []core.Chapter, error) {
	sourceID = strings.TrimPrefix(strings.TrimSpace(sourceID), "rp_")
	data, err := p.huangdouDetail(ctx, sourceID)
	if err != nil {
		return "", nil, err
	}
	title := core.FirstNonEmpty(core.MapString(data, "name", "title", "t"), sourceID)
	episodes := huangdouList(data["episodes"])
	count := atoiDefault(core.FirstNonEmpty(core.MapString(data, "episode_count"), core.MapString(data, "free_episodes")), len(episodes))
	if count <= 0 {
		count = len(episodes)
	}
	if count <= 0 || count > 10000 {
		return title, nil, fmt.Errorf("黄豆返回了无效集数: %d", count)
	}
	chapters := make([]core.Chapter, 0, count)
	if len(episodes) > 0 {
		for i, ep := range episodes {
			seq := atoiDefault(core.FirstNonEmpty(core.MapString(ep, "seq"), core.MapString(ep, "episode"), core.MapString(ep, "ep")), i+1)
			if seq <= 0 {
				seq = i + 1
			}
			titleText := core.FirstNonEmpty(core.MapString(ep, "name", "title"), fmt.Sprintf("第%d集", seq))
			chapters = append(chapters, core.Chapter{VIP: huangdouEpisodeVIP(data, ep, seq), ID: core.ProviderChapterID(sourceHuangdou, sourceID, strconv.Itoa(seq)), Source: sourceHuangdou, Title: titleText, CurrentEpisode: core.RawEpisode(seq)})
		}
	} else {
		for i := 1; i <= count; i++ {
			chapters = append(chapters, core.Chapter{VIP: huangdouEpisodeVIP(data, nil, i), ID: core.ProviderChapterID(sourceHuangdou, sourceID, strconv.Itoa(i)), Source: sourceHuangdou, Title: fmt.Sprintf("第%d集", i), CurrentEpisode: core.RawEpisode(i)})
		}
	}
	core.SortProviderChapters(chapters)
	return title, core.UniqueChapters(chapters), nil
}

func (p *huangdouProvider) resolveHuangdouPlayback(ctx context.Context, client *huangdouAPIClient, sourceID string, seq int) (core.ProviderMedia, error) {
	var decoded any
	if err := client.call(ctx, "/drama/play", map[string]any{"id": sourceID, "seq": strconv.Itoa(seq)}, &decoded); err != nil {
		return core.ProviderMedia{}, err
	}
	data := huangdouDataMap(decoded)
	media := core.FirstNonEmpty(core.MapString(data, "m3u8"), core.MapString(data, "url"), core.MapString(data, "play_url"), core.MapString(data, "playUrl"))
	if huangdouPreviewOnly(data, media, client.host) {
		return core.ProviderMedia{}, &huangdouAPIError{code: "preview", message: "黄豆仅提供试看，未取得该集正片；不会把试看内容当作完整分集"}
	}
	if media == "" {
		media = fmt.Sprintf("%s/api/drama/hls/%s/%d/play.m3u8?line=free", client.host, url.PathEscape(sourceID), seq)
		playlist, err := p.host.FetchText(ctx, core.TextRequest{URL: media, Referer: client.host + "/home"})
		if err != nil || !strings.HasPrefix(strings.TrimSpace(playlist.Body), "#EXTM3U") {
			return core.ProviderMedia{}, errors.New("黄豆未提供有效的播放地址，备用播放列表也不可用")
		}
	}
	media = core.ResolveProviderURL(client.host+"/", media)
	if !core.IsProviderHTTPMediaURL(media) {
		return core.ProviderMedia{}, errors.New("黄豆播放地址不是 HTTP/HTTPS URL")
	}
	result := core.ProviderMedia{URL: media, Referer: client.host + "/home"}
	if rawKey := core.MapString(data, "hls_key"); rawKey != "" {
		key, err := hex.DecodeString(rawKey)
		if err != nil || len(key) != aes.BlockSize {
			return core.ProviderMedia{}, errors.New("黄豆返回了无效的 HLS 密钥")
		}
		result.HLSKey = key
	}
	if duration, err := strconv.ParseFloat(core.MapString(data, "duration"), 64); err == nil && duration > 0 {
		result.Duration = time.Duration(duration * float64(time.Second))
	}
	return result, nil
}

func (c *huangdouAPIClient) call(ctx context.Context, path string, data map[string]any, out *any) error {
	rotate := c.p.configured == ""
	attempts := 1
	if rotate {
		attempts = len(huangdouHostPool)
	} else if configured := c.p.retries; configured > attempts {
		attempts = configured
	}
	var lastErr error
	var backoff time.Duration
	hostsTried := make(map[string]bool, attempts)
	fail := func(err error) error {
		if err == nil || len(hostsTried) < 2 {
			return err
		}
		return fmt.Errorf("黄豆站点连接失败：已尝试 %d 个域名，最后错误：%w", len(hostsTried), err)
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if backoff > 0 {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return fail(ctx.Err())
			}
		}
		hostsTried[huangdouHostname(c.host)] = true
		ceiling := huangdouAttemptTimeout
		if len(hostsTried) == 1 {
			ceiling = huangdouFirstAttemptTimeout
		}
		lastErr = c.callOnce(ctx, path, data, out, c.attemptTimeout(ctx, attempts-attempt, ceiling))
		if lastErr == nil {
			return nil
		}
		var apiErr *huangdouAPIError
		if errors.As(lastErr, &apiErr) {
			return lastErr
		}
		if attempt+1 >= attempts {
			break
		}
		if !rotate {
			backoff = time.Duration(attempt+1) * time.Second
			continue
		}
		c.host = huangdouNextHostURL(c.host)
		backoff = 0
	}
	return fail(lastErr)
}

func (c *huangdouAPIClient) attemptTimeout(ctx context.Context, remainingTries int, ceiling time.Duration) time.Duration {
	if remainingTries < 1 {
		remainingTries = 1
	}
	timeout := ceiling
	if deadline, ok := ctx.Deadline(); ok {
		if budget := time.Until(deadline) / time.Duration(remainingTries); budget < timeout {
			timeout = budget
		}
	}
	if timeout < time.Second {
		timeout = time.Second
	}
	return timeout
}

type huangdouAPIError struct {
	code    string
	message string
}

func (failure *huangdouAPIError) Error() string {
	if failure.code != "preview" && isHuangdouAccessError(failure) {
		return failure.message + "（站点权限限制，已停止自动重试；切换代理不会解锁）"
	}
	return failure.message
}

func isHuangdouAccessError(err error) bool {
	var failure *huangdouAPIError
	if !errors.As(err, &failure) {
		return false
	}
	switch failure.code {
	case "813004", "813005", "813006", "813103", "preview":
		return true
	default:
		return false
	}
}

func huangdouPreviewOnly(data map[string]any, media, host string) bool {
	switch strings.ToLower(core.MapString(data, "is_preview", "isPreview")) {
	case "true", "1", "y":
		return true
	}
	if preview := core.MapString(data, "preview_m3u8", "preview_url"); preview != "" &&
		(media == "" || core.ResolveProviderURL(host+"/", media) == core.ResolveProviderURL(host+"/", preview)) {
		return true
	}
	parsed, err := url.Parse(media)
	if err != nil {
		return false
	}
	path := strings.ToLower(parsed.Path)
	return path == "preview.mp4" || path == "preview.m3u8" || strings.HasSuffix(path, "/preview.mp4") || strings.HasSuffix(path, "/preview.m3u8")
}

func (c *huangdouAPIClient) callOnce(ctx context.Context, path string, data map[string]any, out *any, timeout time.Duration) error {
	path = "/" + strings.TrimLeft(path, "/")
	rid := uuidLike()
	key, err := huangdouKey(rid)
	if err != nil {
		return err
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return err
	}
	plain, err := json.Marshal(map[string]any{"token": "", "deviceId": c.deviceID, "data": data})
	if err != nil {
		return err
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(plain); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	cipherText, err := aesCBCEncrypt(gz.Bytes(), key, iv)
	if err != nil {
		return err
	}
	body := append(append([]byte{}, iv...), cipherText...)
	address := strings.TrimRight(c.host, "/") + "/api" + path
	ticket, err := c.p.host.Reserve(ctx, address, "")
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signSum := sha256.Sum256([]byte("Dart|" + c.sessionID + "|" + rid + "|" + timestamp + "|" + path))
	response, err := c.p.host.Fetch(ctx, core.HTTPRequest{
		Label: "黄豆", Method: http.MethodPost, URL: address, Body: body, Ticket: ticket,
		Limit: core.ProviderMaxBodyBytes, Timeout: timeout, FollowRedirects: true, AnyStatus: true,
		Header: map[string]string{
			"User-Agent":   "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36",
			"Accept":       "*/*",
			"Origin":       c.host,
			"Referer":      c.host + "/home",
			"Content-Type": "application/octet-stream",
			"version":      huangdouVersion,
			"deviceType":   huangdouDeviceType,
			"requestId":    rid,
			"sessionId":    c.sessionID,
			"time":         timestamp,
			"sign":         hex.EncodeToString(signSum[:]) + "-" + timestamp,
		},
	})
	if err != nil {
		return err
	}
	blob := response.Body
	if response.Status < 200 || response.Status >= 300 {
		return fmt.Errorf("huangdou HTTP %d: %s", response.Status, core.Truncate(string(blob), 200))
	}
	decoded, err := huangdouDecode(blob, key)
	if err != nil {
		return err
	}
	if envelope, ok := decoded.(map[string]any); ok {
		if status := core.MapString(envelope, "status"); status != "" && status != "y" {
			return &huangdouAPIError{code: core.MapString(envelope, "errorCode", "error_code", "code"), message: "黄豆接口拒绝请求: " + core.FirstNonEmpty(core.MapString(envelope, "msg", "message", "error"), status)}
		}
	}
	c.p.host.RememberSite(sourceHuangdou, c.host)
	if out != nil {
		*out = decoded
	}
	return nil
}

func huangdouKey(rid string) ([]byte, error) {
	clean := strings.ReplaceAll(rid, "-", "")
	b, err := hex.DecodeString(clean)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, []byte(huangdouPlatformKey))
	_, _ = mac.Write(b)
	return mac.Sum(nil), nil
}

func huangdouDecode(blob, key []byte) (any, error) {
	if len(blob) > core.ProviderMaxBodyBytes {
		return nil, errors.New("黄豆响应过大")
	}
	if json.Valid(blob) {
		var direct any
		decoder := json.NewDecoder(bytes.NewReader(blob))
		decoder.UseNumber()
		if err := decoder.Decode(&direct); err != nil {
			return nil, err
		}
		return direct, nil
	}
	if len(blob) < aes.BlockSize*2 {
		return nil, fmt.Errorf("huangdou response too short")
	}
	plain, err := aesCBCDecrypt(blob[aes.BlockSize:], key, blob[:aes.BlockSize])
	if err != nil {
		return nil, err
	}
	if len(plain) >= 2 && plain[0] == 0x1f && plain[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(plain))
		if err != nil {
			return nil, err
		}
		plain, err = io.ReadAll(io.LimitReader(zr, core.ProviderMaxBodyBytes+1))
		_ = zr.Close()
		if err != nil {
			return nil, err
		}
		if len(plain) > core.ProviderMaxBodyBytes {
			return nil, errors.New("黄豆解压后响应过大")
		}
	}
	var decoded any
	if !json.Valid(plain) {
		return nil, errors.New("黄豆响应格式异常")
	}
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func aesCBCEncrypt(plain, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plain, block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return out, nil
}

func aesCBCDecrypt(cipherText, key, iv []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(cipherText)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("huangdou ciphertext is not block aligned")
	}
	out := make([]byte, len(cipherText))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, cipherText)
	return core.Pkcs7Unpad(out, block.BlockSize())
}

func huangdouDramaFromMap(item map[string]any) core.Drama {
	sourceID := strings.TrimPrefix(core.FirstNonEmpty(core.MapString(item, "id"), core.MapString(item, "drama_id")), "rp_")
	if sourceID == "" {
		return core.Drama{}
	}
	title := core.FirstNonEmpty(core.MapString(item, "name", "title", "t"), sourceID)
	cover := core.FirstNonEmpty(core.MapString(item, "img_y", "img_x", "img", "cover", "pic"))
	remark := core.FirstNonEmpty(core.MapString(item, "update_label"), core.MapString(item, "corner"))
	releaseStatus := core.ReleaseStatusFromRemark(remark)
	if remark == "" {
		if eps := core.MapString(item, "episode_count"); eps != "" {
			remark = "全" + eps + "集"
		}
	}
	category := core.FirstNonEmpty(core.MapString(item, "category", "category_name", "categoryName"), "未分类")
	return core.Drama{VIP: huangdouVIPFlag(item), ID: core.ProviderDramaID(sourceHuangdou, sourceID), Source: sourceHuangdou, SourceID: sourceID, Title: title, Name: title, Desc: core.FirstNonEmpty(core.MapString(item, "description"), core.MapString(item, "summary")), Intro: core.FirstNonEmpty(core.MapString(item, "description"), core.MapString(item, "summary")), Cover: cover, CoverURL: cover, CategoryName: category, ChannelName: "黄豆", Remark: remark, TotalEpisode: core.MapString(item, "episode_count"), EpisodeCount: core.MapString(item, "episode_count"), Tags: core.MapStringSlice(item, "tags"), ReleaseStatus: releaseStatus, Heat: core.MapString(item, "hot_rate"), Views: core.NormalizeViews(core.MapString(item, "click")), OnlineDate: core.ProviderReleaseDate(core.MapString(item, "issue_date"))}
}

func huangdouDataMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if data, ok := m["data"].(map[string]any); ok {
		return data
	}
	return m
}

func huangdouList(v any) []map[string]any {
	switch x := v.(type) {
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case []map[string]any:
		return x
	case map[string]any:
		for _, key := range []string{"list", "items", "data"} {
			if list := huangdouList(x[key]); len(list) > 0 {
				return list
			}
		}
	}
	return nil
}

func atoiDefault(s string, fallback int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return fallback
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

func uuidLike() string {
	raw := randomHex(16)
	if len(raw) != 32 {
		return raw
	}
	return raw[:8] + "-" + raw[8:12] + "-" + raw[12:16] + "-" + raw[16:20] + "-" + raw[20:]
}

type huangdouProvider struct {
	host          core.Host
	configured    string
	retries       int
	detailMu      sync.Mutex
	details       map[string]huangdouDetailEntry
	detailPending map[string]*huangdouDetailCall
}

func (p *huangdouProvider) site() string {
	return strings.TrimRight(core.FirstNonEmpty(p.configured, huangdouBaseURL), "/")
}

var huangdouDescriptor = core.SourceDescriptor{
	ID:          sourceHuangdou,
	Name:        "黄豆",
	Description: "精选短剧",
	DefaultOff:  true,
	Aliases:     []string{"tideember.cc", "xqjurgek.top"},
	MatchesHost: isHuangdouHost,
	BaseURL:     huangdouBaseURL,
	Rankings: []core.RankingBoard{
		{ID: "huangdou-all", Source: sourceHuangdou, Name: "总榜", Description: "黄豆短剧总榜，保留站点返回的顺序。", UpstreamKey: "all"},
		{ID: "huangdou-mogai", Source: sourceHuangdou, Name: "魔改榜", Description: "黄豆站点魔改榜。", UpstreamKey: "mogai"},
		{ID: "huangdou-search", Source: sourceHuangdou, Name: "搜索榜", Description: "黄豆站点搜索榜。", UpstreamKey: "search"},
		{ID: "huangdou-favorite", Source: sourceHuangdou, Name: "收藏榜", Description: "黄豆站点收藏榜。", UpstreamKey: "favorite"},
		{ID: "huangdou-finish", Source: sourceHuangdou, Name: "完结榜", Description: "黄豆站点完结榜。", UpstreamKey: "finish"},
	},
}

func init() {
	core.Register(huangdouDescriptor, func(host core.Host) core.Provider { return &huangdouProvider{host: host} })
}

func (p *huangdouProvider) Descriptor() core.SourceDescriptor { return huangdouDescriptor }

func (p *huangdouProvider) BaseURL() string {
	return core.FirstNonEmpty(p.configured, huangdouBaseURL)
}

func (p *huangdouProvider) Categories(context.Context, bool) ([]core.Category, error) {
	return nil, core.ErrCategoriesIncomplete
}

func (p *huangdouProvider) CatalogPage(ctx context.Context, page int, category string) ([]core.Drama, bool, error) {
	var decoded any
	if err := newHuangdouAPIClient(p).call(ctx, "/drama/list", map[string]any{"page": strconv.Itoa(page), "page_size": "30"}, &decoded); err != nil {
		return nil, false, err
	}
	rows := huangdouList(decoded)
	items := make([]core.Drama, 0, len(rows))
	for _, row := range rows {
		if drama := huangdouDramaFromMap(row); drama.ID != "" {
			items = append(items, drama)
		}
	}
	return items, len(rows) >= 30, nil
}

func (p *huangdouProvider) Detail(ctx context.Context, sourceID string) (core.Drama, []core.Chapter, error) {
	title, chapters, err := p.fetchHuangdouChapters(ctx, sourceID)
	if err != nil {
		return core.Drama{}, nil, err
	}
	raw := core.Drama{ID: core.ProviderDramaID(sourceHuangdou, strings.TrimPrefix(strings.TrimSpace(sourceID), "rp_")), Source: sourceHuangdou, Title: title, Name: title}
	if row, err := p.huangdouDetail(ctx, sourceID); err == nil {
		if fresh := huangdouDramaFromMap(row); fresh.ID == raw.ID {
			raw = fresh
			if raw.Title == "" || raw.Title == "短剧" {
				raw.Title, raw.Name = title, title
			}
		}
	}
	return raw, chapters, nil
}

func (p *huangdouProvider) ResolveMedia(ctx context.Context, task core.Task) (core.ProviderMedia, error) {
	chapter := task.Chapter
	media := core.ProviderMedia{URL: chapter.VideoURL, Referer: core.FirstNonEmpty(chapter.Referer, p.site()+"/")}
	if _, sourceID, valid := core.SplitProviderDramaID(task.DramaID); valid {
		sequence, err := strconv.Atoi(chapter.EpisodeString(task.Index))
		if err != nil || sequence < 1 {
			return core.ProviderMedia{}, fmt.Errorf("黄豆集数无效")
		}
		client := newHuangdouAPIClient(p)
		media, err = p.resolveHuangdouPlayback(ctx, client, sourceID, sequence)
		if err != nil {
			return core.ProviderMedia{}, err
		}
		media.Referer = client.host + "/home"
	}
	return p.host.FinishMedia(ctx, media, sourceHuangdou)
}

func (p *huangdouProvider) RankingPage(ctx context.Context, board core.RankingBoard, page int) (core.RankingPage, error) {
	var decoded any
	if err := newHuangdouAPIClient(p).call(ctx, "/drama/rank", map[string]any{"tab": board.UpstreamKey, "page": strconv.Itoa(page)}, &decoded); err != nil {
		return core.RankingPage{}, err
	}
	return parseHuangdouRanking(decoded, page)
}

func (p *huangdouProvider) CoverAddress(ctx context.Context, drama core.LibraryDrama, id string) (string, error) {
	row, err := p.huangdouDetail(ctx, id)
	if err != nil {
		return "", err
	}
	fresh := core.NativeNormalize(huangdouDramaFromMap(row))
	if fresh.ID != drama.ID {
		return "", errors.New("黄豆详情与请求剧集不符")
	}
	return fresh.Cover, nil
}

func (p *huangdouProvider) CoverReferer(address string) string {
	if parsed, err := url.Parse(address); err == nil && isHuangdouHost(parsed.Hostname()) {
		return parsed.Scheme + "://" + parsed.Host + "/home"
	}
	return core.FirstNonEmpty(p.host.PreferredSite(sourceHuangdou), p.site()) + "/home"
}

func (p *huangdouProvider) NeedsExtraMetadata(drama core.LibraryDrama) bool {
	return drama.Heat == "" || drama.VIP == nil
}

func (p *huangdouProvider) ExtraMetadata(ctx context.Context, drama core.LibraryDrama, id string) (core.LibraryDrama, error) {
	row, err := p.huangdouDetail(ctx, id)
	if err != nil {
		return drama, err
	}
	raw := huangdouDramaFromMap(row)
	if raw.Heat == "" && drama.Heat == "" {
		var decoded any
		err = newHuangdouAPIClient(p).call(ctx, "/drama/list", map[string]any{
			"keywords": core.FirstNonEmpty(raw.Title, drama.Title), "page": "1", "page_size": "50",
		}, &decoded)
		if err != nil {
			return drama, err
		}
		for _, candidate := range huangdouList(decoded) {
			if strings.TrimPrefix(core.MapString(candidate, "id", "drama_id"), "rp_") == id {
				raw = mergeDramaMetadata(huangdouDramaFromMap(candidate), raw)
				break
			}
		}
	}
	if raw.ID == drama.ID {
		drama = core.MergeNativeDrama(drama, core.NativeNormalize(raw))
	}
	return drama, ctx.Err()
}

const sourceHuangdou = "huangdou"
