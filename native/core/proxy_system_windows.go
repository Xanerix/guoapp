package core

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

func platformSystemProxy() nativeSystemProxy {
	var result nativeSystemProxy
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return result
	}
	defer key.Close()
	pac, _, _ := key.GetStringValue("AutoConfigURL")
	result.PAC, result.PACURL = pac != "", pac
	enabled, _, _ := key.GetIntegerValue("ProxyEnable")
	if enabled == 0 {
		return result
	}
	server, _, _ := key.GetStringValue("ProxyServer")
	bypass, _, _ := key.GetStringValue("ProxyOverride")
	result.Bypass = strings.Split(bypass, ";")
	result.HTTP, result.HTTPS = windowsProxyAddresses(server)
	return result
}

func windowsProxyAddresses(server string) (httpAddress, httpsAddress string) {
	parts := strings.FieldsFunc(server, func(r rune) bool { return r == ';' || unicode.IsSpace(r) })
	for _, part := range parts {
		protocol, address, separate := strings.Cut(part, "=")
		if !separate {
			address, protocol = protocol, ""
		}
		if address == "" {
			continue
		}
		if !strings.Contains(address, "://") {
			prefix := "http://"
			if protocol == "socks" {
				prefix = "socks5://"
			}
			address = prefix + address
		}
		switch protocol {
		case "http":
			httpAddress = address
		case "https":
			httpsAddress = address
		case "", "socks":
			if httpAddress == "" {
				httpAddress = address
			}
			if httpsAddress == "" {
				httpsAddress = address
			}
		}
	}
	return httpAddress, httpsAddress
}

const (
	winhttpAccessTypeNoProxy    = 1
	winhttpAccessTypeNamedProxy = 3
	winhttpAutoproxyConfigURL   = 2
)

type winhttpAutoproxyOptions struct {
	flags                 uint32
	autoDetectFlags       uint32
	autoConfigURL         *uint16
	reserved              uintptr
	reservedFlags         uint32
	autoLogonIfChallenged int32
}

type winhttpProxyInfo struct {
	accessType  uint32
	proxy       *uint16
	proxyBypass *uint16
}

type pacResult struct {
	proxy   *url.URL
	err     error
	expires time.Time
}

var (
	winhttpDLL                = windows.NewLazySystemDLL("winhttp.dll")
	procWinHttpOpen           = winhttpDLL.NewProc("WinHttpOpen")
	procWinHttpSetTimeouts    = winhttpDLL.NewProc("WinHttpSetTimeouts")
	procWinHttpGetProxyForUrl = winhttpDLL.NewProc("WinHttpGetProxyForUrl")
	procGlobalFree            = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")

	pacCache = struct {
		sync.Mutex
		results map[string]pacResult
	}{results: map[string]pacResult{}}

	pacSession = sync.OnceValues(func() (uintptr, error) {
		agent, err := windows.UTF16PtrFromString("duanju")
		if err != nil {
			return 0, err
		}
		handle, _, callErr := procWinHttpOpen.Call(uintptr(unsafe.Pointer(agent)), winhttpAccessTypeNoProxy, 0, 0, 0)
		if handle == 0 {
			return 0, callErr
		}
		procWinHttpSetTimeouts.Call(handle, 5000, 5000, 5000, 5000)
		return handle, nil
	})
)

func platformPACProxy(script string, target *url.URL) (*url.URL, error) {
	key := script + "|" + target.Scheme + "://" + strings.ToLower(target.Host)
	pacCache.Lock()
	cached, ok := pacCache.results[key]
	pacCache.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.proxy, cached.err
	}
	proxy, err := winhttpPACProxy(script, target)
	pacCache.Lock()
	if len(pacCache.results) >= 256 {
		clear(pacCache.results)
	}
	pacCache.results[key] = pacResult{proxy: proxy, err: err, expires: time.Now().Add(30 * time.Second)}
	pacCache.Unlock()
	return proxy, err
}

func winhttpPACProxy(script string, target *url.URL) (*url.URL, error) {
	session, err := pacSession()
	if err != nil {
		return nil, err
	}
	scriptPointer, err := windows.UTF16PtrFromString(script)
	if err != nil {
		return nil, err
	}
	targetPointer, err := windows.UTF16PtrFromString(target.String())
	if err != nil {
		return nil, err
	}
	options := winhttpAutoproxyOptions{flags: winhttpAutoproxyConfigURL, autoConfigURL: scriptPointer, autoLogonIfChallenged: 1}
	var info winhttpProxyInfo
	ok, _, callErr := procWinHttpGetProxyForUrl.Call(session, uintptr(unsafe.Pointer(targetPointer)),
		uintptr(unsafe.Pointer(&options)), uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return nil, callErr
	}
	defer func() {
		for _, pointer := range []*uint16{info.proxy, info.proxyBypass} {
			if pointer != nil {
				procGlobalFree.Call(uintptr(unsafe.Pointer(pointer)))
			}
		}
	}()
	if info.accessType != winhttpAccessTypeNamedProxy || info.proxy == nil {
		return nil, nil
	}
	httpAddress, httpsAddress := windowsProxyAddresses(windows.UTF16PtrToString(info.proxy))
	address := httpAddress
	if target.Scheme == "https" {
		address = httpsAddress
	}
	if address == "" {
		return nil, errors.New("自动代理脚本没有返回可用代理")
	}
	return url.Parse(address)
}
