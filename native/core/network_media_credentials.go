package core

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type MediaCredentials struct {
	Cookie    string
	Origin    string
	Referer   string
	UserAgent string
	Expires   time.Time
}

type MediaCredentialsKey struct{}

func providerMediaContext(ctx context.Context, credentials *MediaCredentials) context.Context {
	return context.WithValue(ctx, MediaCredentialsKey{}, credentials)
}

func ProviderMediaOrigin(address *url.URL) string {
	if address == nil {
		return ""
	}
	scheme := strings.ToLower(address.Scheme)
	host := strings.ToLower(address.Host)
	if scheme == "https" && address.Port() == "443" || scheme == "http" && address.Port() == "80" {
		host = strings.TrimSuffix(host, ":"+address.Port())
	}
	return scheme + "://" + host
}

func (credentials *MediaCredentials) apply(request *http.Request) error {
	request.Header.Del("Cookie")
	if credentials.UserAgent != "" {
		request.Header.Set("User-Agent", credentials.UserAgent)
	}
	if credentials.Referer != "" {
		request.Header.Set("Referer", credentials.Referer)
		if address, err := url.Parse(credentials.Referer); err == nil {
			request.Header.Set("Origin", ProviderMediaOrigin(address))
		}
	}
	if ProviderMediaOrigin(request.URL) == credentials.Origin {
		if !credentials.Expires.IsZero() && !time.Now().Before(credentials.Expires) {
			return errors.New("播放凭证已过期，请重新解析播放")
		}
		request.Header.Set("Cookie", credentials.Cookie)
	}
	return nil
}

func (credentials *MediaCredentials) client(client *http.Client) *http.Client {
	scoped := *client
	scoped.Jar = nil
	previousRedirect := client.CheckRedirect
	scoped.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("媒体重定向过多")
		}
		if previousRedirect != nil {
			if err := previousRedirect(request, via); err != nil {
				return err
			}
		}
		return credentials.apply(request)
	}
	return &scoped
}
