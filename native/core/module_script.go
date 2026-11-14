package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"time"

	"github.com/dop251/goja"
)

const (
	moduleInstanceLimit = 4
	moduleCallTimeout   = 60 * time.Second
	moduleQuickTimeout  = 5 * time.Second
)

var moduleInvoker = goja.MustCompile("module-invoker", `(function(exports) {
	return function(name, encoded) {
		var target = exports[name];
		if (typeof target !== "function") throw new Error("模块未导出函数 " + name);
		var result = target.apply(exports, JSON.parse(encoded));
		if (result instanceof Promise) throw new Error("模块函数 " + name + " 不能是异步函数");
		return JSON.stringify(result === undefined ? null : result);
	};
})`, true)

type jsProvider struct {
	module *jsModule
	host   Host
	slots  chan struct{}
	idle   chan *jsInstance
}

type jsInstance struct {
	vm     *goja.Runtime
	invoke goja.Callable
	ctx    context.Context
}

type jsFetchRequest struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Headers         map[string]string `json:"headers"`
	Body            string            `json:"body"`
	TimeoutMS       int64             `json:"timeoutMs"`
	FollowRedirects bool              `json:"followRedirects"`
	AnyStatus       bool              `json:"anyStatus"`
}

type jsFetchResponse struct {
	Status    int                 `json:"status"`
	Headers   map[string][]string `json:"headers"`
	Body      string              `json:"body"`
	Blocked   bool                `json:"blocked"`
	Oversized bool                `json:"oversized"`
}

type jsDramaPage struct {
	Items   []Drama `json:"items"`
	HasMore bool    `json:"hasMore"`
}

type jsDetail struct {
	Drama    Drama     `json:"drama"`
	Chapters []Chapter `json:"chapters"`
}

type jsMedia struct {
	URL        string    `json:"url"`
	Referer    string    `json:"referer"`
	Playlist   string    `json:"playlist"`
	Quality    int       `json:"quality"`
	DurationMS int64     `json:"durationMs"`
	Variants   []jsMedia `json:"variants"`
}

func (media jsMedia) providerMedia() ProviderMedia {
	converted := ProviderMedia{URL: media.URL, Referer: media.Referer, Playlist: media.Playlist, Quality: media.Quality,
		Duration: time.Duration(media.DurationMS) * time.Millisecond}
	for _, variant := range media.Variants {
		converted.Variants = append(converted.Variants, variant.providerMedia())
	}
	return converted
}

func (module *jsModule) provider(host Host) Provider {
	p := &jsProvider{module: module, host: host,
		slots: make(chan struct{}, moduleInstanceLimit), idle: make(chan *jsInstance, moduleInstanceLimit)}
	module.active.Store(p)
	return p
}

func (module *jsModule) validCategory(category string) bool {
	p := module.active.Load()
	if p == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), moduleQuickTimeout)
	defer cancel()
	var valid bool
	return p.call(ctx, "validCategory", &valid, category) == nil && valid
}

func (p *jsProvider) Descriptor() SourceDescriptor { return p.module.descriptor() }

func (p *jsProvider) ProvidesCapability(capability reflect.Type) bool {
	return p.module.capabilities[capability]
}

func (p *jsProvider) Categories(ctx context.Context, force bool) ([]Category, error) {
	var categories []Category
	return categories, p.call(ctx, "categories", &categories, force)
}

func (p *jsProvider) CatalogPage(ctx context.Context, page int, category string) ([]Drama, bool, error) {
	var result jsDramaPage
	err := p.call(ctx, "catalogPage", &result, page, category)
	return result.Items, result.HasMore, err
}

func (p *jsProvider) Search(ctx context.Context, page int, query string) ([]Drama, bool, error) {
	var result jsDramaPage
	err := p.call(ctx, "search", &result, page, query)
	return result.Items, result.HasMore, err
}

func (p *jsProvider) Detail(ctx context.Context, sourceID string) (Drama, []Chapter, error) {
	var result jsDetail
	err := p.call(ctx, "detail", &result, sourceID)
	return result.Drama, result.Chapters, err
}

func (p *jsProvider) ResolveMedia(ctx context.Context, task Task) (ProviderMedia, error) {
	var media jsMedia
	if err := p.call(ctx, "resolveMedia", &media, task); err != nil {
		return ProviderMedia{}, err
	}
	return media.providerMedia(), nil
}

