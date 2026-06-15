package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestSemverGT(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"v2.0.0", "v1.0.0", true},
		{"v1.1.0", "v1.0.0", true},
		{"v1.0.1", "v1.0.0", true},
		{"v10.0.0", "v9.99.99", true},
		{"v1.2.3-beta", "v1.2.2", true}, // pre-release stripped to 1.2.3 > 1.2.2
		{"v1.0.0", "v1.0.0", false},
		{"v1.0.0", "v2.0.0", false},
		{"v1.0.0", "v1.1.0", false},
		{"v1.0.0", "v1.0.1", false},
		{"v1.2.3-beta", "v1.2.3", false}, // stripped pre-release equals v1.2.3
		{"dev", "v1.0.0", false},         // non-semver
		{"v1.0.0", "dev", false},         // non-semver
		{"", "", false},
	}

	for _, tc := range tests {
		got := semverGT(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("semverGT(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func newTestRouter() *Handle {
	return NewRouter(nil, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

func TestCheckLatestVersion_updatesCache(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{
			TagName: "v9.9.9",
			HTMLURL: "https://example.com/releases/v9.9.9",
		})
	}))
	defer srv.Close()

	old := updateCheckURL
	updateCheckURL = srv.URL
	defer func() { updateCheckURL = old }()

	h := newTestRouter()
	h.ro.checkLatestVersion(context.Background())

	got, url := h.ro.getLatestRelease()
	if got != "v9.9.9" {
		t.Errorf("latestVersion: got %q, want %q", got, "v9.9.9")
	}
	if url != "https://example.com/releases/v9.9.9" {
		t.Errorf("releaseURL: got %q, want %q", url, "https://example.com/releases/v9.9.9")
	}
}

func TestCheckLatestVersion_ignoresPrerelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{
			TagName:    "v9.9.9",
			Prerelease: true,
		})
	}))
	defer srv.Close()

	old := updateCheckURL
	updateCheckURL = srv.URL
	defer func() { updateCheckURL = old }()

	h := newTestRouter()
	h.ro.checkLatestVersion(context.Background())

	got, _ := h.ro.getLatestRelease()
	if got != "" {
		t.Errorf("expected empty latestVersion for prerelease, got %q", got)
	}
}

func TestCheckLatestVersion_ignoresDraft(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(githubRelease{
			TagName: "v9.9.9",
			Draft:   true,
		})
	}))
	defer srv.Close()

	old := updateCheckURL
	updateCheckURL = srv.URL
	defer func() { updateCheckURL = old }()

	h := newTestRouter()
	h.ro.checkLatestVersion(context.Background())

	got, _ := h.ro.getLatestRelease()
	if got != "" {
		t.Errorf("expected empty latestVersion for draft, got %q", got)
	}
}

func TestCheckLatestVersion_nonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	old := updateCheckURL
	updateCheckURL = srv.URL
	defer func() { updateCheckURL = old }()

	h := newTestRouter()
	h.ro.checkLatestVersion(context.Background())

	got, _ := h.ro.getLatestRelease()
	if got != "" {
		t.Errorf("expected empty latestVersion on non-200, got %q", got)
	}
}

func TestCheckLatestVersion_badJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer srv.Close()

	old := updateCheckURL
	updateCheckURL = srv.URL
	defer func() { updateCheckURL = old }()

	h := newTestRouter()
	h.ro.checkLatestVersion(context.Background())

	got, _ := h.ro.getLatestRelease()
	if got != "" {
		t.Errorf("expected empty latestVersion for bad JSON, got %q", got)
	}
}

func TestCheckLatestVersion_sendsUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		json.NewEncoder(w).Encode(githubRelease{TagName: "v1.0.0"})
	}))
	defer srv.Close()

	old := updateCheckURL
	updateCheckURL = srv.URL
	defer func() { updateCheckURL = old }()

	h := newTestRouter()
	h.ro.checkLatestVersion(context.Background())

	if gotUA == "" {
		t.Error("expected User-Agent header to be set")
	}
	if len(gotUA) < 7 || gotUA[:7] != "Tindra/" {
		t.Errorf("User-Agent: got %q, want prefix Tindra/", gotUA)
	}
}
