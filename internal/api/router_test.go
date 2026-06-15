package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
)

// --- SetLimits ---

func TestSetLimits_updatesLimitsAtRuntime(t *testing.T) {
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "test-stats-key", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)

	// Initial state: limits are 0 (unlimited). Confirm via the quota endpoint first.
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/"+testProject.ID+"/quota", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("quota: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var before struct {
		EventLimit int `json:"event_limit"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&before); err != nil {
		t.Fatalf("decode before: %v", err)
	}
	if before.EventLimit != 0 {
		t.Errorf("initial event_limit: got %d, want 0", before.EventLimit)
	}

	// Update limits.
	h.SetLimits(5, 10000, 50)

	// Read back via the quota endpoint to confirm limits propagated.
	req2 := httptest.NewRequest(http.MethodGet,
		"/api/projects/"+testProject.ID+"/quota", nil)
	req2.AddCookie(authCookie())
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("quota after SetLimits: expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	var after struct {
		EventLimit int `json:"event_limit"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&after); err != nil {
		t.Fatalf("decode after: %v", err)
	}
	if after.EventLimit != 10000 {
		t.Errorf("after SetLimits, event_limit: got %d, want 10000", after.EventLimit)
	}
}

func TestSetLimits_zeroDisablesLimit(t *testing.T) {
	// projectLimit=0, eventLimit=999, userLimit=0
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "test-stats-key", "", 0, 0, 999, 0, 0, 0, nil, false, true, nil)

	// Confirm the initial non-zero event limit is visible.
	req := httptest.NewRequest(http.MethodGet,
		"/api/projects/"+testProject.ID+"/quota", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		EventLimit int `json:"event_limit"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.EventLimit != 999 {
		t.Errorf("initial event_limit: got %d, want 999", resp.EventLimit)
	}

	// Setting 0 should clear the limit (unlimited).
	h.SetLimits(0, 0, 0)

	req2 := httptest.NewRequest(http.MethodGet,
		"/api/projects/"+testProject.ID+"/quota", nil)
	req2.AddCookie(authCookie())
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec2.Code)
	}
	var resp2 struct {
		EventLimit int `json:"event_limit"`
	}
	if err := json.NewDecoder(rec2.Body).Decode(&resp2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp2.EventLimit != 0 {
		t.Errorf("after SetLimits(0,0,0), event_limit: got %d, want 0", resp2.EventLimit)
	}
}

// --- handleMFASetup unauthenticated ---

func TestMFASetup_unauthenticated(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/mfa/setup", nil)
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated MFA setup, got %d", rec.Code)
	}
}

// --- writeJSONStatus: verify Content-Type and status code propagation ---

func TestWriteJSONStatus_viaStatsEndpoint(t *testing.T) {
	h := api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "my-stats-key", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	req.Header.Set("Authorization", "Bearer my-stats-key")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want \"application/json\"", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	if _, ok := body["projects"]; !ok {
		t.Error("expected 'projects' key in stats response")
	}
}

func TestWriteJSON_contentTypeSet(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	issuesHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want \"application/json\"", ct)
	}
}