func (p *jsProvider) call(ctx context.Context, function string, result any, arguments ...any) error {
	ctx, cancel := context.WithTimeout(ctx, moduleCallTimeout)
	defer cancel()
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return err
	}
	instance, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	defer func() { p.idle <- instance }()
	instance.ctx = ctx
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		instance.vm.Interrupt(ctx.Err())
		close(interrupted)
	})
	value, err := instance.invoke(goja.Undefined(), instance.vm.ToValue(function), instance.vm.ToValue(string(encoded)))
	if !stop() {
		<-interrupted
		instance.vm.ClearInterrupt()
	}
	if err != nil {
		return moduleError(ctx, err)
	}
	return json.Unmarshal([]byte(value.String()), result)
}

func moduleError(ctx context.Context, err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) && ctx.Err() != nil {
		return ctx.Err()
	}
	var exception *goja.Exception
	if !errors.As(err, &exception) {
		return err
	}
	if cause := exception.Unwrap(); cause != nil {
		return cause
	}
	if thrown, ok := exception.Value().(*goja.Object); ok {
		if message := thrown.Get("message"); message != nil && !goja.IsUndefined(message) {
			return errors.New(message.String())
		}
	}
	return errors.New(exception.Value().String())
}

func (p *jsProvider) acquire(ctx context.Context) (*jsInstance, error) {
	select {
	case instance := <-p.idle:
		return instance, nil
	default:
	}
	select {
	case instance := <-p.idle:
		return instance, nil
	case p.slots <- struct{}{}:
		instance, err := p.newInstance()
		if err != nil {
			<-p.slots
		}
		return instance, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *jsProvider) newInstance() (*jsInstance, error) {
	vm := goja.New()
	vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	instance := &jsInstance{vm: vm, ctx: context.Background()}
	module, exports := vm.NewObject(), vm.NewObject()
	if err := module.Set("exports", exports); err != nil {
		return nil, err
	}
	wrapper, err := vm.RunProgram(p.module.program)
	if err != nil {
		return nil, moduleError(instance.ctx, err)
	}
	load, _ := goja.AssertFunction(wrapper)
	if _, err := load(goja.Undefined(), module, exports, p.hostObject(instance)); err != nil {
		return nil, moduleError(instance.ctx, err)
	}
	invoker, err := vm.RunProgram(moduleInvoker)
	if err != nil {
		return nil, err
	}
	bind, _ := goja.AssertFunction(invoker)
	bound, err := bind(goja.Undefined(), module.Get("exports"))
	if err != nil {
		return nil, moduleError(instance.ctx, err)
	}
	instance.invoke, _ = goja.AssertFunction(bound)
	return instance, nil
}

func (p *jsProvider) hostObject(instance *jsInstance) *goja.Object {
	id := p.module.manifest.ID
	host := instance.vm.NewObject()
	members := map[string]any{
		"source":    id,
		"dramaId":   func(sourceID string) string { return ProviderDramaID(id, sourceID) },
		"chapterId": func(sourceID, key string) string { return ProviderChapterID(id, sourceID, key) },
		"fetch":     func(request jsFetchRequest) (jsFetchResponse, error) { return p.fetch(instance.ctx, request) },
		"loadState": func(name string) (string, error) {
			data, err := p.host.LoadState(moduleStatePrefix + id + "-" + name)
			return string(data), err
		},
		"saveState": func(name, value string) error {
			return p.host.SaveState(moduleStatePrefix+id+"-"+name, []byte(value))
		},
		"log": func(message string) {
			if downloader, ok := p.host.(*Downloader); ok {
				downloader.recordDiagnostic(diagnosticEvent{Time: time.Now(), Level: "info", Event: "module", Source: id, Message: message})
			}
		},
	}
	for name, member := range members {
		_ = host.Set(name, member)
	}
	return host
}

func (p *jsProvider) fetch(ctx context.Context, request jsFetchRequest) (jsFetchResponse, error) {
	address, err := url.Parse(request.URL)
	if err != nil || address.Scheme != "https" && address.Scheme != "http" || address.Hostname() == "" {
		return jsFetchResponse{}, errors.New("请求地址无效")
	}
	if !p.module.allowsHost(address.Hostname()) {
		return jsFetchResponse{}, fmt.Errorf("模块清单未声明访问 %s", address.Hostname())
	}
	response, err := p.host.Fetch(ctx, HTTPRequest{
		Label: p.module.manifest.ID, Method: request.Method, URL: request.URL, Header: request.Headers,
		Body: []byte(request.Body), Timeout: time.Duration(request.TimeoutMS) * time.Millisecond,
		FollowRedirects: request.FollowRedirects, AnyStatus: request.AnyStatus,
	})
	if err != nil {
		return jsFetchResponse{}, err
	}
	return jsFetchResponse{Status: response.Status, Headers: response.Header, Body: string(response.Body),
		Blocked: response.Blocked, Oversized: response.Oversized}, nil
}
