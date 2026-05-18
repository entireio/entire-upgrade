package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReleaseCheckerLatestStable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/latest" {
			t.Fatalf("path = %s, want /releases/latest", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"tag_name":"v0.6.1"}`))
	}))
	defer server.Close()

	got, err := (ReleaseChecker{BaseURL: server.URL}).Latest(context.Background(), StableChannel)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "0.6.1" {
		t.Fatalf("latest stable = %s, want 0.6.1", got)
	}
}

func TestReleaseCheckerLatestNightlyChoosesNewest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases" || r.URL.Query().Get("per_page") != "100" {
			t.Fatalf("url = %s, want /releases?per_page=100", r.URL.String())
		}
		_, _ = w.Write([]byte(`[
			{"tag_name":"v0.6.1"},
			{"tag_name":"v9.9.9-not-nightly.1"},
			{"tag_name":"v0.6.2-nightly.202605150717.11da3db0"},
			{"tag_name":"v0.6.2-nightly.202605160654.ddf1a331"}
		]`))
	}))
	defer server.Close()

	got, err := (ReleaseChecker{BaseURL: server.URL}).Latest(context.Background(), NightlyChannel)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "0.6.2-nightly.202605160654.ddf1a331" {
		t.Fatalf("latest nightly = %s", got)
	}
}
