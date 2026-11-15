package core

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const bundledRemovedFile = ".bundled-removed.json"

//go:embed all:bundled
var embeddedModules embed.FS

var bundledModules = func() fs.FS {
	modules, err := fs.Sub(embeddedModules, "bundled")
	if err != nil {
		panic(err)
	}
	return modules
}()

type bundledModule struct {
	manifest ModuleManifest
	raw      []byte
	code     []byte
}

func readBundledModules() (map[string]bundledModule, []error) {
	entries, err := fs.ReadDir(bundledModules, ".")
	if err != nil {
		return nil, []error{err}
	}
	modules := map[string]bundledModule{}
	var failures []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		raw, err := fs.ReadFile(bundledModules, path.Join(id, moduleManifest))
		if err != nil {
			failures = append(failures, err)
			continue
		}
		code, err := fs.ReadFile(bundledModules, path.Join(id, moduleEntry))
		if err != nil {
			failures = append(failures, err)
			continue
		}
		var manifest ModuleManifest
		if err := json.Unmarshal(raw, &manifest); err != nil || manifest.ID != id || !moduleIDPattern.MatchString(id) {
			failures = append(failures, errors.New("内置模块 "+id+" 的清单无效"))
			continue
		}
		modules[id] = bundledModule{manifest: manifest, raw: raw, code: code}
	}
	return modules, failures
}

func isBundledModule(id string) bool {
	info, err := fs.Stat(bundledModules, id)
	return err == nil && info.IsDir()
}

func (d *Downloader) installBundledModulesLocked() {
	modules, failures := readBundledModules()
	for _, err := range failures {
		d.recordModuleFailure("", err)
	}
	removed := d.removedBundledModules()
	for id, module := range modules {
		if removed[id] {
			continue
		}
		if installed, err := readModule(filepath.Join(d.moduleRoot(), id)); err == nil &&
			!newerModuleVersion(module.manifest.Version, installed.manifest.Version) {
			continue
		}
		if err := d.writeModuleLocked(id, module.raw, module.code); err != nil {
			d.recordModuleFailure(id, err)
		}
	}
}

func (d *Downloader) removedBundledModules() map[string]bool {
	removed := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(d.moduleRoot(), bundledRemovedFile))
	if err != nil {
		return removed
	}
	var ids []string
	if json.Unmarshal(raw, &ids) == nil {
		for _, id := range ids {
			removed[id] = true
		}
	}
	return removed
}

func (d *Downloader) markBundledRemoved(id string, removed bool) error {
	set := d.removedBundledModules()
	if !isBundledModule(id) || set[id] == removed {
		return nil
	}
	if removed {
		set[id] = true
	} else {
		delete(set, id)
	}
	ids := make([]string, 0, len(set))
	for name := range set {
		ids = append(ids, name)
	}
	sort.Strings(ids)
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.moduleRoot(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.moduleRoot(), bundledRemovedFile), raw, 0o600)
}

func newerModuleVersion(candidate, current string) bool {
	parse := func(version string) []int {
		parts := strings.Split(version, ".")
		if len(parts) != 3 {
			return nil
		}
		numbers := make([]int, 3)
		for index, part := range parts {
			number, err := strconv.Atoi(part)
			if err != nil || number < 0 {
				return nil
			}
			numbers[index] = number
		}
		return numbers
	}
	next, installed := parse(candidate), parse(current)
	return next != nil && installed != nil && slices.Compare(next, installed) > 0
}

func bundledIndexEntries() []moduleIndexEntry {
	modules, _ := readBundledModules()
	entries := make([]moduleIndexEntry, 0, len(modules))
	for _, module := range modules {
		entries = append(entries, moduleIndexEntry{ID: module.manifest.ID, Name: module.manifest.Name, Version: module.manifest.Version,
			Description: module.manifest.Description, API: module.manifest.API, Adult: *module.manifest.Adult, Bundled: true})
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].ID < entries[b].ID })
	return entries
}
