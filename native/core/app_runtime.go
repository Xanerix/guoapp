package core

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	dataDir         string
	MaxPagesPerSort int
	PageSize        int
	Retries         int
	InsecureTLS     bool
}

type Downloader struct {
	rankings      rankingCache
	cfg           Config
	client        *http.Client
	reserveMu     sync.Mutex
	reserveNext   uint64
	reservations  map[uint64]func()
	providerMu    sync.Mutex
	providerHosts map[string]string
	providersMu   sync.Mutex
	providers     map[string]Provider
	limiter       *requestLimiter
	proxyRouter   *proxyRouter
	directOnce    sync.Once
	direct        *http.Client
	diagnostics   *diagnosticLog
}

func DefaultConfig() Config { return Config{MaxPagesPerSort: 50, PageSize: 30, Retries: 2} }

type LibraryDrama struct {
	MetadataSchema int      `json:"metadataSchema"`
	ID             string   `json:"id"`
	Source         string   `json:"source"`
	SourceID       string   `json:"sourceId"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Cover          string   `json:"cover"`
	Episodes       int      `json:"episodes"`
	Category       string   `json:"category"`
	VIP            *bool    `json:"vip"`
	Heat           string   `json:"heat,omitempty"`
	Views          string   `json:"views,omitempty"`
	OnlineDate     string   `json:"onlineDate,omitempty"`
	Score          string   `json:"score,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	ReleaseStatus  string   `json:"releaseStatus,omitempty"`
	Badge          string   `json:"badge,omitempty"`
}

type nativeInput struct {
	SystemProxy     nativeSystemProxy       `json:"systemProxy"`
	Settings        nativeResourceSettings  `json:"settings"`
	JobIDs          []string                `json:"jobIds"`
	PlaybackSession string                  `json:"playbackSession"`
	StartMS         int64                   `json:"startMs"`
	DurationMS      int64                   `json:"durationMs"`
	Board           string                  `json:"board"`
	Entries         []nativeDownloadEpisode `json:"entries"`
	JobID           string                  `json:"jobId"`
	Command         string                  `json:"command"`
	Action          string                  `json:"action"`
	Directory       string                  `json:"directory"`
	Downloads       string                  `json:"downloads"`
	Source          string                  `json:"source"`
	Page            int                     `json:"page"`
	Query           string                  `json:"query"`
	Category        string                  `json:"category"`
	Drama           LibraryDrama            `json:"drama"`
	Chapter         Chapter                 `json:"chapter"`
	Index           int                     `json:"index"`
	Quality         int                     `json:"quality"`
	Session         string                  `json:"session"`
	Sequence        int64                   `json:"sequence"`
	Force           bool                    `json:"force"`
	Version         string                  `json:"version"`
	Since           string                  `json:"since"`
	Account         json.RawMessage         `json:"account"`
}

type nativeCatalogResult struct {
	Items       []LibraryDrama `json:"items"`
	HasMore     bool           `json:"hasMore"`
	Page        int            `json:"page"`
	Warning     string         `json:"warning,omitempty"`
	LocalSearch bool           `json:"localSearch"`
	Fresh       bool           `json:"fresh"`
	saveError   error
}

type nativePlan struct {
	ExpiresAt       int64             `json:"expiresAt,omitempty"`
	PrefetchedBytes int64             `json:"prefetchedBytes,omitempty"`
	DanmakuID       string            `json:"danmakuId,omitempty"`
	Local           bool              `json:"local"`
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers"`
	Key             string            `json:"decryptionKey,omitempty"`
	Quality         int               `json:"quality"`
	Qualities       []int             `json:"qualities"`
	Session         string            `json:"session,omitempty"`
	RouteIndex      int               `json:"routeIndex"`
	RouteCount      int               `json:"routeCount"`
}

type nativeEngine struct {
	settingsMu       sync.Mutex
	settings         nativeResourceSettings
	readMu           sync.Mutex
	reads            map[string]nativeReadRequest
	readCount        int
	work             map[string]bool
	downloads        *nativeDownloads
	downloader       *Downloader
	directory        string
	defaultDownloads string
	mu               sync.Mutex
	catalogs         map[string][]LibraryDrama
	catalogStates    map[string]nativeCatalogState
	categoryOptions  map[string][]Category
	providerStates   map[string]json.RawMessage
	recommendations  map[string]nativeRecommendationState
	catalogSaveError error
	covers           *nativeCoverCache
	stream           *nativeStreamServer
	playbacks        map[string]nativePlaybackChoice
	playbackMu       sync.Mutex
	playbackSequence int64
	playbackCancel   context.CancelFunc
	sourceCatalogMu  sync.Mutex
	sourceCatalogs   map[string]chan struct{}
}

