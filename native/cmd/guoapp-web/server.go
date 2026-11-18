package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"duanjuapp/native/core"
)

//go:embed web
var webFiles embed.FS

var pages = template.Must(template.ParseFS(webFiles, "web/index.html"))

type library interface {
	Sources() []core.WebSource
	Categories(context.Context, string) ([]core.Category, error)
	Catalog(context.Context, string, string, string, int) (core.WebCatalog, error)
	Detail(context.Context, string) (core.LibraryDrama, []core.Chapter, error)
	Seasons(context.Context, string) ([]core.WebSeason, error)
	Play(context.Context, core.LibraryDrama, core.Chapter, int, int) (core.WebPlayback, error)
	ServeMedia(http.ResponseWriter, *http.Request)
}

type webApp struct {
	library library
	auth    webAuth
}

type link struct {
	Name, URL string
	Current   bool
}

type episode struct {
	Index int    `json:"index"`
	Label string `json:"label"`
}

type page struct {
	View, Heading, Message  string
	Sources                 []core.WebSource
	Source, Category, Query string
	ShowSearch              bool
	Categories              []link
	Items                   []core.LibraryDrama
	Previous, Next          string
	Drama                   core.LibraryDrama
	Episodes                []episode
	Playing                 int
	Playback                core.WebPlayback
}

func newWebApp(lib library, password string, secure bool) http.Handler {
	app := &webApp{library: lib, auth: webAuth{
		password: sha256.Sum256([]byte(password)), secure: secure,
		sessions: make(map[string]time.Time),
	}}
	staticFiles, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	static := http.FileServerFS(staticFiles)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /static/app.css", func(w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/static", static).ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /static/player.js", func(w http.ResponseWriter, r *http.Request) {
		http.StripPrefix("/static", static).ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /login", app.login)
	mux.HandleFunc("POST /login", app.login)
	mux.Handle("POST /logout", app.requireLogin(http.HandlerFunc(app.logout)))
	mux.Handle("GET /{$}", app.requireLogin(http.HandlerFunc(app.catalog)))
	mux.Handle("GET /title", app.requireLogin(http.HandlerFunc(app.title)))
	mux.Handle("POST /watch", app.requireLogin(http.HandlerFunc(app.title)))
	mux.Handle("POST /watch/quality", app.requireLogin(http.HandlerFunc(app.title)))
	mux.Handle("GET /watch/seasons", app.requireLogin(http.HandlerFunc(app.seasons)))
	mux.Handle("GET /watch/episodes", app.requireLogin(http.HandlerFunc(app.episodes)))
	mux.Handle("GET "+core.WebMediaPrefix, app.requireLogin(http.HandlerFunc(app.library.ServeMedia)))

	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "请求来源校验失败，请直接打开小果影视页面后重试。", http.StatusForbidden)
	}))
	protected := protection.Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; img-src 'self' https: http:; media-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		protected.ServeHTTP(w, r)
	})
}

func (app *webApp) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if app.auth.authorized(r) {
			next.ServeHTTP(w, r)
		} else if strings.HasPrefix(r.URL.Path, core.WebMediaPrefix) {
			http.Error(w, "请重新登录", http.StatusUnauthorized)
		} else {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
		}
	})
}

func (app *webApp) login(w http.ResponseWriter, r *http.Request) {
	if app.auth.authorized(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	data := page{View: "login", Heading: "登录"}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "请求格式无效", http.StatusBadRequest)
			return
		}
		status = app.auth.login(w, r.PostForm.Get("password"))
		switch status {
		case http.StatusSeeOther:
			http.Redirect(w, r, "/", status)
			return
		case http.StatusUnauthorized:
			data.Message = "密码有误，请重试。"
		case http.StatusTooManyRequests:
			data.Message = "尝试次数过多，请稍等一分钟。"
		default:
			data.Message = "暂时无法登录，请稍后重试。"
		}
	}
	render(w, status, data)
}

