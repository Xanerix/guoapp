package sources

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"duanjuapp/native/core"
)

var huangguoHTMLTag = regexp.MustCompile(`(?is)<(/?)([a-z][a-z0-9:-]*)\b(?:[^<>"']|"[^"]*"|'[^']*')*>`)

var huangguoNonContent = regexp.MustCompile(`(?is)<!--.*?-->|<script\b[^>]*>.*?</script\s*>|<style\b[^>]*>.*?</style\s*>`)

var huangguoTitleFragment = regexp.MustCompile(`(?i)(?:\b(?:class|type|title|aria-label)\s*=|(?:data|ata)-search-input|hg-search(?:__|-))`)

func huangguoElementBlock(raw string, start, end int, tag string) string {
	depth := 1
	for _, match := range huangguoHTMLTag.FindAllStringSubmatchIndex(raw[end:], -1) {
		if !strings.EqualFold(raw[end+match[4]:end+match[5]], tag) {
			continue
		}
		if match[3] > match[2] {
			depth--
		} else if !strings.HasSuffix(strings.TrimSpace(raw[end+match[0]:end+match[1]]), "/>") {
			depth++
		}
		if depth == 0 {
			return raw[start : end+match[1]]
		}
	}
	return raw[start:end]
}

func huangguoClassBlock(raw, className string) string {
	for _, match := range huangguoHTMLTag.FindAllStringSubmatchIndex(raw, -1) {
		if match[3] > match[2] {
			continue
		}
		for _, class := range strings.Fields(core.ExtractAttr(raw[match[0]:match[1]], "class")) {
			if class == className {
				return huangguoElementBlock(raw, match[0], match[1], raw[match[4]:match[5]])
			}
		}
	}
	return ""
}

func huangguoTitleNeedsRepair(title, sourceID string) bool {
	title = strings.TrimSpace(title)
	return title == "" || title == sourceID || title == "短剧" || title == "搜索" ||
		huangguoTitleFragment.MatchString(title) || strings.Contains(title, "搜索记录") && strings.Contains(title, "暂无搜索记录")
}

func firstHuangguoTitle(sourceID string, values ...string) string {
	for _, title := range values {
		if !huangguoTitleNeedsRepair(title, sourceID) {
			return strings.TrimSpace(title)
		}
	}
	return ""
}

func huangguoMapEpisodes(row map[string]any) string {
	for _, key := range []string{"episode_count", "episodeCount", "total_episodes", "totalEpisodes", "totalEpisode", "total_episode", "episodes", "total"} {
		if count, err := strconv.Atoi(core.MapString(row, key)); err == nil && count > 0 {
			return strconv.Itoa(count)
		}
	}
	return ""
}

func mergeDramaMetadata(base, extra core.Drama) core.Drama {
	if base.VIP == nil && extra.VIP != nil {
		value := *extra.VIP
		base.VIP = &value
	}
	if extra.SortMetadata != nil && (base.SortMetadata == nil || extra.SortMetadata.CheckedAt.After(base.SortMetadata.CheckedAt)) {
		base.SortMetadata = extra.SortMetadata
	}
	if strings.TrimSpace(base.ID) == "" {
		base.ID = extra.ID
	}
	if strings.TrimSpace(base.Source) == "" {
		base.Source = extra.Source
	}
	if strings.TrimSpace(base.SourceID) == "" {
		base.SourceID = extra.SourceID
	}
	if strings.TrimSpace(base.Title) == "" {
		base.Title = extra.Title
	}
	if strings.TrimSpace(base.Name) == "" {
		base.Name = extra.Name
	}
	if strings.TrimSpace(base.Desc) == "" {
		base.Desc = extra.Desc
	}
	if strings.TrimSpace(base.Intro) == "" {
		base.Intro = extra.Intro
	}
	if core.CoverPathFromAny(base.Cover) == "" {
		base.Cover = extra.Cover
	}
	if core.CoverPathFromAny(base.CoverURL) == "" {
		base.CoverURL = extra.CoverURL
	}
	if valueEmpty(base.TotalEpisode) {
		base.TotalEpisode = extra.TotalEpisode
	}
	if valueEmpty(base.EpisodeCount) {
		base.EpisodeCount = extra.EpisodeCount
	}
	if strings.TrimSpace(base.ChannelName) == "" {
		base.ChannelName = extra.ChannelName
	}
	if strings.TrimSpace(base.CategoryName) == "" || (base.CategoryName == "首页" && extra.CategoryName != "") {
		base.CategoryName = extra.CategoryName
	}
	if strings.TrimSpace(base.Remark) == "" || base.Remark == "在线观看" {
		base.Remark = extra.Remark
	}
	if strings.TrimSpace(base.Score) == "" {
		base.Score = extra.Score
	}
	if strings.TrimSpace(base.Views) == "" {
		base.Views = extra.Views
	}
	if strings.TrimSpace(base.Heat) == "" {
		base.Heat = extra.Heat
	}
	if strings.TrimSpace(base.OnlineDate) == "" {
		base.OnlineDate = extra.OnlineDate
	}
	if len(base.Tags) == 0 {
		base.Tags = extra.Tags
	}
	if base.ReleaseStatus == "" || base.ReleaseStatus == "unknown" {
		base.ReleaseStatus = extra.ReleaseStatus
	}
	if core.DramaProvider(base) == sourceHuangguoAI && base.ID == extra.ID {
		_, sourceID, _ := core.SplitProviderDramaID(base.ID)
		if title := firstHuangguoTitle(sourceID, base.Title, base.Name, extra.Title, extra.Name); title != "" {
			base.Title, base.Name = title, title
		}
	}
	return base
}

func valueEmpty(v any) bool {
	if v == nil {
		return true
	}
	s := strings.TrimSpace(fmt.Sprint(v))
	return s == "" || s == "0" || s == "<nil>"
}
