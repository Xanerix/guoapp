package core

import (
	"errors"
	"net/url"

	"regexp"

	"strings"
)

var (
	spaceChars  = regexp.MustCompile(`\s+`)
	rePublicURL = regexp.MustCompile(`(?i)(?:https?|socks5h?)://[^\s"'<>]+`)
)

func PublicError(err error) error {
	if err == nil {
		return nil
	}
	return redactedError{cause: err}
}

type redactedError struct{ cause error }

func (err redactedError) Error() string { return redactErrorString(err.cause.Error()) }
func (err redactedError) Unwrap() error { return err.cause }

func redactErrorString(s string) string {
	return rePublicURL.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[无效 URL 已隐藏]"
		}
		if u.Host == "" {
			return raw
		}
		if u.User != nil {
			u.User = url.User("redacted")
		}
		if u.RawQuery != "" {
			u.RawQuery = "[redacted]"
		}
		if u.Fragment != "" {
			u.Fragment = "redacted"
		}
		return u.String()
	})
}

func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func Pkcs7Unpad(src []byte, blockSize int) ([]byte, error) {
	if len(src) == 0 || len(src)%blockSize != 0 {
		return nil, errors.New("invalid pkcs7 length")
	}
	pad := int(src[len(src)-1])
	if pad == 0 || pad > blockSize || pad > len(src) {
		return nil, errors.New("invalid pkcs7 padding")
	}
	for _, v := range src[len(src)-pad:] {
		if int(v) != pad {
			return nil, errors.New("invalid pkcs7 padding bytes")
		}
	}
	return src[:len(src)-pad], nil
}
