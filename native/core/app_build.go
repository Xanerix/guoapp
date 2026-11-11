package core

import (
	"errors"
	"slices"
)

var errNativeBuildSource = errors.New("当前版本不包含此站源")

type buildFlavorSpec struct {
	name    string
	sources []string
}

var activeFlavor buildFlavorSpec

func RegisterFlavor(name string, sources []string) {
	if activeFlavor.name != "" && activeFlavor.name != name {
		panic("core.RegisterFlavor: conflicting flavors " + activeFlavor.name + " and " + name)
	}
	activeFlavor = buildFlavorSpec{name: name, sources: append(activeFlavor.sources, sources...)}
}

func buildFlavor() string { return activeFlavor.name }

func nativeSourceAvailable(source string) bool {
	source = canonicalProviderSource(source)
	if _, ok := registeredDescriptor(source); !ok {
		return false
	}
	return activeFlavor.sources == nil || slices.Contains(activeFlavor.sources, source)
}

func nativeDramaAvailable(drama LibraryDrama) bool {
	source, _, valid := SplitProviderDramaID(drama.ID)
	return valid && nativeSourceAvailable(source) &&
		(drama.Source == "" || canonicalProviderSource(drama.Source) == source)
}

func nativeChapterAvailable(drama LibraryDrama, chapter Chapter) bool {
	if !nativeDramaAvailable(drama) {
		return false
	}
	source, _, _ := SplitProviderDramaID(drama.ID)
	if chapter.Source != "" && canonicalProviderSource(chapter.Source) != source {
		return false
	}
	if chapterSource, _, valid := SplitProviderDramaID(chapter.ID); valid && chapterSource != source {
		return false
	}
	return true
}

func nativeDownloadAvailable(job nativeDownloadJob) bool {
	return nativeChapterAvailable(job.Drama, job.Chapter)
}

func nativeActionSource(input nativeInput) string {
	if input.Source != "" {
		return canonicalProviderSource(input.Source)
	}
	switch input.Action {
	case "soraniAccount":
		return "sorani"
	case "hongguoDevices", "recommendations", "cachedRecommendations", "suggestions":
		return "hongguo"
	}
	return ""
}

func nativeAuthorizeInput(input nativeInput) error {
	switch input.Action {
	case "recommendations", "cachedRecommendations", "suggestions", "soraniAccount", "sourceAccount":
		if !nativeSourceAvailable(nativeActionSource(input)) {
			return errNativeBuildSource
		}
	case "rankings":
		board, found := findRankingBoard(input.Board)
		if !found || !nativeSourceAvailable(board.Source) {
			return errNativeBuildSource
		}
	case "catalog", "cached", "categories":
		if !nativeSourceAvailable(input.Source) {
			return errNativeBuildSource
		}
	case "cover", "prepareCover", "detail", "seasons", "metadata", "followCheck", "resolve", "preload", "enqueueDownloads", "localPlayback":
		if !nativeDramaAvailable(input.Drama) {
			return errNativeBuildSource
		}
		if (input.Action == "resolve" || input.Action == "preload") && !nativeChapterAvailable(input.Drama, input.Chapter) {
			return errNativeBuildSource
		}
	}
	return nil
}
