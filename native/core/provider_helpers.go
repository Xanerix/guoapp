package core

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var reTitleTag = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

var reH1Tag = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)

var reDescMeta = regexp.MustCompile(`(?is)<meta\b[^>]*(?:name|property)=["'](?:description|og:description)["'][^>]*content=["']([^"']+)["'][^>]*>`)

var reEpisodeNumber = regexp.MustCompile(`(?i)(?:第\s*0*(\d+)\s*(?:集|话|期)|(?:更新至|共|全)\s*0*(\d+)\s*(?:集|话|期)|(?:episode|ep)\s*#?\s*0*(\d+))`)

var ReDateText = regexp.MustCompile(`\d{4}-\d{1,2}-\d{1,2}`)

var ReViewsText = regexp.MustCompile(`(?i)[0-9]+(?:\.[0-9]+)?\s*[w万]?\s*次播放`)

func SortProviderChapters(chapters []Chapter) {
	sort.SliceStable(chapters, func(i, j int) bool {
		ei := ChapterEpisodeNumber(chapters[i], i+1)
		ej := ChapterEpisodeNumber(chapters[j], j+1)
		if ei != ej {
			return ei < ej
		}
		return chapters[i].ID < chapters[j].ID
	})
}

func ChapterEpisodeNumber(ch Chapter, fallback int) int {
	if len(ch.CurrentEpisode) > 0 && string(ch.CurrentEpisode) != "null" {
		var n int
		if err := json.Unmarshal(ch.CurrentEpisode, &n); err == nil && n > 0 {
			return n
		}
		var s string
		if err := json.Unmarshal(ch.CurrentEpisode, &s); err == nil {
			return EpisodeIndex(s, fallback)
		}
	}
	return EpisodeIndex(ch.Title, fallback)
}

func RawEpisode(n int) json.RawMessage {
	if n <= 0 {
		n = 1
	}
	return json.RawMessage(strconv.Itoa(n))
}

func EpisodeIndex(s string, fallback int) int {
	if m := reEpisodeNumber.FindStringSubmatch(s); len(m) > 1 {
		for _, group := range m[1:] {
			if group == "" {
				continue
			}
			if n, err := strconv.Atoi(group); err == nil && n > 0 {
				return n
			}
		}
	}
	return fallback
}

func ExtractPageTitle(rawHTML string) string {
	if m := reH1Tag.FindStringSubmatch(rawHTML); len(m) > 1 {
		if t := CleanText(m[1]); t != "" {
			return t
		}
	}
	if m := regexp.MustCompile(`(?is)<meta\b[^>]*property=["']og:title["'][^>]*content=["']([^"']+)["'][^>]*>`).FindStringSubmatch(rawHTML); len(m) > 1 {
		if t := strings.TrimSpace(html.UnescapeString(m[1])); t != "" {
			return t
		}
	}
	if m := reTitleTag.FindStringSubmatch(rawHTML); len(m) > 1 {
		return CleanText(m[1])
	}
	return ""
}

func ExtractDescription(rawHTML string) string {
	if m := reDescMeta.FindStringSubmatch(rawHTML); len(m) > 1 {
		return strings.TrimSpace(html.UnescapeString(m[1]))
	}
	return ""
}

func ExtractAttr(block string, names ...string) string {
	for _, name := range names {
		re := regexp.MustCompile(`(?is)\b` + regexp.QuoteMeta(name) + `\s*=\s*["']([^"']+)["']`)
		if m := re.FindStringSubmatch(block); len(m) > 1 {
			return strings.TrimSpace(html.UnescapeString(m[1]))
		}
	}
	return ""
}

func FirstMatchText(re *regexp.Regexp, raw string) string {
	if m := re.FindString(raw); m != "" {
		return strings.TrimSpace(m)
	}
	return ""
}

