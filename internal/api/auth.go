package api

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/blendbyte/tindra/internal/storage"
)

func (ro *router) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" || req.Password == "" {
		http.Error(w, "email and password are required", http.StatusBadRequest)
		return
	}

	// Per-(IP, account) limit: keying by IP+email prevents a distributed attacker
	// from locking out a target account while still rate-limiting brute-force
	// attempts against any one account from a single IP.
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if !ro.loginEmailRL.allow(ip + ":" + strings.ToLower(req.Email)) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	if len(ro.oauthProviders) > 0 {
		http.Error(w, "password login disabled", http.StatusForbidden)
		return
	}

	user, err := storage.AuthenticateUser(r.Context(), ro.pool, req.Email, req.Password)
	if err != nil {
		slog.Error("authenticate user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		storage.WriteAuditLog(ro.pool, storage.AuditEntry{
			EventType: "auth.login.fail",
			IP:        r.RemoteAddr,
			Details:   map[string]any{"email": req.Email},
		})
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	// MFA step: issue a short-lived challenge token instead of a full session.
	if user.MFAEnabled {
		mfaToken, err := storage.CreateMFAChallenge(r.Context(), ro.pool, user.ID)
		if err != nil {
			slog.Error("create mfa challenge", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"mfa_required": true,
			"mfa_token":    mfaToken,
		})
		return
	}

	session, err := storage.CreateSession(r.Context(), ro.pool, user.ID)
	if err != nil {
		slog.Error("create session", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "auth.login.success",
		ActorID:   &user.ID,
		IP:        r.RemoteAddr,
	})

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

func (ro *router) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("tindra_session")
	if err == nil {
		userID, _ := storage.DeleteSessionReturningUserID(r.Context(), ro.pool, cookie.Value)
		if userID != "" {
			storage.WriteAuditLog(ro.pool, storage.AuditEntry{
				EventType: "auth.logout",
				ActorID:   &userID,
				IP:        r.RemoteAddr,
			})
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "tindra_session",
		Value:    "",
		MaxAge:   -1,
		Path:     "/",
		HttpOnly: true,
	})
	w.WriteHeader(http.StatusOK)
}
