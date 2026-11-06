package core

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
	"time"
)

const (
	huangdouVersion     = "2.0.0"
	huangdouDeviceType  = "web"
	huangdouPlatformKey = "7961beb44246e3012ce228d6b5ced05a"
)

// 域名池首项恒定为该值，huangdouBaseURL 由它派生。
const huangdouBaseHost = "ferncider.cc"

// huangdouHostPool 是黄豆站点的域名池。站点会不定期换域名，主域名失效后按池内顺序轮转。
//
// 池内每个域名都必须能通过 TLS 严格校验：本项目的 transport 不跳过证书验证，
// 证书过期或 CN 不匹配的域名轮转过去也是白跑，必须从池里剔除
// （2026-11-06 剔除 xqjurgek.top：证书 CN=ccnirwgj.top 且已过期）。
//
// 用定长数组而不是切片，避免后续误用 append 换掉底层数组、让轮转顺序悄悄变化。
// 注意：池内证书是同一批签发的，到期日均为 2027-01-19，届时需要整体重扫换池。
var huangdouHostPool = [...]string{
	huangdouBaseHost,
	"onyxripple.cc",
	"reeflumen.cc",
	"niniharbor.top",
	"halomeadow.top",
	"larksummit.icu",
}

// huangdouBaseURL 是黄豆的默认站点地址，同时作为 providerBaseURL 的兜底值。
const huangdouBaseURL = "https://" + huangdouBaseHost

// huangdouAttemptTimeout 是轮转之后单次请求的超时上限。轮转时会把外层 ctx 的剩余时间
// 按剩余尝试次数均分，避免排在前面的域名吃光预算、后面的域名轮不到。
const huangdouAttemptTimeout = 8 * time.Second

// huangdouFirstAttemptTimeout 是第一个站点的超时上限。站点只是慢（并非连不上）时，
// 一次成功好过把整个域名池轮转一遍，所以首跳比后续轮转宽裕。
// 首跳 12s + 5 次轮转 8s = 52s，仍在外层目录请求 60s 的预算内。
const huangdouFirstAttemptTimeout = 12 * time.Second

