package sources

import (
	"strconv"
	"strings"
	"time"

	"duanjuapp/native/core"
)

func providerTimestampDate(value string) string {
	stamp, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || stamp <= 0 {
		return ""
	}
	if stamp > 100_000_000_000 {
		stamp /= 1000
	}
	date := time.Unix(stamp, 0).In(core.ProviderChinaTime)
	if date.Year() < 2000 || date.Year() > 2100 {
		return ""
	}
	return date.Format("2006-01-02")
}
