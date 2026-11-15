package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

const (
	moduleAPIVersion  = 1
	moduleRuntimeJS   = "js"
	moduleDirectory   = "modules"
	moduleManifest    = "manifest.json"
	moduleEntry       = "main.js"
	moduleEntryBytes  = 4 << 20
	moduleStatePrefix = "module-"
)

var moduleIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

var moduleCapabilityTypes = map[string][]reflect.Type{
	"search":          {reflect.TypeFor[Searcher](), reflect.TypeFor[WarningSearcher]()},
	"account":         {reflect.TypeFor[AccountProvider]()},
	"suggestions":     {reflect.TypeFor[Suggester]()},
	"recommendations": {reflect.TypeFor[Recommender]()},
	"danmaku":         {reflect.TypeFor[DanmakuProvider]()},
	"devices":         {reflect.TypeFor[DeviceProvider]()},
	"rankings":        {reflect.TypeFor[Ranker]()},
	"seasons":         {reflect.TypeFor[SeasonProvider]()},
	"cover":           {reflect.TypeFor[CoverResolver]()},
	"metadata":        {reflect.TypeFor[MetadataEnricher]()},
	"catalogState":    {reflect.TypeFor[CatalogStateful]()},
	"mediaAuth":       {reflect.TypeFor[MediaAuthorizer]()},
}

type ModuleRanking struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type ModuleManifest struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Version           string   `json:"version"`
	Description       string   `json:"description"`
	Runtime           string   `json:"runtime"`
	API               int      `json:"api"`
	Hosts             []string `json:"hosts"`
	Capabilities      []string `json:"capabilities"`
	Adult             *bool    `json:"adult"`
	AllowEmptyCatalog bool     `json:"allowEmptyCatalog"`

	Group              string   `json:"group"`
	GroupName          string   `json:"groupName"`
	EntryName          string   `json:"entryName"`
	SkipVIPPrompt      bool     `json:"skipVipPrompt"`
	Aliases            []string `json:"aliases"`
	HostSuffixes       []string `json:"hostSuffixes"`
	BaseURL            string   `json:"baseUrl"`
	NoHostRotation     bool     `json:"noHostRotation"`
	KeepDetailEpisodes bool     `json:"keepDetailEpisodes"`
	ContinuousFeed     bool     `json:"continuousFeed"`
	BrowserFingerprint bool     `json:"browserFingerprint"`

	Rankings       []ModuleRanking `json:"rankings"`
	MediaAuthPaths []string        `json:"mediaAuthPaths"`
}

type jsModule struct {
	manifest     ModuleManifest
	program      *goja.Program
	capabilities map[reflect.Type]bool
	active       atomic.Pointer[jsProvider]
}

var loadedModules = struct {
	sync.Mutex
	ids map[string]bool
}{ids: map[string]bool{}}

func readModule(directory string) (*jsModule, error) {
	raw, err := os.ReadFile(filepath.Join(directory, moduleManifest))
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(filepath.Join(directory, moduleEntry))
	if err != nil {
		return nil, err
	}
	if info.Size() > moduleEntryBytes {
		return nil, fmt.Errorf("%s 超过 %d MB", moduleEntry, moduleEntryBytes>>20)
	}
	code, err := os.ReadFile(filepath.Join(directory, moduleEntry))
	if err != nil {
		return nil, err
	}
	return parseModule(raw, code)
}

func parseModule(raw, code []byte) (*jsModule, error) {
	var manifest ModuleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("%s 无法解析：%w", moduleManifest, err)
	}
	if !moduleIDPattern.MatchString(manifest.ID) {
		return nil, errors.New("模块标识只能用小写字母、数字、下划线和连字符，最长 32 个字符")
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return nil, errors.New("模块缺少名称")
	}
	if manifest.Runtime != moduleRuntimeJS {
		return nil, fmt.Errorf("不支持的模块运行时 %q", manifest.Runtime)
	}
	if manifest.API != moduleAPIVersion {
		return nil, fmt.Errorf("模块接口版本为 %d，当前应用支持 %d", manifest.API, moduleAPIVersion)
	}
	if manifest.Adult == nil {
		return nil, errors.New("模块缺少 adult 声明")
	}
	capabilities := map[reflect.Type]bool{}
	for _, name := range manifest.Capabilities {
		types, known := moduleCapabilityTypes[name]
		if !known {
			return nil, fmt.Errorf("未知的模块能力 %q", name)
		}
		for _, capability := range types {
			capabilities[capability] = true
		}
	}
	if capabilities[reflect.TypeFor[MediaAuthorizer]()] != (len(manifest.MediaAuthPaths) > 0) {
		return nil, errors.New("媒体鉴权能力与 mediaAuthPaths 须同时声明")
	}
	for _, path := range manifest.MediaAuthPaths {
		if !strings.HasPrefix(path, "/") {
			return nil, errors.New("mediaAuthPaths 的每一项须以 / 开头")
		}
	}
	rankings := map[string]bool{}
	for _, ranking := range manifest.Rankings {
		if strings.TrimSpace(ranking.ID) == "" || strings.TrimSpace(ranking.Name) == "" || rankings[ranking.ID] {
			return nil, errors.New("榜单缺少标识、名称或标识重复")
		}
		rankings[ranking.ID] = true
	}
	if len(code) > moduleEntryBytes {
		return nil, fmt.Errorf("%s 超过 %d MB", moduleEntry, moduleEntryBytes>>20)
	}
	program, err := goja.Compile(manifest.ID+"/"+moduleEntry, "(function(module, exports, host) {"+string(code)+"\n})", true)
	if err != nil {
		return nil, err
	}
	return &jsModule{manifest: manifest, program: program, capabilities: capabilities}, nil
}

