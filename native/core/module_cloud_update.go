package core

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	managedModuleFormat    = 1
	managedModuleMetaBytes = 64 << 10
	managedModuleTimeout   = 15 * time.Second
	managedModuleClient    = "guoapp"
)

var (
	nativeModuleUpdateServer    = ""
	nativeModuleUpdatePublicKey = ""
	managedModulePathPattern    = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

type managedModuleManifest struct {
	Format int                             `json:"format"`
	APIs   map[string]managedModuleRelease `json:"apis"`
}

type managedModuleRelease struct {
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
	Sig     string `json:"sig"`
	Path    string `json:"path"`
}

type managedModuleUpdateResult struct {
	Updated bool               `json:"updated"`
	ID      string             `json:"id,omitempty"`
	Version string             `json:"version,omitempty"`
	Sources []nativeSourceInfo `json:"sources,omitempty"`
}

func nativeModuleUpdateServers() []string {
	raw := strings.TrimSpace(nativeModuleUpdateServer)
	if raw == "" {
		raw = nativeUpdateServer
	}
	servers := []string{}
	for _, server := range strings.Split(raw, ",") {
		if server = strings.TrimRight(strings.TrimSpace(server), "/"); server != "" {
			servers = append(servers, server)
		}
	}
	return servers
}

func managedModulePublicKey() (ed25519.PublicKey, error) {
	encoded := strings.TrimSpace(nativeModuleUpdatePublicKey)
	if encoded == "" {
		return nil, errors.New("未配置模块更新签名公钥")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("模块更新签名公钥无效")
	}
	return ed25519.PublicKey(decoded), nil
}

func parseManagedModuleRelease(raw []byte) (managedModuleRelease, bool, error) {
	var manifest managedModuleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return managedModuleRelease{}, false, errors.New("云更新清单格式无效")
	}
	if manifest.Format != managedModuleFormat {
		return managedModuleRelease{}, false, fmt.Errorf("云更新清单为第 %d 版，当前应用不支持", manifest.Format)
	}
	release, ok := manifest.APIs[strconv.Itoa(moduleAPIVersion)]
	if !ok {
		return managedModuleRelease{}, false, nil
	}
	if strings.TrimSpace(release.Version) == "" ||
		!moduleIndexHash.MatchString(strings.ToLower(strings.TrimSpace(release.SHA256))) ||
		!managedModulePathPattern.MatchString(release.Path) ||
		strings.Contains(release.Path, "..") ||
		strings.TrimSpace(release.Sig) == "" {
		return managedModuleRelease{}, false, errors.New("云更新条目字段无效")
	}
	release.SHA256 = strings.ToLower(strings.TrimSpace(release.SHA256))
	return release, true, nil
}

func verifyManagedModulePackage(raw []byte, release managedModuleRelease, publicKey ed25519.PublicKey) error {
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != release.SHA256 {
		return errors.New("云更新包校验和不符")
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(release.Sig))
	if err != nil {
		signature, err = base64.RawStdEncoding.DecodeString(strings.TrimSpace(release.Sig))
	}
	if err != nil || len(signature) != ed25519.SignatureSize {
		return errors.New("云更新包签名格式无效")
	}
	if !ed25519.Verify(publicKey, raw, signature) {
		return errors.New("云更新包签名校验失败")
	}
	return nil
}

func (engine *nativeEngine) fetchManagedModuleResource(ctx context.Context, address string, limit int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, managedModuleTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("X-App-Client", managedModuleClient)
	response, err := engine.downloader.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", response.StatusCode)
	}
	return readLimitedBody(response.Body, limit)
}

func (engine *nativeEngine) updateBundledModules(ctx context.Context) (managedModuleUpdateResult, error) {
	servers := nativeModuleUpdateServers()
	if len(servers) == 0 {
		return managedModuleUpdateResult{}, nil
	}
	publicKey, err := managedModulePublicKey()
	if err != nil {
		engine.recordManagedModuleUpdate("warn", "", err.Error())
		return managedModuleUpdateResult{}, nil
	}
	bundled, _ := readBundledModules()
	if len(bundled) == 0 {
		return managedModuleUpdateResult{}, nil
	}
	d := engine.downloader
	removed := d.removedBundledModules()
	var updated managedModuleUpdateResult
	for id := range bundled {
		if removed[id] {
			continue
		}
		installed, err := readModule(filepath.Join(d.moduleRoot(), id))
		if err != nil {
			continue
		}
		version, changed := engine.updateManagedModule(ctx, servers, publicKey, id, installed.manifest.Version)
		if changed {
			updated = managedModuleUpdateResult{
				Updated: true,
				ID:      id,
				Version: version,
				Sources: engine.sourceList(),
			}
		}
	}
	return updated, nil
}

