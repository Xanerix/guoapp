package core

import (
	"encoding/json"
	"net/http"
	"testing"
)

func mediaTestRequest(t *testing.T, credentials *MediaCredentials) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "https://media.example.invalid/video.mp4", nil)
	if err != nil {
		t.Fatal(err)
	}
	mediaRequestHeaders(request, "https://www.example.invalid/watch/1")
	if err := credentials.apply(request); err != nil {
		t.Fatal(err)
	}
	return request
}

func TestMediaCredentialsKeepFetchMetadataByDefault(t *testing.T) {
	request := mediaTestRequest(t, &MediaCredentials{})
	if request.Header.Get("Sec-Fetch-Mode") != "cors" || request.Header.Get("Sec-Fetch-Dest") != "empty" {
		t.Fatalf("默认应保留 Sec-Fetch 头，得到 %v", request.Header)
	}
}

func TestMediaCredentialsOmitFetchMetadata(t *testing.T) {
	var media jsMedia
	if err := json.Unmarshal([]byte(`{"credentials":{"omitFetchMetadata":true}}`), &media); err != nil {
		t.Fatal(err)
	}
	credentials := media.providerMedia().Credentials
	request := mediaTestRequest(t, credentials)
	for _, name := range []string{"Sec-Fetch-Mode", "Sec-Fetch-Dest"} {
		if value, ok := request.Header[name]; ok {
			t.Fatalf("声明 omitFetchMetadata 后不应发送 %s，得到 %q", name, value)
		}
	}
	if !moduleMedia(media.providerMedia()).Credentials.OmitFetchMetadata {
		t.Fatal("回传给模块时丢失 omitFetchMetadata")
	}
}
