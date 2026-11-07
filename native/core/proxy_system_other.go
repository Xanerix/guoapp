//go:build !windows

package core

import (
	"errors"
	"net/url"
)

func platformSystemProxy() nativeSystemProxy { return nativeSystemProxy{} }

func platformPACProxy(string, *url.URL) (*url.URL, error) {
	return nil, errors.New("当前平台不支持自动代理脚本")
}
