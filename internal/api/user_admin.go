package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/storage"
)

// handleAdminDisableMFA removes MFA from any user. Requires manage_users.
func (ro *router) handleAdminDisableMFA(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	u, err := storage.GetUserByID(r.Context(), ro.pool, userID)
	if err != nil {
		slog.Error("get user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := storage.DisableMFA(r.Context(), ro.pool, userID); err != nil {
		slog.Error("admin disable mfa", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	actor := actorFromContext(r.Context())
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "auth.mfa.admin_disabled",
		ActorID:   actor,
		TargetID:  &userID,
		IP:        r.RemoteAddr,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminSetPassword sets any user's password directly. Requires manage_users.
func (ro *router) handleAdminSetPassword(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		http.Error(w, "password is required", http.StatusBadRequest)
		return
	}
	if err := storage.AdminSetPassword(r.Context(), ro.pool, userID, req.Password); err != nil {
		if strings.Contains(err.Error(), "user not found") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	actor := actorFromContext(r.Context())
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "auth.password.admin_set",
		ActorID:   actor,
		TargetID:  &userID,
		IP:        r.RemoteAddr,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminSendPasswordReset emails a password reset link to a user. Requires manage_users.
func (ro *router) handleAdminSendPasswordReset(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	u, err := storage.GetUserByID(r.Context(), ro.pool, userID)
	if err != nil {
		slog.Error("get user for password reset", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	token, err := storage.CreatePasswordResetToken(r.Context(), ro.pool, userID)
	if err != nil {
		slog.Error("create password reset token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resetURL := fmt.Sprintf("%s/reset-password/%s", strings.TrimRight(ro.publicURL, "/"), token)

	resp := map[string]any{"reset_url": resetURL, "email_sent": false}

	if AppEmailSender != nil {
		if err := sendPasswordResetEmail(r.Context(), AppEmailSender, u.Email, resetURL, ro.publicURL); err != nil {
			slog.Error("send password reset email", "err", err, "to", u.Email)
			resp["email_error"] = err.Error()
		} else {
			resp["email_sent"] = true
		}
	}

	actor := actorFromContext(r.Context())
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "auth.password.reset_requested",
		ActorID:   actor,
		TargetID:  &userID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"email": u.Email},
	})

	writeJSON(w, resp)
}

// handleGetPasswordReset validates a reset token and returns the email. Public endpoint.
func (ro *router) handleGetPasswordReset(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	u, err := storage.GetPasswordResetUser(r.Context(), ro.pool, token)
	if err != nil {
		slog.Error("get password reset user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u == nil {
		http.Error(w, "reset link not found or expired", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{"email": u.Email})
}

// handleDoPasswordReset redeems the token, sets the new password, and opens a session. Public endpoint.
func (ro *router) handleDoPasswordReset(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		http.Error(w, "password is required", http.StatusBadRequest)
		return
	}

	u, err := storage.UsePasswordResetToken(r.Context(), ro.pool, token, req.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if u == nil {
		http.Error(w, "reset link not found or expired", http.StatusNotFound)
		return
	}

	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "auth.password.reset_completed",
		ActorID:   &u.ID,
		IP:        r.RemoteAddr,
	})

	// MFA step: issue a short-lived challenge instead of a full session.
	if u.MFAEnabled {
		mfaToken, err := storage.CreateMFAChallenge(r.Context(), ro.pool, u.ID)
		if err != nil {
			slog.Error("create mfa challenge after password reset", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{
			"mfa_required": true,
			"mfa_token":    mfaToken,
		})
		return
	}

	session, err := storage.CreateSession(r.Context(), ro.pool, u.ID)
	if err != nil {
		slog.Error("create session after password reset", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "tindra_session",
		Value:    session.Token,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
		Expires:  session.ExpiresAt,
		Secure:   ro.cookieSecure,
	})
	writeJSON(w, u)
}

func sendPasswordResetEmail(ctx context.Context, sender alerts.EmailSender, to, resetURL, publicURL string) error {
	html, text, err := alerts.RenderPasswordResetEmail(resetURL, publicURL)
	if err != nil {
		return fmt.Errorf("render email: %w", err)
	}
	return sender.Send(ctx, alerts.EmailMessage{
		To:      to,
		Subject: "Reset your Tindra password",
		Text:    text,
		HTML:    html,
	})
}
