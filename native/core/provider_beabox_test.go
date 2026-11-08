package core

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

func beaboxFixtureCiphertext(t *testing.T, value any) string {
	t.Helper()
	plain, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(beaboxCipherKey)
	if err != nil {
		t.Fatal(err)
	}
	padded := pkcs7Pad(append([]byte(nil), plain...), aes.BlockSize)
	ciphertext := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ciphertext, padded)
	return hex.EncodeToString(append(iv, ciphertext...))
}

func TestBeaboxPublishPageDecodesDomainPool(t *testing.T) {
	hosts := []string{"sample4066.top", "sample4254.top"}
	raw, err := json.Marshal(hosts)
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("secureKey")
	for index := range raw {
		raw[index] ^= key[index%len(key)]
	}
	page := `<script>window.__ENC_DOMAINS = "` + base64.StdEncoding.EncodeToString(raw) + `";
window.__KEY = "secureKey";</script>`
	sites := parseBeaboxPublishPage(page)
	if len(sites) != 2 || sites[0] != "https://sample4066.top" || sites[1] != "https://sample4254.top" {
		t.Fatalf("beabox publish page parsed incorrectly: %v", sites)
	}
	if parseBeaboxPublishPage(`<html></html>`) != nil {
		t.Fatal("beabox accepted a page without a domain pool")
	}
}

