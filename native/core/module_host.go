package core

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/net/html"
)

const (
	moduleCacheEntries    = 512
	moduleCacheValueBytes = 1 << 20
	moduleRandomBytes     = 1024
	moduleSleepLimit      = time.Minute
	moduleHTMLBytes       = 8 << 20
)

type moduleCacheEntry struct {
	value   string
	expires time.Time
}

type jsReserveRequest struct {
	URL        string `json:"url"`
	Lane       string `json:"lane"`
	Background bool   `json:"background"`
}

type jsCatalogContext struct {
	More       bool `json:"more"`
	Background bool `json:"background"`
}

type jsHTMLNode struct {
	Tag      string            `json:"tag,omitempty"`
	Attrs    map[string]string `json:"attrs,omitempty"`
	Text     string            `json:"text,omitempty"`
	Children []jsHTMLNode      `json:"children,omitempty"`
}

func (p *jsProvider) hostUtilities(instance *jsInstance) map[string]any {
	vm := instance.vm
	bytesOf := func(value goja.Value) []byte {
		if text, ok := value.Export().(string); ok {
			return []byte(text)
		}
		var data []byte
		if err := vm.ExportTo(value, &data); err != nil {
			panic(vm.NewTypeError("需要字符串或字节数组"))
		}
		return append([]byte(nil), data...)
	}
	bytesValue := func(data []byte) goja.Value {
		array, err := vm.New(vm.Get("Uint8Array"), vm.ToValue(vm.NewArrayBuffer(data)))
		if err != nil {
			panic(err)
		}
		return array
	}
	return map[string]any{
		"retries":  func() int { return p.host.Retries() },
		"maxPages": func() int { return p.host.MaxPages() },
		"reserve": func(request jsReserveRequest) uint64 {
			if err := p.allowedAddress(request.URL); err != nil {
				instance.fail(err, false)
			}
			ctx := instance.ctx
			if request.Background {
				ctx = context.WithValue(ctx, BackgroundCatalogKey{}, true)
			}
			ticket, err := p.host.Reserve(ctx, request.URL, request.Lane)
			if err != nil {
				instance.fail(err, false)
			}
			return ticket
		},
		"sleep": func(milliseconds int64) error {
			timer := time.NewTimer(min(max(time.Duration(milliseconds)*time.Millisecond, 0), moduleSleepLimit))
			defer timer.Stop()
			select {
			case <-timer.C:
				return nil
			case <-instance.ctx.Done():
				return instance.ctx.Err()
			}
		},
		"finishMedia": func(media jsMedia) (jsMedia, error) {
			finished, err := p.host.FinishMedia(instance.ctx, media.providerMedia(), p.module.manifest.ID)
			return moduleMedia(finished), err
		},
		"catalogContext": func() jsCatalogContext {
			more, _ := instance.ctx.Value(LibraryMoreKey{}).(bool)
			background, _ := instance.ctx.Value(BackgroundCatalogKey{}).(bool)
			return jsCatalogContext{More: more, Background: background}
		},
		"isKnown": func(id string) bool {
			known, _ := instance.ctx.Value(LibraryKnownKey{}).(map[string]bool)
			return known[id]
		},
		"reportProgress": func(items goja.Value) error {
			var dramas []Drama
			if err := instance.decode(items, &dramas); err != nil {
				return err
			}
			ReportLibraryProgress(instance.ctx, p.module.manifest.ID, dramas, nil, false)
			return nil
		},
		"cacheGet": func(key string) goja.Value {
			if value, found := p.cached(key); found {
				return vm.ToValue(value)
			}
			return goja.Null()
		},
		"cacheSet": func(key string, value goja.Value, ttl int64) error {
			if goja.IsNull(value) || goja.IsUndefined(value) {
				return p.storeCache(key, "", 0)
			}
			text := value.String()
			if len(text) > moduleCacheValueBytes {
				return errors.New("缓存内容过大")
			}
			return p.storeCache(key, text, time.Duration(ttl)*time.Millisecond)
		},
		"preserveIntegers": preserveIntegers,
		"parseHtml": func(text string) (goja.Value, error) {
			if len(text) > moduleHTMLBytes {
				return nil, errors.New("网页内容过大")
			}
			document, err := html.Parse(strings.NewReader(text))
			if err != nil {
				return nil, errors.New("网页无法解析")
			}
			return instance.jsonValue(moduleHTMLTree(document))
		},
		"utf8Encode": func(text string) goja.Value { return bytesValue([]byte(text)) },
		"utf8Decode": func(data goja.Value) string { return strings.ToValidUTF8(string(bytesOf(data)), "�") },
		"hexEncode":  func(data goja.Value) string { return hex.EncodeToString(bytesOf(data)) },
		"hexDecode": func(text string) (goja.Value, error) {
			data, err := hex.DecodeString(strings.TrimSpace(text))
			if err != nil {
				return nil, errors.New("十六进制内容无效")
			}
			return bytesValue(data), nil
		},
		"base64Encode": func(data goja.Value) string { return base64.StdEncoding.EncodeToString(bytesOf(data)) },
		"base64Decode": func(text string) (goja.Value, error) {
			text = strings.TrimSpace(text)
			data, err := base64.StdEncoding.Strict().DecodeString(text)
			if err != nil {
				data, err = base64.RawStdEncoding.Strict().DecodeString(text)
			}
			if err != nil {
				return nil, errors.New("Base64 内容无效")
			}
			return bytesValue(data), nil
		},
		"md5": func(data goja.Value) goja.Value {
			sum := md5.Sum(bytesOf(data))
			return bytesValue(sum[:])
		},
		"sha256": func(data goja.Value) goja.Value {
			sum := sha256.Sum256(bytesOf(data))
			return bytesValue(sum[:])
		},
		"randomBytes": func(size int) (goja.Value, error) {
			if size < 0 || size > moduleRandomBytes {
				return nil, errors.New("随机字节数无效")
			}
			data := make([]byte, size)
			if _, err := rand.Read(data); err != nil {
				return nil, err
			}
			return bytesValue(data), nil
		},
		"aesCbcEncrypt": func(key, iv, data goja.Value) (goja.Value, error) {
			block, err := aes.NewCipher(bytesOf(key))
			if err != nil {
				return nil, errors.New("AES 密钥长度无效")
			}
			vector, plain := bytesOf(iv), bytesOf(data)
			if len(vector) != aes.BlockSize || len(plain)%aes.BlockSize != 0 {
				return nil, errors.New("AES 向量或数据长度无效")
			}
			cipher.NewCBCEncrypter(block, vector).CryptBlocks(plain, plain)
			return bytesValue(plain), nil
		},
	}
}

