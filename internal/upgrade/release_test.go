package upgrade

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseCheckerLatestStable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases/latest" {
			t.Fatalf("path = %s, want /releases/latest", r.URL.Path)
		}
		if got := r.Header.Get("User-Agent"); got != githubUserAgent {
			t.Fatalf("User-Agent = %q, want %q", got, githubUserAgent)
		}
		_, _ = w.Write([]byte(`{"tag_name":"v0.6.1"}`))
	}))
	defer server.Close()

	got, err := (ReleaseChecker{BaseURL: server.URL + "/"}).Latest(context.Background(), StableChannel)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "0.6.1" {
		t.Fatalf("latest stable = %s, want 0.6.1", got)
	}
}

func TestReleaseCheckerLatestNightlyChoosesNewest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases" || r.URL.Query().Get("per_page") != "100" || r.URL.Query().Get("page") != "1" {
			t.Fatalf("url = %s, want /releases?per_page=100&page=1", r.URL.String())
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

func TestReleaseCheckerLatestNightlyPaginates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/releases" || r.URL.Query().Get("per_page") != "100" {
			t.Fatalf("url = %s, want /releases?per_page=100", r.URL.String())
		}
		switch r.URL.Query().Get("page") {
		case "1":
			fmt.Fprint(w, `[`)
			for i := 0; i < githubReleasePageSize; i++ {
				if i > 0 {
					fmt.Fprint(w, `,`)
				}
				fmt.Fprintf(w, `{"tag_name":"v0.%d.0"}`, i)
			}
			fmt.Fprint(w, `]`)
		case "2":
			_, _ = w.Write([]byte(`[{"tag_name":"v0.6.2-nightly.202605160654.ddf1a331"}]`))
		default:
			t.Fatalf("unexpected page %q", r.URL.Query().Get("page"))
		}
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

func TestReleaseCheckerDoesNotSendTokenToOverrideBaseURL(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "secret")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("Authorization header = %q, want empty", got)
		}
		_, _ = w.Write([]byte(`{"tag_name":"v0.6.1"}`))
	}))
	defer server.Close()

	if _, err := (ReleaseChecker{BaseURL: server.URL}).Latest(context.Background(), StableChannel); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseCheckerIncludesErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer server.Close()

	_, err := (ReleaseChecker{BaseURL: server.URL}).Latest(context.Background(), StableChannel)
	if err == nil {
		t.Fatal("expected release lookup to fail")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error = %q, want response body", err)
	}
}