func TestBeaboxPayloadDecryptsHexEnvelope(t *testing.T) {
	body := map[string]any{"total": 2, "page": 1, "data": []any{map[string]any{"vodId": "sample01"}}}
	encoded := beaboxFixtureCiphertext(t, body)
	envelope, err := parseBeaboxEnvelope([]byte(`{"code":200,"msg":"OK","data":"` + encoded + `"}`))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := beaboxEnvelopeData(envelope)
	if err != nil {
		t.Fatal(err)
	}
	row, valid := payload.(map[string]any)
	if !valid || mapString(row, "total") != "2" {
		t.Fatalf("beabox encrypted payload not decoded: %+v", payload)
	}
	failed, err := parseBeaboxEnvelope([]byte(`{"code":403,"msg":"no auth","data":""}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := beaboxEnvelopeData(failed); err == nil {
		t.Fatal("beabox accepted a failed envelope")
	}
	if _, err := parseBeaboxEnvelope([]byte(`<html></html>`)); err == nil {
		t.Fatal("beabox accepted a non-api page")
	}
}

type beaboxFixtureSites struct {
	mu            sync.Mutex
	requests      []string
	publishOnline bool
}

func (sites *beaboxFixtureSites) take() []string {
	sites.mu.Lock()
	defer sites.mu.Unlock()
	requests := sites.requests
	sites.requests = nil
	return requests
}

func (sites *beaboxFixtureSites) downloader(t *testing.T, dataDir string) *Downloader {
	t.Helper()
	raw, err := json.Marshal([]string{"dead.example.test", "live.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	for index := range raw {
		raw[index] ^= 'k'
	}
	publish := `window.__ENC_DOMAINS = "` + base64.StdEncoding.EncodeToString(raw) + `"; window.__KEY = "k";`
	d := sourceFixtureDownloader(t, func(request *http.Request) (*http.Response, error) {
		sites.mu.Lock()
		sites.requests = append(sites.requests, request.URL.Host+request.URL.Path)
		online := sites.publishOnline
		sites.mu.Unlock()
		switch request.URL.Host {
		case "beabox.cc":
			if online {
				return sourceFixtureResponse(request, http.StatusOK, publish), nil
			}
		case "dead.example.test":
			return sourceFixtureResponse(request, http.StatusOK, "<html>parked</html>"), nil
		case "live.example.test":
			if request.URL.Path == "/api/vod/detail/missing" {
				return sourceFixtureResponse(request, http.StatusNotFound, `{"code":404,"msg":"missing"}`), nil
			}
			return sourceFixtureResponse(request, http.StatusOK, `{"code":200,"data":{"home_tags":"a,b"}}`), nil
		}
		return nil, errors.New("offline")
	})
	d.cfg.dataDir = dataDir
	return d
}

func TestBeaboxCallFailsOverOnlyFromUnreachableSites(t *testing.T) {
	sites := &beaboxFixtureSites{publishOnline: true}
	d := sites.downloader(t, t.TempDir())
	categories, err := d.fetchBeaboxCategories(context.Background())
	if err != nil || len(categories) != 2 {
		t.Fatalf("beabox categories = %+v, %v", categories, err)
	}
	want := []string{"bh4066.top/api/configs", "beabox.cc/", "dead.example.test/api/configs", "live.example.test/api/configs"}
	if requests := sites.take(); !slices.Equal(requests, want) {
		t.Fatalf("beabox failover requests = %v, want %v", requests, want)
	}
	if _, _, err := d.fetchBeaboxDetail(context.Background(), "missing"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("beabox detail error = %v", err)
	}
	if requests := sites.take(); !slices.Equal(requests, []string{"live.example.test/api/vod/detail/missing"}) {
		t.Fatalf("beabox api error rotated sites: %v", requests)
	}
}

func TestBeaboxSitesSurviveRestart(t *testing.T) {
	sites := &beaboxFixtureSites{publishOnline: true}
	dataDir := t.TempDir()
	if _, err := sites.downloader(t, dataDir).fetchBeaboxCategories(context.Background()); err != nil {
		t.Fatal(err)
	}
	sites.take()
	if _, err := sites.downloader(t, dataDir).fetchBeaboxCategories(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests := sites.take(); !slices.Equal(requests, []string{"live.example.test/api/configs"}) {
		t.Fatalf("beabox restart did not reuse the saved site: %v", requests)
	}

	sites.publishOnline = false
	stale := t.TempDir()
	state := `{"version":1,"site":"https://dead.example.test","pool":["https://live.example.test","http://insecure.example.test","https://bad.example.test/path"]}`
	if err := writeNativeCacheFile(filepath.Join(stale, "beabox-sites.json"), []byte(state)); err != nil {
		t.Fatal(err)
	}
	d := sites.downloader(t, stale)
	if _, err := d.fetchBeaboxCategories(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{"dead.example.test/api/configs", "bh4066.top/api/configs", "beabox.cc/", "beabox.live/", "live.example.test/api/configs"}
	if requests := sites.take(); !slices.Equal(requests, want) {
		t.Fatalf("beabox saved pool fallback requests = %v, want %v", requests, want)
	}
	if !slices.Equal(d.beaboxPool, []string{"https://live.example.test"}) {
		t.Fatalf("beabox loaded invalid saved sites: %v", d.beaboxPool)
	}
}

func TestBeaboxDetailMapsPlaySources(t *testing.T) {
	row := map[string]any{
		"vodId": "sample01", "vodName": "<em>样本</em>", "vodPic": "https://media.example.test/cover.txt",
		"vodClass": []any{"<em>分类甲</em>", "分类乙", "&lt;em&gt;分类丙&lt;/em&gt;"}, "vodTime": json.Number("1794141003"),
		"vodPlaySource": map[string]any{
			"beabox-app": []any{
				map[string]any{"from": "beabox-app", "flag": "第 1 集", "url": "https://media.example.test/1.m3u8", "playUrl": "https://media.example.test/1.m3u8"},
				map[string]any{"from": "beabox-app", "flag": "第 2 集", "url": "https://media.example.test/2.m3u8", "playUrl": "https://media.example.test/2.m3u8"},
			},
			"other": []any{
				map[string]any{"from": "other", "needParse": true, "playUrl": "sample-token"},
			},
		},
		"playNeedParse": false,
	}
	drama, err := beaboxDramaFromMap(row, "https://site.example.test", "")
	if err != nil || drama.ID != "beabox:sample01" || drama.Title != "样本" || drama.CategoryName != "分类甲" || drama.OnlineDate == "" {
		t.Fatalf("beabox drama mapping failed: %+v %v", drama, err)
	}
	if len(drama.Tags) != 3 || drama.Tags[0] != "分类甲" || drama.Tags[1] != "分类乙" || drama.Tags[2] != "分类丙" {
		t.Fatalf("beabox tags were not cleaned: %v", drama.Tags)
	}
	fallback, err := beaboxDramaFromMap(map[string]any{"vodId": "sample02", "vodName": "样本二"}, "https://site.example.test", "分类乙")
	if err != nil || fallback.CategoryName != "分类乙" {
		t.Fatalf("beabox fallback category lost: %+v %v", fallback, err)
	}
	chapters := beaboxChapters(row, "https://site.example.test", "sample01")
	if len(chapters) != 2 || chapters[1].Title != "第 2 集" || chapters[0].Source != sourceBeabox ||
		!strings.HasPrefix(chapters[0].VideoURL, "beabox-play://") {
		t.Fatalf("beabox chapters mapped incorrectly: %+v", chapters)
	}
	if entry, ok := beaboxEntryAt(row, "other", 0); !ok || !beaboxEntryNeedsParse(entry, row) {
		t.Fatal("beabox parse flag was lost")
	}
}

func TestBeaboxTextCoverDecodesXoredDataURL(t *testing.T) {
	image := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 1, 2, 3}
	encoded := []byte("data:image/png;base64," + base64.StdEncoding.EncodeToString(image))
	for index := 0; index < min(4096, len(encoded)); index++ {
		encoded[index] ^= 18
	}
	decoded := nativeDecodeCover(encoded)
	if !bytes.Equal(decoded, image) {
		t.Fatalf("beabox text cover not decoded: %x", decoded)
	}
}

func TestBeaboxSearchHighlightRemovalKeepsTextJoined(t *testing.T) {
	for input, want := range map[string]string{
		"分<em>类</em>今天天气不错":          "分类今天天气不错",
		"&lt;em&gt;分&lt;/em&gt;类 今天": "分类 今天",
		"<em class=\"hl\">分类</em>":   "分类",
		"分 <em>类</em> 今天":            "分 类 今天",
	} {
		if got := beaboxCleanText(input); got != want {
			t.Fatalf("beabox highlight cleanup = %q, want %q (input %q)", got, want, input)
		}
	}
}

func TestBeaboxCategoryValidation(t *testing.T) {
	for _, category := range []string{"", "tag:分类甲", "tag:古装"} {
		if !validBeaboxCategory(category) {
			t.Fatalf("beabox rejected valid category: %q", category)
		}
	}
	for _, category := range []string{"分类甲", "tag:", "tag:a/b", "tag:" + strings.Repeat("长", 25)} {
		if validBeaboxCategory(category) {
			t.Fatalf("beabox accepted invalid category: %q", category)
		}
	}
}