var nativeState struct {
	sync.Mutex
	engine *nativeEngine
}

func NativeText(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]any:
		for _, key := range []string{"url", "src", "cover", "image", "pic"} {
			if text := NativeText(v[key]); text != "" {
				return text
			}
		}
	case []any:
		for _, entry := range v {
			if text := NativeText(entry); text != "" {
				return text
			}
		}
	case json.Number:
		return string(v)
	case float64:
		return strconv.Itoa(int(v))
	case int:
		return strconv.Itoa(v)
	}
	return ""
}

func NativeNormalize(drama Drama) LibraryDrama {
	cover := ""
	for _, value := range []any{drama.Cover, drama.CoverURL, drama.CoverURLSnake, drama.Image, drama.ImageURL, drama.ImageURLSnake, drama.Img, drama.Pic, drama.Picture, drama.Poster, drama.Thumb, drama.Thumbnail} {
		candidate := NativeText(value)
		if strings.HasPrefix(candidate, "//") {
			candidate = "https:" + candidate
		}
		if IsProviderHTTPMediaURL(candidate) {
			cover = candidate
			break
		}
	}
	episodes := 0
	for _, value := range []any{drama.TotalEpisode, drama.TotalEpisodeSnake, drama.EpisodeCount, drama.EpisodeCountSnake, drama.ChapterCount, drama.ChapterCountSnake, drama.Total, drama.Episodes} {
		if count, _ := strconv.Atoi(NativeText(value)); count > episodes {
			episodes = count
		}
	}
	source, sourceID, _ := SplitProviderDramaID(drama.ID)
	return LibraryDrama{MetadataSchema: 1, ID: drama.ID, Source: source, SourceID: sourceID, Title: drama.DisplayTitle(),
		Description: FirstNonEmpty(drama.Desc, drama.Intro), Cover: cover, Episodes: episodes,
		Category: nativeDramaCategory(drama), VIP: drama.VIP,
		Heat: drama.Heat, Views: drama.Views, OnlineDate: ProviderReleaseDate(drama.OnlineDate), Score: NativeDramaScore(drama.Score),
		Tags: append([]string(nil), drama.Tags...), ReleaseStatus: drama.ReleaseStatus, Badge: drama.Badge}
}

func NativeDramaScore(score string) string {
	score = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(score), "评分"), "分"))
	value, err := strconv.ParseFloat(score, 64)
	if err != nil || !(value > 0 && value <= 10) {
		return ""
	}
	return strconv.FormatFloat(value, 'f', 1, 64)
}

func nativeDramaCategory(drama Drama) string {
	category := FirstNonEmpty(drama.CategoryName, drama.CategoryNameSnake, drama.TypeName, drama.TypeNameSnake, drama.SortName, drama.SortNameSnake, drama.Category)
	if category != "" {
		return category
	}
	switch drama.ChannelName {
	case "真人剧", "短剧", "漫剧", "AI 剧", "AI剧", "AI 短剧", "AI 漫剧", "AI 换脸", "AI 魔改":
		return drama.ChannelName
	}
	return ""
}

func newNativeEngine(directory, downloads string) (*nativeEngine, error) {
	if !filepath.IsAbs(directory) {
		return nil, errors.New("应用数据目录无效")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	cfg := DefaultConfig()
	cfg.dataDir = directory
	router := &proxyRouter{}
	d := &Downloader{cfg: cfg, providerHosts: map[string]string{}, proxyRouter: router,
		limiter: newRequestLimiter(3, 250*time.Millisecond), diagnostics: newDiagnosticLog(directory)}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	transport.MaxIdleConnsPerHost = 8
	transport.ResponseHeaderTimeout = 20 * time.Second
	transport.Proxy = router.proxy
	cdn := newCDNTransport(transport, newDNSResolver(transport))
	d.client = &http.Client{Transport: newHuangguoBrowserTransport(cdn, d), Timeout: 45 * time.Second,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("站源重定向次数过多")
			}
			if len(via) > 0 && !strings.EqualFold(request.URL.Host, via[0].URL.Host) {
				request.Header.Del("X-Preview-Token")
			}
			return nil
		}}
	engine := &nativeEngine{downloader: d, directory: directory, defaultDownloads: nativeDefaultDownloads(directory), catalogs: map[string][]LibraryDrama{}, catalogStates: map[string]nativeCatalogState{}}
	if filepath.IsAbs(downloads) {
		engine.defaultDownloads = filepath.Clean(downloads)
	}
	engine.loadResourceSettings()
	d.loadRankingCache()
	engine.loadCatalogCache()
	engine.covers = newNativeCoverCache(directory, d)
	engine.downloads = newNativeDownloads(engine)
	return engine, nil
}

