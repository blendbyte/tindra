package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blendbyte/tindra/internal/ingest"
)

func routerWithPublicURL(url string) http.Handler {
	return NewRouter(nil, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", url, "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

type configResp struct {
	PublicURL  string `json:"public_url"`
	RequireMFA bool   `json:"require_mfa"`
}

func TestHandleGetConfig_publicURLSet(t *testing.T) {
	h := routerWithPublicURL("https://tindra.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp configResp
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.PublicURL != "https://tindra.example.com" {
		t.Errorf("public_url: got %q, want %q", resp.PublicURL, "https://tindra.example.com")
	}
}

func TestHandleGetConfig_publicURLEmpty(t *testing.T) {
	h := routerWithPublicURL("")

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp configResp
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Key must be present even when empty - frontend depends on it.
	if resp.PublicURL != "" {
		t.Errorf("expected empty public_url, got %q", resp.PublicURL)
	}
}

func TestHandleGetConfig_requireMFA(t *testing.T) {
	h := routerWithPublicURL("")

	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var resp configResp
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// routerWithPublicURL passes requireMFA=true
	if !resp.RequireMFA {
		t.Error("require_mfa: expected true")
	}
}

func TestHandleGetConfig_noAuthRequired(t *testing.T) {
	h := routerWithPublicURL("https://tindra.example.com")

	// No session cookie, no bearer token - must still return 200.
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 without auth, got %d", rec.Code)
	}
}