func ExtractTags(raw string) []string {
	re := regexp.MustCompile(`(?is)<[^>]+class=["'][^"']*hg-tag[^"']*["'][^>]*>(.*?)</[^>]+>`)
	seen := map[string]bool{}
	var out []string
	for _, m := range re.FindAllStringSubmatch(raw, -1) {
		if len(m) < 2 {
			continue
		}
		v := CleanText(m[1])
		if v != "" && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func NormalizeDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	matches := ReDateText.FindAllString(s, -1)
	for _, m := range matches {
		parts := strings.Split(m, "-")
		if len(parts) != 3 {
			continue
		}
		y, yErr := strconv.Atoi(parts[0])
		mo, moErr := strconv.Atoi(parts[1])
		d, dErr := strconv.Atoi(parts[2])
		if yErr == nil && moErr == nil && dErr == nil && y >= 2000 && y <= 2100 && mo >= 1 && mo <= 12 && d >= 1 && d <= 31 {
			return fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
		}
	}
	return ""
}

func NormalizeViews(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "次播放") {
		return s
	}
	if strings.ContainsAny(strings.ToLower(s), "w万") {
		return s + "次播放"
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	if n <= 0 {
		return ""
	}
	return strconv.FormatFloat(n, 'f', -1, 64) + "次播放"
}

func CleanText(s string) string {
	s = regexp.MustCompile(`(?is)<script\b.*?</script>`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?is)<style\b.*?</style>`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = spaceChars.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func MapString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch x := v.(type) {
			case string:
				if text := strings.TrimSpace(x); text != "" {
					return text
				}
			case bool:
				return strconv.FormatBool(x)
			case float64:
				if x == float64(int64(x)) {
					return strconv.FormatInt(int64(x), 10)
				}
				return strconv.FormatFloat(x, 'f', -1, 64)
			case int:
				return strconv.Itoa(x)
			case json.Number:
				return x.String()
			}
		}
	}
	return ""
}

func MapStringSlice(m map[string]any, keys ...string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, key := range keys {
		v, ok := m[key]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case string:
			for _, part := range strings.FieldsFunc(x, func(r rune) bool { return r == ',' || r == '/' || r == '，' || r == '、' }) {
				add(part)
			}
		case []any:
			for _, item := range x {
				if row, ok := item.(map[string]any); ok {
					if name := MapString(row, "name", "title", "label", "tag"); name != "" {
						add(name)
					}
					continue
				}
				add(fmt.Sprint(item))
			}
		}
	}
	return out
}

func ResolveProviderURL(baseURL, ref string) string {
	ref = strings.TrimSpace(html.UnescapeString(ref))
	if ref == "" {
		return ""
	}
	ref = strings.ReplaceAll(ref, `\/`, `/`)
	if strings.HasPrefix(ref, "//") {
		return "https:" + ref
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if u.IsAbs() {
		if u.Scheme == "http" {
			u.Scheme = "https"
		}
		return u.String()
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return ref
	}
	resolved := base.ResolveReference(u)
	if resolved.Scheme == "http" {
		resolved.Scheme = "https"
	}
	return resolved.String()
}

func NormalizeProviderMediaURL(pageURL, raw string) string {
	raw = strings.TrimSpace(html.UnescapeString(raw))
	raw = strings.Trim(raw, " \t\r\n\"'")
	if raw == "" {
		return ""
	}
	if unquoted, err := strconv.Unquote("\"" + strings.ReplaceAll(raw, "\"", "\\\"") + "\""); err == nil {
		raw = unquoted
	}
	raw = strings.ReplaceAll(raw, `\/`, `/`)
	resolved := ResolveProviderURL(pageURL, raw)
	if !strings.Contains(strings.ToLower(resolved), ".m3u8") && !strings.Contains(strings.ToLower(resolved), ".mp4") {
		return ""
	}
	return resolved
}

func IsProviderHTTPMediaURL(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil
}

func ProviderReleaseDate(value string) string {
	value = strings.TrimSpace(value)
	if stamp, err := time.Parse(time.RFC3339Nano, value); err == nil {
		value = stamp.In(ProviderChinaTime).Format("2006-01-02")
	}
	date := NormalizeDate(value)
	if parsed, err := time.Parse("2006-01-02", date); err != nil || parsed.Year() < 2000 || parsed.Year() > 2100 {
		return ""
	}
	return date
}

var ProviderChinaTime = time.FixedZone("CST", 8*60*60)