// huangdouHostname 从域名或完整地址中取出裸域名（小写、不含 scheme、端口、userinfo 与路径）。
func huangdouHostname(candidate string) string {
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	if index := strings.Index(candidate, "://"); index >= 0 {
		candidate = candidate[index+3:]
	}
	// 协议相对地址（//host/path）
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

// isHuangdouHost 判断给定域名或地址是否属于黄豆域名池。入参可以是裸域名，也可以是完整 URL。
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

// huangdouNextHostURL 返回域名池中 current 之后的下一个站点；current 不在池中时返回池首。
func huangdouNextHostURL(current string) string {
	if len(huangdouHostPool) == 0 {
		return ""
	}
	host := huangdouHostname(current)
	for index, pooled := range huangdouHostPool {
		if pooled == host {
			return "https://" + huangdouHostPool[(index+1)%len(huangdouHostPool)]
		}
	}
	return "https://" + huangdouHostPool[0]
}

type huangdouAPIClient struct {
	d         *Downloader
	host      string
	sessionID string
	deviceID  string
}

func newHuangdouAPIClient(d *Downloader) *huangdouAPIClient {
	id := randomHex(16)
	if id == "" {
		id = strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	host := d.providerBaseURL(sourceHuangdou)
	d.providerMu.Lock()
	if preferred := d.providerHosts[sourceHuangdou]; preferred != "" {
		host = preferred
	}
	d.providerMu.Unlock()
	return &huangdouAPIClient{d: d, host: host, sessionID: id, deviceID: id}
}

func (d *Downloader) fetchHuangdouChapters(ctx context.Context, sourceID string) (string, []Chapter, error) {
	sourceID = strings.TrimPrefix(strings.TrimSpace(sourceID), "rp_")
	data, err := d.huangdouDetail(ctx, sourceID)
	if err != nil {
		return "", nil, err
	}
	title := firstNonEmpty(mapString(data, "name", "title", "t"), sourceID)
	episodes := huangdouList(data["episodes"])
	count := atoiDefault(firstNonEmpty(mapString(data, "episode_count"), mapString(data, "free_episodes")), len(episodes))
	if count <= 0 {
		count = len(episodes)
	}
	if count <= 0 || count > 10000 {
		return title, nil, fmt.Errorf("黄豆返回了无效集数: %d", count)
	}
	chapters := make([]Chapter, 0, count)
	if len(episodes) > 0 {
		for i, ep := range episodes {
			seq := atoiDefault(firstNonEmpty(mapString(ep, "seq"), mapString(ep, "episode"), mapString(ep, "ep")), i+1)
			if seq <= 0 {
				seq = i + 1
			}
			titleText := firstNonEmpty(mapString(ep, "name", "title"), fmt.Sprintf("第%d集", seq))
			chapters = append(chapters, Chapter{VIP: huangdouEpisodeVIP(data, ep, seq), ID: providerChapterID(sourceHuangdou, sourceID, strconv.Itoa(seq)), Source: sourceHuangdou, Title: titleText, CurrentEpisode: rawEpisode(seq)})
		}
	} else {
		for i := 1; i <= count; i++ {
			chapters = append(chapters, Chapter{VIP: huangdouEpisodeVIP(data, nil, i), ID: providerChapterID(sourceHuangdou, sourceID, strconv.Itoa(i)), Source: sourceHuangdou, Title: fmt.Sprintf("第%d集", i), CurrentEpisode: rawEpisode(i)})
		}
	}
	sortProviderChapters(chapters)
	return title, uniqueChapters(chapters), nil
}

func (d *Downloader) resolveHuangdouPlayback(ctx context.Context, client *huangdouAPIClient, sourceID string, seq int) (providerMedia, error) {
	var decoded any
	if err := client.call(ctx, "/drama/play", map[string]any{"id": sourceID, "seq": strconv.Itoa(seq)}, &decoded); err != nil {
		return providerMedia{}, err
	}
	data := huangdouDataMap(decoded)
	media := firstNonEmpty(mapString(data, "m3u8"), mapString(data, "url"), mapString(data, "play_url"), mapString(data, "playUrl"))
	if huangdouPreviewOnly(data, media, client.host) {
		return providerMedia{}, &huangdouAPIError{code: "preview", message: "黄豆仅提供试看，未取得该集正片；不会把试看内容当作完整分集"}
	}
	if media == "" {
		media = fmt.Sprintf("%s/api/drama/hls/%s/%d/play.m3u8?line=free", client.host, url.PathEscape(sourceID), seq)
		playlist, err := d.fetchProviderText(ctx, media, client.host+"/home")
		if err != nil || !strings.HasPrefix(strings.TrimSpace(playlist), "#EXTM3U") {
			return providerMedia{}, errors.New("黄豆未提供有效的播放地址，备用播放列表也不可用")
		}
	}
	media = resolveProviderURL(client.host+"/", media)
	if !isProviderHTTPMediaURL(media) {
		return providerMedia{}, errors.New("黄豆播放地址不是 HTTP/HTTPS URL")
	}
	result := providerMedia{URL: media, Referer: client.host + "/home"}
	if rawKey := mapString(data, "hls_key"); rawKey != "" {
		key, err := hex.DecodeString(rawKey)
		if err != nil || len(key) != aes.BlockSize {
			return providerMedia{}, errors.New("黄豆返回了无效的 HLS 密钥")
		}
		result.HLSKey = key
	}
	if duration, err := strconv.ParseFloat(mapString(data, "duration"), 64); err == nil && duration > 0 {
		result.Duration = time.Duration(duration * float64(time.Second))
	}
	return result, nil
}

// call 发起一次接口调用。遇到网络层失败时按域名池轮转下一个站点；
// 业务层错误（huangdouAPIError）说明站点可达且已明确拒绝，不再重试也不再切换。
//
// 非并发安全：host 这类轮转状态是围绕单次请求的，每个请求新建一个客户端使用，不要跨 goroutine 复用。
func (c *huangdouAPIClient) call(ctx context.Context, path string, data map[string]any, out *any) error {
	// 用户显式配置了站点时以配置为准，只按 cfg.Retries 重试同一站点；
	// 否则把整个域名池走一遍——全局 Retries 的默认值（2）只够切一次。
	rotate := c.d.cfg.HuangdouURL == ""
	attempts := 1
	if rotate {
		attempts = len(huangdouHostPool)
	} else if configured := c.d.cfg.Retries; configured > attempts {
		attempts = configured
	}
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	var backoff time.Duration
	hostsTried := make(map[string]bool, attempts)
	// 全池失败时补上「试过几个站点」的上下文，否则用户只看到最后一个陌生域名的报错。
	// 显式配置站点时是同一域名重试，去重后只有一个域名，不会走到这个包装。
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
			// 第一个站点给更宽裕的预算：站点只是慢（并非连不上）时，一次成功好过轮转一圈。
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
		next := huangdouNextHostURL(c.host)
		if next == "" || next == c.host {
			// 当前不可达（轮转模式下域名池至少两项），仅防御域名池退化为 0 或 1 项。
			backoff = time.Duration(attempt+1) * time.Second
			continue
		}
		// 轮换到新域名是换目标，不是对同一目标的重复请求，无需退避。
		// 失败不写 providerHosts，所以下次请求仍从主域名开始重试整个池。
		c.host = next
		backoff = 0
	}
	return fail(lastErr)
}

// attemptTimeout 给出单次请求的超时：不超过 ceiling，
// 并把外层 ctx 的剩余时间按剩余尝试次数均分，避免排在前面的域名吃光预算。
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
	switch strings.ToLower(mapString(data, "is_preview", "isPreview")) {
	case "true", "1", "y":
		return true
	}
	if preview := mapString(data, "preview_m3u8", "preview_url"); preview != "" &&
		(media == "" || resolveProviderURL(host+"/", media) == resolveProviderURL(host+"/", preview)) {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.host, "/")+"/api"+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Origin", c.host)
	req.Header.Set("Referer", c.host+"/home")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("version", huangdouVersion)
	req.Header.Set("deviceType", huangdouDeviceType)
	req.Header.Set("requestId", rid)
	req.Header.Set("sessionId", c.sessionID)
	resp, err := c.d.doPreparedCatalogRequest(req, timeout, func(prepared *http.Request) {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signRaw := "Dart|" + c.sessionID + "|" + rid + "|" + timestamp + "|" + path
		signSum := sha256.Sum256([]byte(signRaw))
		prepared.Header.Set("time", timestamp)
		prepared.Header.Set("sign", hex.EncodeToString(signSum[:])+"-"+timestamp)
	})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	blob, err := io.ReadAll(io.LimitReader(resp.Body, providerMaxBodyBytes+1))
	if err != nil {
		return err
	}
	if len(blob) > providerMaxBodyBytes {
		return fmt.Errorf("huangdou response exceeds %d bytes", providerMaxBodyBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("huangdou HTTP %d: %s", resp.StatusCode, truncate(string(blob), 200))
	}
	decoded, err := huangdouDecode(blob, key)
	if err != nil {
		return err
	}
	if envelope, ok := decoded.(map[string]any); ok {
		if status := mapString(envelope, "status"); status != "" && status != "y" {
			return &huangdouAPIError{code: mapString(envelope, "errorCode", "error_code", "code"), message: "黄豆接口拒绝请求: " + firstNonEmpty(mapString(envelope, "msg", "message", "error"), status)}
		}
	}
	c.d.providerMu.Lock()
	c.d.providerHosts[sourceHuangdou] = c.host
	c.d.providerMu.Unlock()
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
	if len(blob) > providerMaxBodyBytes {
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
		plain, err = io.ReadAll(io.LimitReader(zr, providerMaxBodyBytes+1))
		_ = zr.Close()
		if err != nil {
			return nil, err
		}
		if len(plain) > providerMaxBodyBytes {
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
	return pkcs7Unpad(out, block.BlockSize())
}

func huangdouDramaFromMap(item map[string]any) Drama {
	sourceID := strings.TrimPrefix(firstNonEmpty(mapString(item, "id"), mapString(item, "drama_id")), "rp_")
	if sourceID == "" {
		return Drama{}
	}
	title := firstNonEmpty(mapString(item, "name", "title", "t"), sourceID)
	cover := firstNonEmpty(mapString(item, "img_y", "img_x", "img", "cover", "pic"))
	remark := firstNonEmpty(mapString(item, "update_label"), mapString(item, "corner"))
	releaseStatus := releaseStatusFromRemark(remark)
	if remark == "" {
		if eps := mapString(item, "episode_count"); eps != "" {
			remark = "全" + eps + "集"
		}
	}
	category := firstNonEmpty(mapString(item, "category", "category_name", "categoryName"), "未分类")
	return Drama{VIP: huangdouVIPFlag(item), ID: providerDramaID(sourceHuangdou, sourceID), Source: sourceHuangdou, SourceID: sourceID, Title: title, Name: title, Desc: firstNonEmpty(mapString(item, "description"), mapString(item, "summary")), Intro: firstNonEmpty(mapString(item, "description"), mapString(item, "summary")), Cover: cover, CoverURL: cover, CategoryName: category, ChannelName: "黄豆", Remark: remark, TotalEpisode: mapString(item, "episode_count"), EpisodeCount: mapString(item, "episode_count"), Tags: mapStringSlice(item, "tags"), ReleaseStatus: releaseStatus, Heat: mapString(item, "hot_rate"), Views: normalizeViews(mapString(item, "click")), OnlineDate: providerReleaseDate(mapString(item, "issue_date"))}
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
