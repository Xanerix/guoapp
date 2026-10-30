package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
)

var nativeUpdateServer = ""

const nativeReleasePrefix = "https://github.com/Xanerix/guoapp/releases/"

var nativeAppVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+\+(\d+)$`)
var nativeInstallIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

var nativeUpdateABIs = map[string]string{"arm64": "arm64-v8a", "arm": "armeabi-v7a", "amd64": "x86_64"}

type nativeUpdate struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	URL       string `json:"url,omitempty"`
}

type nativeUpdateJob struct {
	cancel   context.CancelFunc
	received atomic.Int64
	total    atomic.Int64
	path     string
	err      error
	done     bool
}

type nativeProgressReader struct {
	reader   io.Reader
	received *atomic.Int64
}

func (progress nativeProgressReader) Read(buffer []byte) (int, error) {
	count, err := progress.reader.Read(buffer)
	progress.received.Add(int64(count))
	return count, err
}

func nativeUpdateServers() []string {
	servers := []string{}
	for _, server := range strings.Split(nativeUpdateServer, ",") {
		if server = strings.TrimRight(strings.TrimSpace(server), "/"); server != "" {
			servers = append(servers, server)
		}
	}
	return servers
}

func nativeAppBuildSlug() string {
	if buildAllSources == "true" {
		return "zhenguojian"
	}
	return "hongguojian"
}

func nativeAppBuildNumber(version string) (int, bool) {
	match := nativeAppVersionPattern.FindStringSubmatch(version)
	if match == nil {
		return 0, false
	}
	number, err := strconv.Atoi(match[1])
	return number, err == nil
}

func (engine *nativeEngine) installID() (string, error) {
	path := filepath.Join(engine.directory, "install-id")
	if data, err := os.ReadFile(path); err == nil && nativeInstallIDPattern.Match(data) {
		return string(data), nil
	}
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	id := hex.EncodeToString(buffer)
	return id, os.WriteFile(path, []byte(id), 0600)
}

func (engine *nativeEngine) reportUsage(ctx context.Context, version string) error {
	if _, valid := nativeAppBuildNumber(version); !valid {
		return errors.New("应用版本号无效")
	}
	servers := nativeUpdateServers()
	if len(servers) == 0 {
		return nil
	}
	id, err := engine.installID()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]string{"id": id, "version": version, "platform": runtime.GOOS, "build": nativeAppBuildSlug()})
	if err != nil {
		return err
	}
	for _, server := range servers {
		if err = engine.postUsage(ctx, server, payload); err == nil || ctx.Err() != nil {
			return err
		}
	}
	return err
}

func (engine *nativeEngine) postUsage(ctx context.Context, server string, payload []byte) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/v1/ping", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := engine.downloader.client.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode/100 != 2 {
		return fmt.Errorf("使用统计失败：HTTP %d", response.StatusCode)
	}
	return nil
}

func (engine *nativeEngine) checkUpdate(ctx context.Context, version string) (nativeUpdate, error) {
	current, valid := nativeAppBuildNumber(version)
	if !valid {
		return nativeUpdate{}, errors.New("应用版本号无效")
	}
	servers := nativeUpdateServers()
	if len(servers) == 0 {
		return nativeUpdate{}, nil
	}
	var latest nativeUpdate
	var err error
	for _, server := range servers {
		if latest, err = engine.fetchLatest(ctx, server); err == nil || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		return nativeUpdate{}, err
	}
	number, valid := nativeAppBuildNumber(latest.Version)
	if !valid || number <= current || !strings.HasPrefix(latest.URL, nativeReleasePrefix) {
		return nativeUpdate{}, nil
	}
	return nativeUpdate{Available: true, Version: latest.Version, URL: latest.URL}, nil
}

func (engine *nativeEngine) fetchLatest(ctx context.Context, server string) (nativeUpdate, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/v1/latest", nil)
	if err != nil {
		return nativeUpdate{}, err
	}
	response, err := engine.downloader.client.Do(request)
	if err != nil {
		return nativeUpdate{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nativeUpdate{}, fmt.Errorf("检查更新失败：HTTP %d", response.StatusCode)
	}
	var latest nativeUpdate
	return latest, json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&latest)
}

func (engine *nativeEngine) updateDownload(input nativeInput) (any, error) {
	engine.updateMu.Lock()
	defer engine.updateMu.Unlock()
	job := engine.updateJob
	switch input.Command {
	case "start":
		if _, valid := nativeAppBuildNumber(input.Version); !valid {
			return nil, errors.New("应用版本号无效")
		}
		abi, found := nativeUpdateABIs[runtime.GOARCH]
		if !found {
			return nil, errors.New("当前设备架构没有对应的安装包")
		}
		if !filepath.IsAbs(input.Directory) {
			return nil, errors.New("更新目录无效")
		}
		if job != nil {
			job.cancel()
		}
		ctx, cancel := context.WithCancel(context.Background())
		job = &nativeUpdateJob{cancel: cancel}
		engine.updateJob = job
		name := nativeAppBuildSlug() + "-" + input.Version + "-" + abi + ".apk"
		go engine.runUpdateDownload(ctx, job, input.Directory, name)
		return true, nil
	case "cancel":
		if job != nil {
			job.cancel()
			engine.updateJob = nil
		}
		return true, nil
	case "status":
		if job == nil {
			return nil, errors.New("没有正在进行的更新下载")
		}
		status := map[string]any{"received": job.received.Load(), "total": job.total.Load(), "done": job.done, "path": job.path}
		if job.err != nil {
			status["error"] = publicError(job.err).Error()
		}
		return status, nil
	default:
		return nil, errors.New("不支持的更新操作")
	}
}

func (engine *nativeEngine) runUpdateDownload(ctx context.Context, job *nativeUpdateJob, directory, name string) {
	path, err := engine.fetchUpdatePackage(ctx, job, directory, name)
	engine.updateMu.Lock()
	job.path, job.err, job.done = path, err, true
	engine.updateMu.Unlock()
}

func (engine *nativeEngine) fetchUpdatePackage(ctx context.Context, job *nativeUpdateJob, directory, name string) (string, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return "", err
	}
	err := errors.New("没有可用的更新服务器")
	for _, server := range nativeUpdateServers() {
		var path string
		if path, err = engine.fetchUpdatePackageFrom(ctx, job, server+"/apk/"+name, directory, name); err == nil || ctx.Err() != nil {
			return path, err
		}
	}
	return "", err
}

func (engine *nativeEngine) fetchUpdatePackageFrom(ctx context.Context, job *nativeUpdateJob, address, directory, name string) (string, error) {
	job.received.Store(0)
	job.total.Store(0)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", err
	}
	client := *engine.downloader.client
	client.Timeout = 0
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载更新失败：HTTP %d", response.StatusCode)
	}
	job.total.Store(response.ContentLength)
	file, err := os.CreateTemp(directory, "update-*.part")
	if err != nil {
		return "", err
	}
	_, err = io.Copy(file, nativeProgressReader{response.Body, &job.received})
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil && response.ContentLength > 0 && job.received.Load() != response.ContentLength {
		err = io.ErrUnexpectedEOF
	}
	path := filepath.Join(directory, name)
	if err == nil {
		err = os.Rename(file.Name(), path)
	}
	if err != nil {
		os.Remove(file.Name())
		return "", err
	}
	return path, nil
}
