package sources

import (
	"math"
	"strconv"
	"strings"
	"time"

	"duanjuapp/native/core"
)

func hongguoHeat(row map[string]any) string {
	hot := nestedMap(row, "hot_score_data")

	if score := core.FirstNonEmpty(core.MapString(hot, "score"), core.MapString(row, "hot_score")); score != "" {
		value, err := strconv.ParseFloat(score, 64)
		if err == nil && value >= 0 && !math.IsInf(value, 0) && !math.IsNaN(value) {
			return score
		}
	}
	return core.MapString(hot, "text")
}

func hongguoOnlineDate(row map[string]any, now time.Time) string {
	if date := providerTimestampDate(core.MapString(row, "first_visible_time")); date != "" {
		return date
	}
	for _, value := range anyList(row["sub_title_list"]) {
		label := core.MapString(nestedMap(value), "content")
		day := now.In(core.ProviderChinaTime)
		switch label {
		case "今日上新":
			return day.Format("2006-01-02")
		case "昨日上新":
			return day.AddDate(0, 0, -1).Format("2006-01-02")
		default:
			if strings.HasSuffix(label, "上新") {
				date := core.NormalizeDate(label)
				if _, err := time.Parse("2006-01-02", date); err == nil {
					return date
				}
			}
		}
	}
	return ""
}
