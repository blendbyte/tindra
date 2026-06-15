package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestMFASetup_authenticated(t *testing.T) {
	testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)
	defer testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/mfa/setup", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Secret string `json:"secret"`
		URI    string `json:"uri"`
		QR     string `json:"qr"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Secret == "" {
		t.Error("expected non-empty secret")
	}
	if resp.URI == "" {
		t.Error("expected non-empty uri")
	}
	if !strings.HasPrefix(resp.QR, "data:image/png;base64,") {
		t.Errorf("expected qr to start with data:image/png;base64,, got prefix %q", resp.QR)
	}
}

func TestMFASetup_storesSecret(t *testing.T) {
	testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)
	defer testPool.Exec(context.Background(), "UPDATE users SET mfa_secret = NULL WHERE id = $1", testUser.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/mfa/setup", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	ctx := context.Background()
	secret, err := storage.GetMFASecret(ctx, testPool, testUser.ID)
	if err != nil {
		t.Fatalf("GetMFASecret: %v", err)
	}
	if secret == nil {
		t.Fatal("expected non-nil secret stored in DB")
	}
	if *secret == "" {
		t.Error("expected non-empty secret stored in DB")
	}
}