func (app *webApp) logout(w http.ResponseWriter, r *http.Request) {
	app.auth.logout(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func catalogURL(source, category, query string, number int) string {
	values := url.Values{"source": {source}, "page": {strconv.Itoa(number)}}
	if category != "" {
		values.Set("category", category)
	}
	if query != "" {
		values.Set("q", query)
	}
	return "/?" + values.Encode()
}

func (app *webApp) catalog(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	data := page{View: "catalog", Heading: "发现", Query: strings.TrimSpace(values.Get("q")),
		Category: values.Get("category"), Sources: app.library.Sources()}
	if len(data.Sources) == 0 {
		data.Message = "还没有可用站源。请在服务端添加站源模块后重启。"
		render(w, http.StatusOK, data)
		return
	}
	selected := data.Sources[0]
	for _, source := range data.Sources {
		if source.ID == values.Get("source") {
			selected = source
			break
		}
	}
	data.Source, data.ShowSearch = selected.ID, selected.Search
	current, _ := strconv.Atoi(values.Get("page"))
	current = max(1, current)
	if current > 200 || len(data.Query) > 240 || len(data.Category) > 128 {
		http.Error(w, "搜索或分页参数无效", http.StatusBadRequest)
		return
	}
	if data.Query != "" {
		data.Heading = "搜索结果"
		data.Category = ""
		if !selected.Search {
			data.Message = "此站源暂不支持搜索，请切换站源。"
			render(w, http.StatusOK, data)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if data.Query == "" {
		categories, err := app.library.Categories(ctx, data.Source)
		if err != nil && data.Category != "" {
			categories = []core.Category{{Name: "全部"}}
		}
		for _, category := range categories {
			data.Categories = append(data.Categories, link{category.Name, catalogURL(data.Source, category.ID, "", 1), data.Category == category.ID})
		}
	}
	result, err := app.library.Catalog(ctx, data.Source, data.Category, data.Query, current)
	if err != nil {
		data.Message = "暂时无法读取剧集，请刷新重试。"
		render(w, http.StatusBadGateway, data)
		return
	}
	data.Items, data.Message = result.Items, result.Warning
	if len(data.Items) == 0 && data.Message == "" {
		data.Message = "没有找到剧集。"
	}
	if current > 1 {
		data.Previous = catalogURL(data.Source, data.Category, data.Query, current-1)
	}
	if result.HasMore && current < 200 {
		data.Next = catalogURL(data.Source, data.Category, data.Query, current+1)
	}
	render(w, http.StatusOK, data)
}

func webEpisodes(chapters []core.Chapter) []episode {
	result := make([]episode, 0, len(chapters))
	for i, chapter := range chapters {
		label := strings.TrimSpace(chapter.Title)
		if label == "" {
			label = "第 " + chapter.EpisodeString(i+1) + " 集"
		}
		result = append(result, episode{i + 1, label})
	}
	return result
}

func (app *webApp) seasons(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if _, _, valid := core.SplitProviderDramaID(id); !valid || len(id) > 512 {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	seasons, err := app.library.Seasons(ctx, id)
	if err != nil {
		http.Error(w, "暂时无法读取季列表", http.StatusBadGateway)
		return
	}
	type item struct {
		ID     string `json:"id"`
		Number int    `json:"number"`
	}
	items := make([]item, 0, len(seasons))
	for _, season := range seasons {
		items = append(items, item{season.Drama.ID, season.Number})
	}
	writeJSON(w, items)
}

func (app *webApp) episodes(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if _, _, valid := core.SplitProviderDramaID(id); !valid || len(id) > 512 {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	_, chapters, err := app.library.Detail(ctx, id)
	if err != nil {
		http.Error(w, "暂时无法读取分集", http.StatusBadGateway)
		return
	}
	writeJSON(w, webEpisodes(chapters))
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(value)
}

func (app *webApp) title(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	index, quality := 0, 0
	changeQuality := r.URL.Path == "/watch/quality"
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "请求格式无效", http.StatusBadRequest)
			return
		}
		id = r.PostForm.Get("id")
		index, _ = strconv.Atoi(r.PostForm.Get("episode"))
		if index < 1 {
			http.Error(w, "分集参数无效", http.StatusBadRequest)
			return
		}
		if changeQuality {
			var err error
			quality, err = strconv.Atoi(r.PostForm.Get("quality"))
			if err != nil || quality < 0 || quality > 4320 {
				http.Error(w, "画质参数无效", http.StatusBadRequest)
				return
			}
		}
	}
	if len(id) > 512 {
		http.Error(w, "剧集参数无效", http.StatusBadRequest)
		return
	}
	if _, _, valid := core.SplitProviderDramaID(id); !valid {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	drama, chapters, err := app.library.Detail(ctx, id)
	if err != nil {
		if changeQuality {
			http.Error(w, "暂时无法读取详情", http.StatusBadGateway)
			return
		}
		render(w, http.StatusBadGateway, page{View: "error", Heading: "剧集暂不可用", Message: "暂时无法读取详情，请返回列表重试。"})
		return
	}
	if index > len(chapters) {
		http.Error(w, "分集不存在", http.StatusNotFound)
		return
	}
	data := page{View: "title", Heading: drama.Title, Drama: drama, Episodes: webEpisodes(chapters)}
	status := http.StatusOK
	if index > 0 {
		playback, err := app.library.Play(ctx, drama, chapters[index-1], index, quality)
		if changeQuality {
			switch {
			case errors.Is(err, core.ErrWebQualityUnavailable):
				http.Error(w, "站源没有该画质", http.StatusUnprocessableEntity)
			case err != nil:
				http.Error(w, "切换画质失败", http.StatusBadGateway)
			default:
				writeJSON(w, playback)
			}
			return
		}
		data.View, data.Playing, data.Playback = "watch", index, playback
		if err != nil {
			data.Message = "这一集暂时无法播放，请重试或选择其他分集。"
			status = http.StatusBadGateway
		}
	}
	render(w, status, data)
}

func render(w http.ResponseWriter, status int, data page) {
	var content bytes.Buffer
	if err := pages.ExecuteTemplate(&content, "index.html", data); err != nil {
		http.Error(w, "页面加载失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = content.WriteTo(w)
}