func (engine *nativeEngine) updateManagedModule(
	ctx context.Context,
	servers []string,
	publicKey ed25519.PublicKey,
	id, installedVersion string,
) (string, bool) {
	release, found, mirror, err := engine.fetchManagedModuleRelease(ctx, servers, id)
	if err != nil {
		engine.recordManagedModuleUpdate("warn", id, err.Error())
		return "", false
	}
	if !found || !newerModuleVersion(release.Version, installedVersion) {
		return "", false
	}
	raw, err := engine.fetchManagedModulePackage(ctx, servers, mirror, id, release, publicKey)
	if err != nil {
		engine.recordManagedModuleUpdate("error", id, err.Error())
		return "", false
	}
	manifest, code, err := modulePackageFiles(raw)
	if err != nil {
		engine.recordManagedModuleUpdate("error", id, err.Error())
		return "", false
	}
	module, err := parseModule(manifest, code)
	if err != nil {
		engine.recordManagedModuleUpdate("error", id, err.Error())
		return "", false
	}
	if module.manifest.ID != id {
		engine.recordManagedModuleUpdate("error", id, fmt.Sprintf("云更新包标识不符：%s", module.manifest.ID))
		return "", false
	}
	if module.manifest.Version != release.Version {
		engine.recordManagedModuleUpdate("error", id, fmt.Sprintf("云更新包版本与清单不一致：%s != %s", module.manifest.Version, release.Version))
		return "", false
	}
	d := engine.downloader
	loadedModules.Lock()
	defer loadedModules.Unlock()
	if d.removedBundledModules()[id] {
		return "", false
	}
	current, err := readModule(filepath.Join(d.moduleRoot(), id))
	if err != nil || !newerModuleVersion(module.manifest.Version, current.manifest.Version) {
		return "", false
	}
	if err := moduleConflict(module.manifest); err != nil {
		engine.recordManagedModuleUpdate("error", id, err.Error())
		return "", false
	}
	if err := d.writeModuleLocked(id, manifest, code); err != nil {
		engine.recordManagedModuleUpdate("error", id, err.Error())
		return "", false
	}
	d.replaceModuleLocked(module)
	engine.recordManagedModuleUpdate("info", id, fmt.Sprintf("云更新成功：%s -> %s", current.manifest.Version, module.manifest.Version))
	return module.manifest.Version, true
}

func (engine *nativeEngine) fetchManagedModuleRelease(
	ctx context.Context,
	servers []string,
	id string,
) (managedModuleRelease, bool, int, error) {
	var failures []string
	for index, server := range servers {
		address := fmt.Sprintf("%s/v1/modules/%s/latest.json", server, id)
		raw, err := engine.fetchManagedModuleResource(ctx, address, managedModuleMetaBytes)
		if err == nil {
			release, found, parseErr := parseManagedModuleRelease(raw)
			if parseErr == nil {
				return release, found, index, nil
			}
			err = parseErr
		}
		failures = append(failures, err.Error())
		if ctx.Err() != nil {
			break
		}
	}
	return managedModuleRelease{}, false, 0, fmt.Errorf("检查 %s 云更新失败：%s", id, strings.Join(failures, "；"))
}

func (engine *nativeEngine) fetchManagedModulePackage(
	ctx context.Context,
	servers []string,
	mirror int,
	id string,
	release managedModuleRelease,
	publicKey ed25519.PublicKey,
) ([]byte, error) {
	var failures []string
	for offset := range servers {
		server := servers[(mirror+offset)%len(servers)]
		address := fmt.Sprintf("%s/v1/modules/%s/%s", server, id, release.Path)
		raw, err := engine.fetchManagedModuleResource(ctx, address, modulePackageBytes)
		if err == nil {
			if err = verifyManagedModulePackage(raw, release, publicKey); err == nil {
				return raw, nil
			}
		}
		failures = append(failures, err.Error())
		if ctx.Err() != nil {
			break
		}
	}
	return nil, fmt.Errorf("下载 %s 云更新失败：%s", id, strings.Join(failures, "；"))
}

func (engine *nativeEngine) recordManagedModuleUpdate(level, id, message string) {
	if engine.downloader == nil {
		return
	}
	engine.downloader.recordDiagnostic(diagnosticEvent{
		Time:    time.Now(),
		Level:   level,
		Event:   "module_cloud_update",
		Source:  id,
		Message: message,
	})
}
