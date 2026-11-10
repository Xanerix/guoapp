package core

import "context"

type TextRequest struct {
	URL       string
	Referer   string
	UserAgent string
	Cookie    string
	NoCache   bool
	Body      []byte
}

type TextResponse struct {
	Body string
	URL  string
}

type Host interface {
	FetchText(ctx context.Context, request TextRequest) (TextResponse, error)
	PrepareWebMedia(ctx context.Context, media ProviderMedia, sourceName string) (ProviderMedia, error)
}

func (d *Downloader) FetchText(ctx context.Context, request TextRequest) (TextResponse, error) {
	responses := &playbackResponseURLs{}
	ctx = context.WithValue(ctx, playbackResponseURLsKey{}, responses)
	if request.UserAgent != "" {
		ctx = context.WithValue(ctx, providerTextUserAgentKey{}, request.UserAgent)
	}
	if request.Cookie != "" {
		ctx = context.WithValue(ctx, providerTextCookieKey{}, request.Cookie)
	}
	if request.NoCache {
		ctx = context.WithValue(ctx, providerTextNoCacheKey{}, true)
	}
	body, err := d.fetchProviderRequest(ctx, request.URL, request.Referer, request.Body)
	if err != nil {
		return TextResponse{}, err
	}
	address := request.URL
	if actual, found := responses.values.Load(request.URL); found {
		address = actual.(string)
	}
	return TextResponse{Body: body, URL: address}, nil
}

func (d *Downloader) PrepareWebMedia(ctx context.Context, media ProviderMedia, sourceName string) (ProviderMedia, error) {
	return d.prepareWebProviderMedia(ctx, media, sourceName)
}
