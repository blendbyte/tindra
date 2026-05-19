package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestLogin_noMFA(t *testing.T) {
	body := bytes.NewBufferString(`{"email":"test@example.com","password":"testpassword"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// No mfa_required field - should have set a session cookie.
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["mfa_required"] == true {
		t.Error("expected no mfa_required for user without MFA")
	}
	hasCookie := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "tindra_session" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Error("expected tindra_session cookie on login without MFA")
	}
}

func TestMFA_setupConfirmAndLogin(t *testing.T) {
	h := authHandler()

	// 1. Setup: get a provisioning URI.
	setupReq := httptest.NewRequest(http.MethodGet, "/api/auth/mfa/setup", nil)
	setupReq.AddCookie(authCookie())
	setupRec := httptest.NewRecorder()
	h.ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup: expected 200, got %d: %s", setupRec.Code, setupRec.Body.String())
	}
	var setupResp struct {
		Secret string `json:"secret"`
		URI    string `json:"uri"`
	}
	if err := json.NewDecoder(setupRec.Body).Decode(&setupResp); err != nil {
		t.Fatalf("setup decode: %v", err)
	}
	if setupResp.Secret == "" {
		t.Fatal("expected non-empty secret")
	}

	// 2. Confirm with a valid TOTP code.
	code, err := totp.GenerateCode(setupResp.Secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	confirmBody, _ := json.Marshal(map[string]string{"code": code})
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/confirm", bytes.NewBuffer(confirmBody))
	confirmReq.AddCookie(authCookie())
	confirmReq.Header.Set("Content-Type", "application/json")
	confirmRec := httptest.NewRecorder()
	h.ServeHTTP(confirmRec, confirmReq)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: expected 200, got %d: %s", confirmRec.Code, confirmRec.Body.String())
	}

	// 3. Login now returns mfa_required=true + mfa_token.
	loginBody := bytes.NewBufferString(`{"email":"test@example.com","password":"testpassword"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", loginBody)
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := httptest.NewRecorder()
	h.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login: expected 200, got %d", loginRec.Code)
	}
	var loginResp struct {
		MFARequired bool   `json:"mfa_required"`
		MFAToken    string `json:"mfa_token"`
	}
	if err := json.NewDecoder(loginRec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("login decode: %v", err)
	}
	if !loginResp.MFARequired {
		t.Fatal("expected mfa_required=true after enabling MFA")
	}
	if loginResp.MFAToken == "" {
		t.Fatal("expected non-empty mfa_token")
	}

	// 4. Complete MFA challenge with a fresh TOTP code.
	code2, _ := totp.GenerateCode(setupResp.Secret, time.Now())
	verifyBody, _ := json.Marshal(map[string]string{
		"mfa_token": loginResp.MFAToken,
		"code":      code2,
	})
	verifyReq := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", bytes.NewBuffer(verifyBody))
	verifyReq.Header.Set("Content-Type", "application/json")
	verifyRec := httptest.NewRecorder()
	h.ServeHTTP(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("verify: expected 200, got %d: %s", verifyRec.Code, verifyRec.Body.String())
	}
	hasCookie := false
	for _, c := range verifyRec.Result().Cookies() {
		if c.Name == "tindra_session" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Error("expected tindra_session cookie after successful MFA verify")
	}

	// 5. Disable MFA so the test user is clean for other tests.
	disableBody, _ := json.Marshal(map[string]string{"password": "testpassword"})
	disableReq := httptest.NewRequest(http.MethodDelete, "/api/auth/mfa", bytes.NewBuffer(disableBody))
	disableReq.AddCookie(authCookie())
	disableReq.Header.Set("Content-Type", "application/json")
	disableRec := httptest.NewRecorder()
	h.ServeHTTP(disableRec, disableReq)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("disable: expected 200, got %d: %s", disableRec.Code, disableRec.Body.String())
	}
}

func TestMFA_confirmWrongCode(t *testing.T) {
	// Setup (generates a secret into the DB).
	setupReq := httptest.NewRequest(http.MethodGet, "/api/auth/mfa/setup", nil)
	setupReq.AddCookie(authCookie())
	setupRec := httptest.NewRecorder()
	authHandler().ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("setup: %d", setupRec.Code)
	}

	// Try to confirm with a bad code.
	body, _ := json.Marshal(map[string]string{"code": "000000"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/confirm", bytes.NewBuffer(body))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for wrong code, got %d", rec.Code)
	}
}

func TestMFA_verifyExpiredToken(t *testing.T) {
	body, _ := json.Marshal(map[string]string{
		"mfa_token": "0000000000000000000000000000000000000000",
		"code":      "123456",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/mfa/verify", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid mfa_token, got %d", rec.Code)
	}
}

func TestListProviders_empty(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/providers", nil)
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Providers []string `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// No env vars set in tests - providers list should be empty (nil providers).
	if len(resp.Providers) != 0 {
		t.Errorf("expected 0 providers in test env, got %d", len(resp.Providers))
	}
}

func TestOAuthRedirect_unknownProvider(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/no-such-provider/redirect", nil)
	rec := httptest.NewRecorder()
	authHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}
