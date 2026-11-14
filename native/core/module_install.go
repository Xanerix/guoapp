package core

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	modulePackageBytes  = 8 << 20
	moduleManifestBytes = 64 << 10
	modulePackageFolder = "module-packages"
)

var modulePackageToken = regexp.MustCompile(`^[0-9a-f]{32}$`)

type nativeModuleInfo struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description"`
	Hosts        []string `json:"hosts"`
	Capabilities []string `json:"capabilities"`
	Installed    bool     `json:"installed"`
	Previous     string   `json:"previous,omitempty"`
	Token        string   `json:"token,omitempty"`
	Error        string   `json:"error,omitempty"`
}

func moduleInfo(manifest ModuleManifest) nativeModuleInfo {
	return nativeModuleInfo{ID: manifest.ID, Name: manifest.Name, Version: manifest.Version, Description: manifest.Description,
		Hosts: manifest.Hosts, Capabilities: manifest.Capabilities}
}

func (engine *nativeEngine) modules() ([]nativeModuleInfo, error) {
	entries, err := os.ReadDir(engine.downloader.moduleRoot())
	if errors.Is(err, os.ErrNotExist) {
		return []nativeModuleInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	items := []nativeModuleInfo{}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		module, err := readModule(filepath.Join(engine.downloader.moduleRoot(), entry.Name()))
		if err != nil {
			items = append(items, nativeModuleInfo{ID: entry.Name(), Name: entry.Name(), Error: err.Error()})
			continue
		}
		info := moduleInfo(module.manifest)
		if !registeredProviderSource(module.manifest.ID) {
			info.Error = "模块未能加载，详见诊断日志"
		}
		items = append(items, info)
	}
	return items, nil
}

func (engine *nativeEngine) prepareModule(ctx context.Context, file, address string) (nativeModuleInfo, error) {
	var raw []byte
	var err error
	if address != "" {
		raw, err = engine.downloadModulePackage(ctx, address)
	} else {
		raw, err = readModulePackage(file)
	}
	if err != nil {
		return nativeModuleInfo{}, err
	}
	module, err := unpackModule(raw)
	if err != nil {
		return nativeModuleInfo{}, err
	}
	if err := moduleConflict(module.manifest); err != nil {
		return nativeModuleInfo{}, err
	}
	folder := filepath.Join(engine.cacheDirectory, modulePackageFolder)
	if err := os.RemoveAll(folder); err != nil {
		return nativeModuleInfo{}, err
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return nativeModuleInfo{}, err
	}
	token := make([]byte, 16)
	_, _ = rand.Read(token)
	info := moduleInfo(module.manifest)
	info.Token = hex.EncodeToString(token)
	if err := os.WriteFile(filepath.Join(folder, info.Token+".zip"), raw, 0o600); err != nil {
		return nativeModuleInfo{}, err
	}
	if installed, err := readModule(filepath.Join(engine.downloader.moduleRoot(), module.manifest.ID)); err == nil {
		info.Installed, info.Previous = true, installed.manifest.Version
	}
	return info, nil
}

func (engine *nativeEngine) installModule(token string) (map[string]any, error) {
	if !modulePackageToken.MatchString(token) {
		return nil, errors.New("安装包已失效，请重新选择")
	}
	staged := filepath.Join(engine.cacheDirectory, modulePackageFolder, token+".zip")
	raw, err := os.ReadFile(staged)
	if err != nil {
		return nil, errors.New("安装包已失效，请重新选择")
	}
	manifest, code, err := modulePackageFiles(raw)
	if err != nil {
		return nil, err
	}
	module, err := parseModule(manifest, code)
	if err != nil {
		return nil, err
	}
	d := engine.downloader
	loadedModules.Lock()
	defer loadedModules.Unlock()
	if err := moduleConflict(module.manifest); err != nil {
		return nil, err
	}
	id := module.manifest.ID
	root := d.moduleRoot()
	temporary := filepath.Join(root, "."+id+".installing")
	if err := os.RemoveAll(temporary); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(temporary, 0o700); err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	if err := os.WriteFile(filepath.Join(temporary, moduleManifest), manifest, 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(temporary, moduleEntry), code, 0o600); err != nil {
		return nil, err
	}
	target := filepath.Join(root, id)
	if err := os.RemoveAll(target); err != nil {
		return nil, err
	}
	if err := os.Rename(temporary, target); err != nil {
		return nil, err
	}
	_ = os.Remove(staged)
	d.reloadModuleLocked(id)
	if !registeredProviderSource(id) {
		return nil, errors.New("模块已写入但未能加载，详见诊断日志")
	}
	return map[string]any{"source": id, "sources": engine.sourceList()}, nil
}

