package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"log/slog"
	"net/http"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/blendbyte/tindra/internal/storage"
)

// handleMFASetup generates a new TOTP secret and stores it pending confirmation.
// Requires session auth. The secret is NOT active until handleMFAConfirm succeeds.
func (ro *router) handleMFASetup(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(ctxUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	user, err := storage.GetUserByID(r.Context(), ro.pool, userID)
	if err != nil {
		slog.Error("get user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	issuer := "Tindra"
	if ro.publicURL != "" {
		issuer = ro.publicURL
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: user.Email,
	})
	if err != nil {
		slog.Error("generate totp key", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := storage.StoreMFASecret(r.Context(), ro.pool, userID, key.Secret()); err != nil {
		slog.Error("store mfa secret", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	qrImg, err := key.Image(200, 200)
	if err != nil {
		slog.Error("generate qr image", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, qrImg); err != nil {
		slog.Error("encode qr png", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	qr := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	writeJSON(w, map[string]string{
		"secret": key.Secret(),
		"uri":    key.URL(),
		"qr":     qr,
	})
}

// handleMFAConfirm activates MFA after the user verifies their first TOTP code.
func (ro *router) handleMFAConfirm(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(ctxUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	secret, err := storage.GetMFASecret(r.Context(), ro.pool, userID)
	if err != nil {
		slog.Error("get mfa secret", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if secret == nil {
		http.Error(w, "no authenticator setup in progress", http.StatusBadRequest)
		return
	}

	if !totp.Validate(req.Code, *secret) {
		http.Error(w, "incorrect code", http.StatusUnauthorized)
		return
	}

	if err := storage.EnableMFA(r.Context(), ro.pool, userID); err != nil {
		slog.Error("enable mfa", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleMFADisable disables MFA after verifying the user's password.
func (ro *router) handleMFADisable(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(ctxUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	user, err := storage.GetUserByID(r.Context(), ro.pool, userID)
	if err != nil {
		slog.Error("get user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
		http.Error(w, "password is incorrect", http.StatusUnauthorized)
		return
	}

	if err := storage.DisableMFA(r.Context(), ro.pool, userID); err != nil {
		slog.Error("disable mfa", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleMFAVerify completes a login for users with MFA enabled.
// Called with the mfa_token issued during handleLogin when mfa_required is true.
func (ro *router) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.MFAToken == "" || req.Code == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Look up without consuming - keep the challenge alive so retries work.
	userID, err := storage.GetMFAChallenge(r.Context(), ro.pool, req.MFAToken)
	if err != nil {
		slog.Error("get mfa challenge", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if userID == "" {
		http.Error(w, "invalid or expired MFA token", http.StatusUnauthorized)
		return
	}

	secret, err := storage.GetMFASecret(r.Context(), ro.pool, userID)
	if err != nil {
		slog.Error("get mfa secret", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if secret == nil || !totp.Validate(req.Code, *secret) {
		http.Error(w, "invalid code", http.StatusUnauthorized)
		return
	}

	// Code is correct - now consume the challenge to prevent replay.
	if _, err := storage.ConsumeMFAChallenge(r.Context(), ro.pool, req.MFAToken); err != nil {
		slog.Error("consume mfa challenge", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	session, err := storage.CreateSession(r.Context(), ro.pool, userID)
	if err != nil {
		slog.Error("create session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "tindra_session",
		Value:    session.Token,
		HttpOnly: true,
		Secure:   ro.cookieSecure,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
		Expires:  session.ExpiresAt,
	})
	w.WriteHeader(http.StatusOK)
}
