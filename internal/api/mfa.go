package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"

	"github.com/blendbyte/tindra/internal/storage"
)

// totpIssuer derives an issuer label from PUBLIC_URL.
//
// The otpauth label is "issuer:account", so a colon or slash in the issuer
// produces a URI that authenticator apps either mis-split or reject outright.
// Feeding PUBLIC_URL in raw yielded "otpauth://totp/https://host:user@example.com".
// Use the bare hostname, and fall back to "Tindra" if nothing usable is left.
func totpIssuer(publicURL string) string {
	const fallback = "Tindra"

	raw := strings.TrimSpace(publicURL)
	if raw == "" {
		return fallback
	}

	// A scheme-less PUBLIC_URL ("tindra.example.com:8443/app") parses as a path
	// rather than an authority. Prefixing "//" makes url.Parse read it as a host.
	if !strings.Contains(raw, "//") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fallback
	}

	// Hostname drops the port, userinfo, and IPv6 brackets.
	host := u.Hostname()
	// An unbracketed IPv6 literal would reintroduce the label delimiter, and an
	// IP address makes a poor issuer label anyway.
	if host == "" || strings.Contains(host, ":") {
		return fallback
	}
	return host
}

// handleMFASetup generates a new TOTP secret and stores it pending confirmation.
// Requires session auth. The secret is NOT active until handleMFAConfirm succeeds.
func (ro *router) handleMFASetup(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(ctxUserID).(string)

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

	// Replacing an enrolled factor requires proof of the current factor, which
	// also works for SSO-only accounts without a local password.
	var activeSecret *string
	if user.MFAEnabled {
		var req struct {
			Code string `json:"code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
			http.Error(w, "current authenticator code required", http.StatusUnauthorized)
			return
		}
		activeSecret, err = storage.GetMFASecret(r.Context(), ro.pool, userID)
		if err != nil {
			slog.Error("get active mfa secret", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if activeSecret == nil || !totp.Validate(req.Code, *activeSecret) {
			http.Error(w, "incorrect code", http.StatusUnauthorized)
			return
		}
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer(ro.publicURL),
		AccountName: user.Email,
	})
	if err != nil {
		slog.Error("generate totp key", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	staged, err := storage.SetPendingMFASecret(r.Context(), ro.pool, userID, key.Secret(), activeSecret)
	if err != nil {
		slog.Error("store mfa secret", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if !staged {
		http.Error(w, "authenticator changed; restart setup", http.StatusConflict)
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
	userID := r.Context().Value(ctxUserID).(string)

	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	secret, err := storage.GetPendingMFASecret(r.Context(), ro.pool, userID)
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

	confirmed, err := storage.ConfirmMFA(r.Context(), ro.pool, userID, *secret)
	if err != nil {
		slog.Error("enable mfa", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if !confirmed {
		http.Error(w, "authenticator setup changed or expired; restart setup", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleMFADisable disables MFA after verifying the user's password.
func (ro *router) handleMFADisable(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(ctxUserID).(string)

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
// Accepts the password-login mfa_token or the HttpOnly OAuth challenge cookie.
func (ro *router) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// OAuth challenges are carried in an HttpOnly cookie, never a redirect URL.
	if req.MFAToken == "" {
		if cookie, err := r.Cookie("tindra_mfa"); err == nil {
			req.MFAToken = cookie.Value
		}
	}
	if req.MFAToken == "" {
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

	// Commit challenge consumption and session creation together with reset protection.
	session, err := storage.CompleteMFALogin(r.Context(), ro.pool, req.MFAToken, userID, *secret)
	if errors.Is(err, storage.ErrAuthenticationChanged) {
		http.Error(w, "invalid or expired MFA token", http.StatusUnauthorized)
		return
	}
	if err != nil {
		slog.Error("complete mfa login", "err", err)
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
	clearMFAChallengeCookie(w, ro.cookieSecure)
	w.WriteHeader(http.StatusOK)
}

func clearMFAChallengeCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: "tindra_mfa", Value: "", Path: "/api/auth/mfa/verify",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}