func NativeRequest(raw string) (result string) {
	defer func() {
		if recover() != nil {
			result = `{"ok":false,"error":"本地核心处理失败，请重试"}`
		}
	}()
	var input nativeInput
	if len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &input) != nil {
		return `{"ok":false,"error":"请求格式无效"}`
	}
	data, err := nativeDispatch(input)
	envelope := map[string]any{"ok": err == nil}
	if err != nil {
		envelope["error"] = PublicError(err).Error()
		if errors.Is(err, errNativeLocalFile) {
			envelope["code"] = "local_media"
		}
		if errors.Is(err, ErrSourceLogin) {
			envelope["code"] = "source_login"
		}
		var backoff *RequestBackoff
		if errors.As(err, &backoff) {
			envelope["code"] = "source_blocked"
			envelope["retryAt"] = backoff.until
		}
	} else {
		envelope["data"] = data
	}
	body, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		return `{"ok":false,"error":"无法读取站源返回的数据"}`
	}
	return string(body)
}

func nativeDispatch(input nativeInput) (any, error) {
	if err := nativeAuthorizeInput(input); err != nil {
		return nil, err
	}
	nativeState.Lock()
	if input.Action == "initialize" {
		if nativeState.engine == nil {
			engine, err := newNativeEngine(input.Directory, input.Downloads)
			if err != nil {
				nativeState.Unlock()
				return nil, err
			}
			nativeState.engine = engine
		}
		engine := nativeState.engine
		nativeState.Unlock()
		return map[string]any{"version": "0.2.17", "standalone": true, "flavor": buildFlavor(), "sources": engine.sourceList()}, nil
	}
	engine := nativeState.engine
	nativeState.Unlock()
	if engine == nil {
		return nil, errors.New("应用核心尚未就绪，请重新打开应用")
	}
	duration := 60 * time.Second
	if input.Action == "moveDownloads" {
		duration = 10 * time.Minute
	} else if input.Action == "danmaku" {
		duration = 10 * time.Second
	} else if input.Action == "preload" {
		duration = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	if input.Action == "danmaku" || input.Action == "preload" || input.Session != "" && (input.Action == "catalog" || input.Action == "categories" || input.Action == "suggestions" || input.Action == "recommendations" || input.Action == "metadata") {
		work, finish, err := engine.beginRead(ctx, input)
		if err != nil {
			return nil, err
		}
		defer finish()
		ctx = work
	}
	switch input.Action {
	case "updateSystemProxy":
		return true, engine.updateSystemProxy(input.SystemProxy)
	case "resourceSettings":
		return engine.resourceSettings(), nil
	case "saveResourceSettings":
		return engine.saveResourceSettings(input.Settings)
	case "controlDownloadBatch":
		return engine.downloads.controlBatch(ctx, input.JobIDs, input.Command)
	case "preload":
		return engine.nativePreload(ctx, input)
	case "danmaku":
		return engine.nativeDanmaku(ctx, input)
	case "cancelRead":
		engine.cancelRead(input)
		return true, nil
	case "recommendations":
		return engine.nativeRecommendations(ctx, input)
	case "cachedRecommendations":
		return engine.cachedRecommendations(nativeActionSource(input), input.Category)
	case "hongguoDevices", "sourceDevices":
		return engine.sourceDevices(ctx, nativeActionSource(input), input.Command == "register")
	case "soraniAccount", "sourceAccount":
		return engine.sourceAccount(ctx, nativeActionSource(input), input.Command, input.Account)
	case "rankingBoards":
		boards := []RankingBoard{}
		for _, board := range allRankingBoards() {
			if nativeSourceAvailable(board.Source) {
				boards = append(boards, board)
			}
		}
		return map[string]any{"items": boards}, nil
	case "rankings":
		return engine.nativeRanking(ctx, input)
	case "suggestions":
		items, err := engine.suggestions(ctx, nativeActionSource(input), input.Query)
		return map[string]any{"items": items}, err
	case "downloadDirectory":
		engine.downloads.mu.Lock()
		root := engine.downloads.root
		engine.downloads.mu.Unlock()
		return map[string]string{"directory": root}, nil
	case "storage":
		return engine.storage()
	case "moveDownloads":
		leftover, reason, err := engine.moveDownloads(ctx, input.Directory)
		return map[string]string{"leftover": leftover, "leftoverReason": reason}, err
	case "workLease":
		count, err := engine.workLease(input.JobID, input.Command)
		return map[string]int{"count": count}, err
	case "downloads":
		return engine.downloads.snapshotSince(input.Since)
	case "enqueueDownloads":
		added, err := engine.downloads.enqueueContext(ctx, input)
		return map[string]int{"added": added}, err
	case "controlDownloads":
		return true, engine.downloads.control(input.JobID, input.Command)
	case "beginConversion":
		return engine.downloads.beginConversion(input.JobID)
	case "finishConversion":
		return true, engine.downloads.finishConversion(input.JobID, input.Command)
	case "localPlayback":
		plan, _, err := engine.downloads.localPlan(input.Drama.ID, input.Index)
		return plan, err
	case "catalog":
		return engine.nativeCatalog(ctx, input)
	case "cached":
		if !ValidNativeCategory(canonicalProviderSource(input.Source), input.Category) {
			return nil, errors.New("内容分类无效")
		}
		return engine.nativeCached(nativeCatalogKey(input.Source, input.Category)), nil
	case "categories":
		items, err := engine.nativeCategories(ctx, input.Source, input.Force)
		return map[string]any{"items": items}, err
	case "cover":
		return engine.loadCover(ctx, input.Drama, input.Force)
	case "prepareCover":
		return engine.prepareCover(ctx, input.Drama)
	case "detail":
		return engine.nativeDetail(ctx, input.Drama)
	case "seasons":
		return engine.nativeSeasons(ctx, input.Drama)
	case "metadata":
		return engine.nativeMetadata(ctx, input.Drama)
	case "followCheck":
		return engine.nativeFollowCheck(ctx, input.Drama)
	case "resolve", "fallback":
		playback, finish, err := engine.nativeBeginPlayback(ctx, input.Sequence)
		if err != nil {
			return nil, err
		}
		defer finish()
		if input.Action == "fallback" {
			return engine.nativeNextPlayback(playback, input.Session)
		}
		return engine.nativeResolve(playback, input)
	case "cancelPlayback":
		engine.nativeCancelPlayback(input.Sequence)
		return true, nil
	case "release":
		engine.nativeReleasePlayback(input.Session)
		return true, nil
	case "checkUpdate":
		return engine.checkUpdate(ctx, input.Version)
	case "reportUsage":
		return true, engine.reportUsage(ctx, input.Version)
	default:
		return nil, errors.New("不支持的应用操作")
	}
}

func (engine *nativeEngine) nativeCatalog(ctx context.Context, input nativeInput) (nativeCatalogResult, error) {
	source := canonicalProviderSource(input.Source)
	if !registeredProviderSource(source) {
		return nativeCatalogResult{}, errors.New("请选择有效站源")
	}
	category := strings.TrimSpace(input.Category)
	if !ValidNativeCategory(source, category) {
		return nativeCatalogResult{}, errors.New("内容分类无效")
	}
	cacheKey := nativeCatalogKey(source, category)
	if err := ctx.Err(); err != nil {
		return nativeCatalogResult{}, err
	}
	page := max(1, input.Page)
	query := strings.TrimSpace(input.Query)
	if query == "" {
		if retried, err := engine.retryCatalogSave(); retried {
			cached := engine.nativeCached(cacheKey)
			if err != nil || len(cached.Items) > 0 {
				return cached, nil
			}
		}
	}
	if query == "" && page == 1 && !input.Force {
		if cached := engine.nativeCached(cacheKey); cached.Fresh {
			return cached, nil
		}
	}
	d := engine.downloader
	result := nativeCatalogResult{Items: []LibraryDrama{}, Page: page}
	registered, hasProvider := d.provider(source)
	if searcher, ok := registered.(WarningSearcher); ok && query != "" {
		items, more, warning, err := searcher.SearchWithWarning(ctx, page, query)
		if err != nil {
			return result, err
		}
		for _, drama := range items {
			result.Items = append(result.Items, NativeNormalize(drama))
		}
		result.Warning, result.HasMore = warning, more
		return result, nil
	}
	if searcher, ok := registered.(Searcher); ok && query != "" {
		items, more, err := searcher.Search(ctx, page, query)
		if err != nil {
			return result, err
		}
		for _, drama := range items {
			result.Items = append(result.Items, NativeNormalize(drama))
		}
		result.HasMore = more
		return result, nil
	}
	if query != "" {
		engine.mu.Lock()
		items := append([]LibraryDrama{}, engine.catalogs[source]...)
		engine.mu.Unlock()
		for _, item := range items {
			text := NativeSearchText(item.Title + " " + item.Description + " " + strings.Join(item.Tags, " "))
			matches := true
			for _, word := range strings.Fields(query) {
				matches = matches && strings.Contains(text, NativeSearchText(word))
			}
			if matches {
				result.Items = append(result.Items, item)
			}
		}
		result.LocalSearch = true
		return result, nil
	}
	unlock, lockErr := engine.lockSourceCatalog(ctx, source)
	if lockErr != nil {
		return nativeCatalogResult{}, lockErr
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return result, err
	}
	var items []Drama
	var err error
	if !hasProvider {
		return result, errors.New("请选择有效站源")
	}
	descriptor := registered.Descriptor()
	allowEmptyCatalog := descriptor.AllowEmptyCatalog
	if descriptor.ContinuousFeed {
		engine.mu.Lock()
		known := make(map[string]bool, len(engine.catalogs[cacheKey]))
		for _, drama := range engine.catalogs[cacheKey] {
			known[drama.ID] = true
		}
		engine.mu.Unlock()
		ctx = context.WithValue(ctx, LibraryKnownKey{}, known)
		if page > 1 {
			ctx = context.WithValue(ctx, LibraryMoreKey{}, true)
		}
	}
	items, result.HasMore, err = registered.CatalogPage(ctx, page, category)
	if err != nil && len(items) == 0 {
		return result, err
	}
	if len(items) == 0 && page == 1 && !allowEmptyCatalog {
		return result, errors.New("站源暂未返回剧集，请稍后刷新")
	}
	if err != nil {
		result.Warning = PublicError(err).Error()
	}
	seen := map[string]bool{}
	for _, drama := range items {
		if drama.ID == "" || seen[drama.ID] {
			continue
		}
		seen[drama.ID] = true
		result.Items = append(result.Items, NativeNormalize(drama))
	}
	engine.saveCatalogCache(cacheKey, &result)
	return result, nil
}

func (engine *nativeEngine) nativeDetail(ctx context.Context, drama LibraryDrama) (any, error) {
	source, sourceID, valid := SplitProviderDramaID(drama.ID)
	if !valid {
		return nil, errors.New("剧集信息无效，请刷新剧库")
	}
	p, ok := engine.downloader.provider(source)
	if !ok {
		return nil, errors.New("请选择有效站源")
	}
	raw, chapters, err := p.Detail(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if len(chapters) == 0 {
		return nil, errors.New("该剧暂时没有可播放的分集")
	}
	if raw.ID == drama.ID {
		drama = MergeNativeDrama(drama, NativeNormalize(raw))
	}
	drama.Source, drama.SourceID, drama.Episodes = source, sourceID, len(chapters)
	if p.Descriptor().KeepDetailEpisodes {
		drama.Episodes = max(drama.Episodes, NativeNormalize(raw).Episodes)
	}
	warning := ""
	if err := engine.saveDetailMetadata(drama); err != nil {
		warning = err.Error()
	}
	return map[string]any{"drama": drama, "chapters": chapters, "warning": warning}, nil
}

func (engine *nativeEngine) nativeResolve(ctx context.Context, input nativeInput) (nativePlan, error) {
	if _, _, valid := SplitProviderDramaID(input.Drama.ID); !valid {
		return nativePlan{}, errors.New("剧集信息无效")
	}
	if !input.Force && engine.downloads != nil {
		if plan, found, err := engine.downloads.localPlan(input.Drama.ID, input.Index); found || err != nil {
			return plan, err
		}
	}
	task := Task{DramaID: input.Drama.ID, DramaTitle: input.Drama.Title, Chapter: input.Chapter, Index: input.Index}
	media, err := engine.downloader.resolveProviderMedia(ctx, task)
	if err != nil {
		return nativePlan{}, err
	}
	choice := nativePlaybackChoices(media, input.Quality)
	if p, ok := engine.downloader.provider(SourceFromDramaID(task.DramaID)); ok {
		if danmaku, ok := p.(DanmakuProvider); ok {
			if series, video, valid := danmaku.DanmakuIDs(task); valid {
				choice.danmakuSource, choice.danmakuSeries, choice.danmakuVideo = p.Descriptor().ID, series, video
			}
		}
	}
	return engine.nativeOpenPlayback(ctx, choice)
}
