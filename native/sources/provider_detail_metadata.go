package sources

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"duanjuapp/native/core"
)

func parseHongguoSortDetail(body, id string) (core.Drama, error) {
	row := nestedMap(routerLoaderMap(parseRouterData(body), "detail_page", "detail_"), "seriesDetail")
	if core.MapString(row, "series_id", "series_id_str") != id || core.MapString(row, "series_name", "series_title") == "" {
		return core.Drama{}, errors.New("红果网页详情与请求剧集不符")
	}
	patch := hongguoDramaFromAny(row, "")
	patch.Title = core.MapString(row, "series_name", "series_title")
	patch.Name = patch.Title
	patch.OnlineDate = providerTimestampDate(core.MapString(row, "first_visible_time"))
	if cover := hongguoCoverAddress(core.MapString(row, "series_cover", "cover")); cover != "" {
		patch.Cover, patch.CoverURL = cover, cover
	}
	return patch, nil
}

func parseHuangguoSortDetail(body, pageURL string, patch core.Drama) (core.Drama, error) {
	type entry struct {
		Type      string `json:"@type"`
		ID        string `json:"@id"`
		URL       string `json:"url"`
		Name      string `json:"name"`
		Published string `json:"datePublished"`
		Uploaded  string `json:"uploadDate"`
		Image     any    `json:"image"`
		Thumbnail any    `json:"thumbnailUrl"`
	}
	expected, _ := url.Parse(pageURL)
	matched := false
	for _, block := range rankingJSONLD.FindAllStringSubmatch(body, -1) {
		var graph struct {
			Graph []entry `json:"@graph"`
			entry
		}
		if json.Unmarshal([]byte(block[1]), &graph) != nil {
			continue
		}
		for _, row := range append(graph.Graph, graph.entry) {
			if row.Type != "WebPage" && row.Type != "VideoObject" && row.Type != "TVSeries" && row.Type != "Movie" {
				continue
			}
			actual, err := url.Parse(core.FirstNonEmpty(row.URL, row.ID))
			if err != nil || actual.Host != "" && !strings.EqualFold(actual.Host, expected.Host) && core.ProviderSourceForURL(actual.String()) != patch.Source || strings.TrimRight(actual.Path, "/") != strings.TrimRight(expected.Path, "/") || row.Name == "" || patch.Source == sourceHuangguoAI && huangguoTitleNeedsRepair(row.Name, patch.SourceID) {
				continue
			}
			matched = true
			patch.Title = row.Name
			if address := core.FirstNonEmpty(core.ProviderCoverAddress(row.Image, pageURL), core.ProviderCoverAddress(row.Thumbnail, pageURL)); address != "" {
				patch.Cover, patch.CoverURL = address, address
			}
			if date := core.ProviderReleaseDate(core.FirstNonEmpty(row.Uploaded, row.Published)); date != "" && (patch.OnlineDate == "" || row.Type == "VideoObject") {
				patch.OnlineDate = date
			}
		}
	}
	if !matched {
		return patch, fmt.Errorf("黄果详情没有返回所请求剧集的元数据")
	}
	if core.NativeNormalize(patch).Cover == "" {
		for _, tag := range core.CoverMetaTag.FindAllString(body, -1) {
			if strings.ToLower(core.ExtractAttr(tag, "property", "name")) == "og:image" {
				if address := core.ProviderCoverAddress(core.ExtractAttr(tag, "content"), pageURL); address != "" {
					patch.Cover, patch.CoverURL = address, address
					break
				}
			}
		}
	}
	markup := huangguoNonContent.ReplaceAllString(body, "")
	metaBlock := huangguoClassBlock(markup, "hg-web-detail__meta")
	meta := core.CleanText(metaBlock)
	patch.Views = core.NormalizeViews(core.FirstMatchText(core.ReViewsText, meta))
	if patch.OnlineDate == "" && strings.Contains(meta, "上线") {
		patch.OnlineDate = core.ProviderReleaseDate(core.FirstMatchText(core.ReDateText, meta))
	}
	if patch.Source == sourceHuangguoAI {
		episode := core.FirstNonEmpty(core.ExtractAttr(metaBlock, "data-ep-base"), meta)
		if count := core.EpisodeIndex(episode, 0); count > 0 {
			patch.TotalEpisode, patch.EpisodeCount = count, count
		}
	}
	return patch, nil
}

func huangguoDetailMetadata(body, pageURL, source, sourceID string) core.Drama {
	drama := core.Drama{ID: core.ProviderDramaID(source, sourceID), Source: source, SourceID: sourceID,
		Title: core.FirstNonEmpty(core.ExtractPageTitle(body), "短剧"), Desc: core.ExtractDescription(body), Tags: core.ExtractTags(body)}
	if patch, err := parseHuangguoSortDetail(body, pageURL, drama); err == nil {
		drama = patch
	}
	drama.Name, drama.Intro = drama.Title, drama.Desc
	meta := core.CleanText(huangguoClassBlock(huangguoNonContent.ReplaceAllString(body, ""), "hg-web-detail__meta"))
	drama.ReleaseStatus = core.ReleaseStatusFromRemark(meta)
	if drama.ReleaseStatus == "unknown" && source == sourceHuangguoVideo && !strings.HasPrefix(sourceID, "video/") {
		if header := reVideoDetailMeta.FindStringSubmatch(body); len(header) > 1 {
			drama.ReleaseStatus = core.ReleaseStatusFromRemark(core.CleanText(header[1]))
		}
	}
	return drama
}
