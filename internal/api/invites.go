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

// AppEmailSender is set by main before the server starts. Nil means email is not configured.
var AppEmailSender alerts.EmailSender

func (ro *router) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Email == "" {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	if lim := ro.userLimit.Load(); lim > 0 {
		count, err := storage.CountUsers(r.Context(), ro.pool)
		if err != nil {
			slog.Error("count users", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if count >= int64(lim) {
			http.Error(w, "user limit reached", http.StatusTooManyRequests)
			return
		}
	}

	existing, err := storage.GetUserByEmail(r.Context(), ro.pool, req.Email)
	if err != nil {
		slog.Error("get user by email", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing != nil {
		http.Error(w, "a user with this email already exists", http.StatusConflict)
		return
	}

	actorID := actorFromContext(r.Context())
	inviterID := ""
	if actorID != nil {
		inviterID = *actorID
	}

	token, err := storage.CreateInvite(r.Context(), ro.pool, inviterID, req.Email, req.Name)
	if err != nil {
		slog.Error("create invite", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	inviteURL := fmt.Sprintf("%s/invite/%s", strings.TrimRight(ro.publicURL, "/"), token)
	emailConfigured := AppEmailSender != nil
	emailSent := false
	var emailError string
	if emailConfigured {
		if err := sendInviteEmail(r.Context(), AppEmailSender, req.Email, inviteURL, ro.publicURL); err != nil {
			slog.Error("send invite email", "err", err, "to", req.Email)
			emailError = err.Error()
		} else {
			emailSent = true
		}
	}

	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "auth.user.invited",
		ActorID:   actorID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"email": req.Email},
	})

	writeJSONStatus(w, http.StatusCreated, map[string]any{
		"invite_url":       inviteURL,
		"email_sent":       emailSent,
		"email_configured": emailConfigured,
		"email_error":      emailError,
	})
}

func (ro *router) handleListInvites(w http.ResponseWriter, r *http.Request) {
	invites, err := storage.ListPendingInvites(r.Context(), ro.pool)
	if err != nil {
		slog.Error("list invites", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if invites == nil {
		invites = []*storage.Invite{}
	}
	writeJSON(w, invites)
}

func (ro *router) handleRevokeInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	found, err := storage.DeleteInvite(r.Context(), ro.pool, token)
	if err != nil {
		slog.Error("delete invite", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "auth.invite.revoked",
		ActorID:   actorFromContext(r.Context()),
		IP:        r.RemoteAddr,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleGetInvite validates a token and returns the email - used by the accept page.
func (ro *router) handleGetInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	inv, err := storage.GetInvite(r.Context(), ro.pool, token)
	if err != nil {
		slog.Error("get invite", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if inv == nil {
		http.Error(w, "invite not found or expired", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]string{
		"email": inv.Email,
		"name":  inv.Name,
	})
}

// handleAcceptInvite creates the user account, marks the invite accepted, and opens a session.
func (ro *router) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	var req struct {
		Password string `json:"password"`
		Name     string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Password == "" {
		http.Error(w, "password is required", http.StatusBadRequest)
		return
	}

	inv, err := storage.GetInvite(r.Context(), ro.pool, token)
	if err != nil {
		slog.Error("get invite", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if inv == nil {
		http.Error(w, "invite not found or expired", http.StatusNotFound)
		return
	}

	if lim := ro.userLimit.Load(); lim > 0 {
		count, err := storage.CountUsers(r.Context(), ro.pool)
		if err != nil {
			slog.Error("count users", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if count >= int64(lim) {
			http.Error(w, "user limit reached", http.StatusTooManyRequests)
			return
		}
	}

	user, err := storage.CreateUser(r.Context(), ro.pool, inv.Email, req.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	name := req.Name
	if name == "" {
		name = inv.Name
	}
	if name != "" {
		if updated, err2 := storage.UpdateUserProfile(r.Context(), ro.pool, user.ID, name, user.Email, user.Timezone); err2 == nil && updated != nil {
			user = updated
		}
	}

	if err := storage.MarkInviteAccepted(r.Context(), ro.pool, token); err != nil {
		slog.Error("mark invite accepted", "err", err)
	}

	session, err := storage.CreateSession(r.Context(), ro.pool, user.ID)
	if err != nil {
		slog.Error("create session after invite accept", "err", err)
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
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
		Expires:  session.ExpiresAt,
		Secure:   ro.cookieSecure,
	})
	writeJSONStatus(w, http.StatusCreated, user)
}

func sendInviteEmail(ctx context.Context, sender alerts.EmailSender, to, inviteURL, publicURL string) error {
	html, text, err := alerts.RenderInviteEmail(inviteURL, publicURL)
	if err != nil {
		return fmt.Errorf("render email: %w", err)
	}
	return sender.Send(ctx, alerts.EmailMessage{
		To:      to,
		Subject: "You've been invited to Tindra",
		Text:    text,
		HTML:    html,
	})
}
