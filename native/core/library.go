package core

func DramaProvider(drama Drama) string {
	if source := canonicalProviderSource(drama.Source); source != "" {
		return source
	}
	if source, _, ok := SplitProviderDramaID(drama.ID); ok {
		return source
	}
	return "cloudfront"
}