func (engine *nativeEngine) uninstallModule(name string) (map[string]any, error) {
	if name == "" || !filepath.IsLocal(name) || filepath.Base(name) != name || strings.HasPrefix(name, ".") {
		return nil, errors.New("模块标识无效")
	}
	d := engine.downloader
	loadedModules.Lock()
	defer loadedModules.Unlock()
	if err := os.RemoveAll(filepath.Join(d.moduleRoot(), name)); err != nil {
		return nil, err
	}
	d.reloadModuleLocked(name)
	return map[string]any{"sources": engine.sourceList()}, nil
}

func (engine *nativeEngine) downloadModulePackage(ctx context.Context, address string) ([]byte, error) {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return nil, errors.New("请填写以 http:// 或 https:// 开头的安装包地址")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	response, err := engine.downloader.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("下载安装包失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载安装包失败：HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, modulePackageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("下载安装包失败：%w", err)
	}
	if len(raw) > modulePackageBytes {
		return nil, fmt.Errorf("安装包超过 %d MB", modulePackageBytes>>20)
	}
	return raw, nil
}

func readModulePackage(file string) ([]byte, error) {
	handle, err := os.Open(file)
	if err != nil {
		return nil, errors.New("无法读取所选文件")
	}
	defer handle.Close()
	raw, err := io.ReadAll(io.LimitReader(handle, modulePackageBytes+1))
	if err != nil {
		return nil, errors.New("无法读取所选文件")
	}
	if len(raw) > modulePackageBytes {
		return nil, fmt.Errorf("安装包超过 %d MB", modulePackageBytes>>20)
	}
	return raw, nil
}

func unpackModule(raw []byte) (*jsModule, error) {
	manifest, code, err := modulePackageFiles(raw)
	if err != nil {
		return nil, err
	}
	return parseModule(manifest, code)
}

func modulePackageFiles(raw []byte) (manifest, code []byte, err error) {
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, nil, errors.New("安装包不是有效的 zip 文件")
	}
	files := map[string]*zip.File{}
	var prefixes []string
	for _, file := range archive.File {
		name := strings.TrimPrefix(strings.ReplaceAll(file.Name, `\`, "/"), "./")
		files[name] = file
		if name == moduleManifest {
			prefixes = append(prefixes, "")
		} else if folder, base := path.Split(name); base == moduleManifest && strings.Count(folder, "/") == 1 {
			prefixes = append(prefixes, folder)
		}
	}
	if len(prefixes) != 1 {
		return nil, nil, fmt.Errorf("安装包根目录或唯一的文件夹里应有 %s 和 %s", moduleManifest, moduleEntry)
	}
	if manifest, err = readPackageFile(files[prefixes[0]+moduleManifest], moduleManifestBytes); err != nil {
		return nil, nil, err
	}
	if code, err = readPackageFile(files[prefixes[0]+moduleEntry], moduleEntryBytes); err != nil {
		return nil, nil, err
	}
	return manifest, code, nil
}

func readPackageFile(file *zip.File, limit int) ([]byte, error) {
	if file == nil {
		return nil, fmt.Errorf("安装包里缺少 %s", moduleEntry)
	}
	if file.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("%s 超过 %d KB", path.Base(file.Name), limit>>10)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("无法解压 %s", path.Base(file.Name))
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return nil, fmt.Errorf("无法解压 %s", path.Base(file.Name))
	}
	if len(data) > limit {
		return nil, fmt.Errorf("%s 超过 %d KB", path.Base(file.Name), limit>>10)
	}
	return data, nil
}
