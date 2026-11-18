package core

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const WebMediaPrefix = "/media/"

type webMediaRequest struct{}

type WebLibrary struct{ engine *nativeEngine }

type WebSource struct {
	ID, Name string
	Search   bool
}

type WebCatalog = nativeCatalogResult
type WebSeason = nativeSeason

type WebPlayback struct {
	URL       string `json:"url"`
	Quality   int    `json:"quality"`
	Qualities []int  `json:"qualities"`
}

var ErrWebQualityUnavailable = errors.New("站源没有该画质")

func NewWebLibrary(directory string) (*WebLibrary, error) {
	engine, err := newNativeEngine(directory, filepath.Join(directory, "cache"), "")
	if err != nil {
		return nil, err
	}
	return &WebLibrary{engine: engine}, nil
}

func (library *WebLibrary) Sources() []WebSource {
	var sources []WebSource
	for _, source := range library.engine.sourceList() {
		if source.Available {
			sources = append(sources, WebSource{source.ID, source.Name, source.Search})
		}
	}
	return sources
}

func (library *WebLibrary) Categories(ctx context.Context, source string) ([]Category, error) {
	return library.engine.nativeCategories(ctx, source, false)
}

func (library *WebLibrary) Catalog(ctx context.Context, source, category, query string, page int) (WebCatalog, error) {
	return library.engine.nativeCatalog(ctx, nativeInput{Source: source, Category: category, Query: query, Page: page})
}

func (library *WebLibrary) Detail(ctx context.Context, id string) (LibraryDrama, []Chapter, error) {
	detail, err := library.engine.nativeDetail(ctx, LibraryDrama{ID: id})
	if err != nil {
		return LibraryDrama{}, nil, err
	}
	result := detail.(map[string]any)
	return result["drama"].(LibraryDrama), result["chapters"].([]Chapter), nil
}

func (library *WebLibrary) Seasons(ctx context.Context, id string) ([]WebSeason, error) {
	result, err := library.engine.nativeSeasons(ctx, LibraryDrama{ID: id})
	if err != nil {
		return nil, err
	}
	return result.(map[string]any)["items"].([]nativeSeason), nil
}

func (library *WebLibrary) Play(ctx context.Context, drama LibraryDrama, chapter Chapter, index, quality int) (WebPlayback, error) {
	if !nativeChapterAvailable(drama, chapter) {
		return WebPlayback{}, errors.New("剧集信息无效")
	}
	number, err := strconv.Atoi(chapter.EpisodeString(index))
	if err != nil || number < 1 {
		number = index
	}
	task := Task{DramaID: drama.ID, DramaTitle: drama.Title, Chapter: chapter, Index: number}
	resolved, err := library.engine.downloader.resolveProviderMedia(ctx, task)
	if err != nil {
		return WebPlayback{}, err
	}
	choice := nativePlaybackChoices(resolved, 0)
	if len(choice.media) == 0 {
		return WebPlayback{}, errors.New("站源没有可播放的线路")
	}
	stream, err := library.engine.streamServer()
	if err != nil {
		return WebPlayback{}, err
	}
	failure := ErrWebQualityUnavailable
	for _, media := range choice.media {
		if quality != 0 && media.Quality != quality {
			continue
		}
		if err := ctx.Err(); err != nil {
			return WebPlayback{}, err
		}
		if media.Credentials != nil && !media.Credentials.Expires.IsZero() && !time.Now().Before(media.Credentials.Expires) {
			failure = errors.New("播放凭证已过期，请重试")
			continue
		}
		address, token := stream.nativeOpen(media)
		if len(media.CENCKey) > 0 {
			if err := stream.nativeDecrypt(ctx, token, media); err != nil {
				failure = err
				stream.nativeRelease(token)
				continue
			}
		}
		return WebPlayback{
			URL:     WebMediaPrefix + strings.TrimPrefix(address, stream.address+"/"),
			Quality: media.Quality, Qualities: choice.qualities,
		}, nil
	}
	return WebPlayback{}, failure
}

func (library *WebLibrary) ServeMedia(w http.ResponseWriter, r *http.Request) {
	library.engine.mu.Lock()
	stream := library.engine.stream
	library.engine.mu.Unlock()
	if stream == nil {
		http.NotFound(w, r)
		return
	}
	handler := http.StripPrefix(WebMediaPrefix, http.HandlerFunc(stream.nativeServe))
	handler.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), webMediaRequest{}, true)))
}
