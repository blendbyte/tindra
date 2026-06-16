package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func authHandlerWithURL(publicURL string) http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", publicURL, "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

func TestMFASetup_withPublicURL(t *testing.T) {
	testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)
	defer testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/mfa/setup", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	authHandlerWithURL("https://app.example.com").ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		URI string `json:"uri"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.Contains(resp.URI, "app.example.com") {
		t.Errorf("expected publicURL as issuer in TOTP URI, got %q", resp.URI)
	}
}

func TestMFAVerify_noMFASecret(t *testing.T) {
	testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)

	token, err := storage.CreateMFAChallenge(context.Background(), testPool, testUser.ID)
	if err != nil {
		t.Fatalf("create challenge: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"mfa_token": token, "code": "123456"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 when user has no MFA secret, got %d", rec.Code)
	}
}