func (module *jsModule) descriptor() SourceDescriptor {
	var rankings []RankingBoard
	for _, ranking := range module.manifest.Rankings {
		rankings = append(rankings, RankingBoard{ID: ranking.ID, Source: module.manifest.ID, Name: ranking.Name, Description: ranking.Description})
	}
	return SourceDescriptor{
		Rankings:          rankings,
		ID:                module.manifest.ID,
		Name:              module.manifest.Name,
		Description:       module.manifest.Description,
		AllowEmptyCatalog: module.manifest.AllowEmptyCatalog,
		ValidCategory:     module.validCategory,

		Group:              module.manifest.Group,
		GroupName:          module.manifest.GroupName,
		EntryName:          module.manifest.EntryName,
		Adult:              *module.manifest.Adult,
		SkipVIPPrompt:      module.manifest.SkipVIPPrompt,
		Aliases:            module.manifest.Aliases,
		HostSuffixes:       module.manifest.HostSuffixes,
		BaseURL:            module.manifest.BaseURL,
		NoHostRotation:     module.manifest.NoHostRotation,
		KeepDetailEpisodes: module.manifest.KeepDetailEpisodes,
		ContinuousFeed:     module.manifest.ContinuousFeed,
		BrowserFingerprint: module.manifest.BrowserFingerprint,
	}
}

func (module *jsModule) allowsHost(host string) bool {
	host = strings.ToLower(host)
	for _, pattern := range module.manifest.Hosts {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if suffix, wildcard := strings.CutPrefix(pattern, "*"); wildcard && strings.HasPrefix(suffix, ".") && strings.HasSuffix(host, suffix) {
			return true
		}
		if pattern != "" && pattern == host {
			return true
		}
	}
	return false
}

func (d *Downloader) loadModules() {
	loadedModules.Lock()
	defer loadedModules.Unlock()
	for id := range loadedModules.ids {
		Unregister(id)
	}
	clear(loadedModules.ids)
	d.installBundledModulesLocked()
	entries, err := os.ReadDir(d.moduleRoot())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			d.recordModuleFailure("", err)
		}
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
			d.registerModuleLocked(entry.Name())
		}
	}
}

func (d *Downloader) moduleRoot() string { return filepath.Join(d.cfg.dataDir, moduleDirectory) }

func (d *Downloader) reloadModuleLocked(id string) {
	if loadedModules.ids[id] {
		Unregister(id)
		delete(loadedModules.ids, id)
	}
	if _, err := os.Stat(filepath.Join(d.moduleRoot(), id)); err == nil {
		d.registerModuleLocked(id)
	}
}

func (d *Downloader) registerModuleLocked(name string) {
	module, err := readModule(filepath.Join(d.moduleRoot(), name))
	if err == nil && module.manifest.ID != name {
		err = fmt.Errorf("模块文件夹名应与标识 %q 一致", module.manifest.ID)
	}
	if err == nil {
		err = moduleConflict(module.manifest)
	}
	if err != nil {
		d.recordModuleFailure(name, err)
		return
	}
	Register(module.descriptor(), module.provider)
	loadedModules.ids[module.manifest.ID] = true
}

func moduleConflict(manifest ModuleManifest) error {
	for _, name := range append([]string{manifest.ID}, manifest.Aliases...) {
		if owner, taken := registeredSource(name); taken && owner != manifest.ID {
			return fmt.Errorf("模块标识或别名 %q 与已有站源 %q 重复", name, owner)
		}
	}
	return nil
}

func (d *Downloader) recordModuleFailure(module string, err error) {
	d.recordDiagnostic(diagnosticEvent{Time: time.Now(), Level: "error", Event: "module_load", Source: module, Message: err.Error()})
}
