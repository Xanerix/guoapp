package core

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

type providerTextUserAgentKey struct{}
type providerTextNoCacheKey struct{}
type providerTextCookieKey struct{}

var WebProviderNumericID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

func WebProviderInteger(value any, maximum int) (int, bool) {
	number, err := strconv.Atoi(NativeText(value))
	return number, err == nil && number >= 0 && number <= maximum
}

func ProviderHTMLAttr(node *html.Node, name string) string {
	if node != nil {
		for _, attribute := range node.Attr {
			if attribute.Key == name {
				return attribute.Val
			}
		}
	}
	return ""
}

func ProviderHTMLClass(node *html.Node, name string) bool {
	for _, value := range strings.Fields(ProviderHTMLAttr(node, "class")) {
		if value == name {
			return true
		}
	}
	return false
}

func ProviderHTMLNodes(root *html.Node, match func(*html.Node) bool) []*html.Node {
	var found []*html.Node
	if root == nil {
		return found
	}
	pending := []*html.Node{root}
	for len(pending) > 0 {
		node := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if match(node) {
			found = append(found, node)
		}
		for child := node.LastChild; child != nil; child = child.PrevSibling {
			pending = append(pending, child)
		}
	}
	return found
}

func ProviderHTMLFirstClass(root *html.Node, names ...string) *html.Node {
	for _, name := range names {
		matches := ProviderHTMLNodes(root, func(node *html.Node) bool {
			return ProviderHTMLClass(node, name)
		})
		if len(matches) > 0 {
			return matches[0]
		}
	}
	return nil
}

func ProviderHTMLText(root *html.Node) string {
	var text strings.Builder
	for _, node := range ProviderHTMLNodes(root, func(node *html.Node) bool {
		return node.Type == html.TextNode
	}) {
		if node.Parent != nil && (node.Parent.Data == "script" || node.Parent.Data == "style") {
			continue
		}
		text.WriteString(node.Data)
		text.WriteByte(' ')
	}
	return strings.Join(strings.Fields(text.String()), " ")
}

func FetchHostPage(ctx context.Context, host Host, address, referer, agent string) (*html.Node, string, error) {
	response, err := host.FetchText(ctx, TextRequest{URL: address, Referer: referer, UserAgent: agent})
	if err != nil {
		return nil, "", err
	}
	if len(response.Body) > 4<<20 {
		return nil, "", errors.New("站源页面过大")
	}
	document, err := html.Parse(strings.NewReader(response.Body))
	return document, response.URL, err
}

func (d *Downloader) prepareWebProviderMedia(ctx context.Context, media ProviderMedia, sourceName string) (ProviderMedia, error) {
	address, err := url.Parse(media.URL)
	if err != nil || !IsProviderHTTPMediaURL(media.URL) || address.User != nil {
		return ProviderMedia{}, fmt.Errorf("%s未返回有效播放地址，请刷新详情后重试", sourceName)
	}
	if strings.HasSuffix(strings.ToLower(address.Path), ".m3u8") {
		if media.Credentials != nil {
			ctx = providerMediaContext(ctx, media.Credentials)
		}
		media.Playlist, media.URL, err = d.fetchMediaPlaylist(ctx, media.URL, media.Referer)
		if err != nil {
			return ProviderMedia{}, fmt.Errorf("获取%s播放列表失败：%w", sourceName, err)
		}
		if !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(media.Playlist, "\ufeff")), "#EXTM3U") {
			return ProviderMedia{}, fmt.Errorf("%s播放列表无效，请重新解析播放", sourceName)
		}
		if duration := M3u8Duration(media.Playlist); duration > 0 {
			media.Duration = duration
		}
	}
	return media, nil
}