func (instance *jsInstance) jsonValue(value any) (goja.Value, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	parse, _ := goja.AssertFunction(instance.vm.Get("JSON").ToObject(instance.vm).Get("parse"))
	return parse(goja.Undefined(), instance.vm.ToValue(string(encoded)))
}

func (instance *jsInstance) decode(value goja.Value, target any) error {
	stringify, _ := goja.AssertFunction(instance.vm.Get("JSON").ToObject(instance.vm).Get("stringify"))
	encoded, err := stringify(goja.Undefined(), value)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(encoded.String()), target)
}

func (p *jsProvider) cached(key string) (string, bool) {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	entry, found := p.cache[key]
	if found && !entry.expires.IsZero() && !time.Now().Before(entry.expires) {
		delete(p.cache, key)
		return "", false
	}
	return entry.value, found
}

func (p *jsProvider) storeCache(key, value string, ttl time.Duration) error {
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if value == "" {
		delete(p.cache, key)
		return nil
	}
	if _, found := p.cache[key]; !found && len(p.cache) >= moduleCacheEntries {
		now := time.Now()
		for name, entry := range p.cache {
			if !entry.expires.IsZero() && !now.Before(entry.expires) {
				delete(p.cache, name)
			}
		}
		for name, entry := range p.cache {
			if len(p.cache) < moduleCacheEntries {
				break
			}
			if !entry.expires.IsZero() {
				delete(p.cache, name)
			}
		}
		if len(p.cache) >= moduleCacheEntries {
			return errors.New("不过期的缓存项过多")
		}
	}
	entry := moduleCacheEntry{value: value}
	if ttl > 0 {
		entry.expires = time.Now().Add(ttl)
	}
	p.cache[key] = entry
	return nil
}

func preserveIntegers(text string) string {
	var out strings.Builder
	out.Grow(len(text) + 64)
	inString, escaped := false, false
	for index := 0; index < len(text); {
		char := text[index]
		switch {
		case inString:
			if escaped {
				escaped = false
			} else if char == '\\' {
				escaped = true
			} else if char == '"' {
				inString = false
			}
		case char == '"':
			inString = true
		case char == '-' || char >= '0' && char <= '9':
			end := index + 1
			for end < len(text) && strings.IndexByte("0123456789+-.eE", text[end]) >= 0 {
				end++
			}
			token := text[index:end]
			if value, err := strconv.ParseInt(token, 10, 64); !strings.ContainsAny(token, ".eE+") && (err != nil || value > 1<<53-1 || value < -(1<<53-1)) {
				out.WriteByte('"')
				out.WriteString(token)
				out.WriteByte('"')
			} else {
				out.WriteString(token)
			}
			index = end
			continue
		}
		out.WriteByte(char)
		index++
	}
	return out.String()
}

func moduleHTMLTree(node *html.Node) jsHTMLNode {
	var converted jsHTMLNode
	switch node.Type {
	case html.TextNode:
		return jsHTMLNode{Text: node.Data}
	case html.ElementNode:
		converted.Tag = node.Data
		if len(node.Attr) > 0 {
			converted.Attrs = make(map[string]string, len(node.Attr))
			for _, attribute := range node.Attr {
				converted.Attrs[attribute.Key] = attribute.Val
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == html.TextNode || child.Type == html.ElementNode {
			converted.Children = append(converted.Children, moduleHTMLTree(child))
		}
	}
	return converted
}
