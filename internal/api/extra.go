package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pquerna/otp/totp"

	"github.com/blendbyte/tindra/internal/storage"
)

// handleGetMe returns the current authenticated user.
func (ro *router) handleGetMe(w http.ResponseWriter, r *http.Request) {
	actorID := actorFromContext(r.Context())
	if actorID == nil {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	u, err := storage.GetUserByID(r.Context(), ro.pool, *actorID)
	if err != nil {
		slog.Error("get me", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, u)
}

// handleUpdateMe updates the current user's profile (name, email, weekly_digest).
func (ro *router) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	actorID := actorFromContext(r.Context())
	if actorID == nil {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	var req struct {
		Name         string `json:"name"`
		Email        string `json:"email"`
		WeeklyDigest *bool  `json:"weekly_digest"`
		Timezone     string `json:"timezone"`
		// Required only when the email changes; see confirmEmailChange.
		CurrentPassword string `json:"current_password"`
		MFACode         string `json:"mfa_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if req.Email == "" {
		http.Error(w, "email is required", http.StatusBadRequest)
		return
	}
	current, err := storage.GetUserByID(r.Context(), ro.pool, *actorID)
	if err != nil {
		slog.Error("update me: get user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if current == nil {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	if req.Email != current.Email && !ro.confirmEmailChange(w, r, current, req.Email, req.CurrentPassword, req.MFACode) {
		return
	}
	tz := req.Timezone
	if tz == "" {
		tz = "UTC"
	}
	if _, err := time.LoadLocation(tz); err != nil {
		http.Error(w, "invalid timezone", http.StatusBadRequest)
		return
	}
	u, err := storage.UpdateUserProfile(r.Context(), ro.pool, *actorID, req.Name, req.Email, tz)
	if errors.Is(err, storage.ErrEmailTaken) {
		http.Error(w, "that email address is already in use", http.StatusConflict)
		return
	}
	if err != nil {
		slog.Error("update me", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if req.Email != current.Email {
		storage.WriteAuditLog(ro.pool, storage.AuditEntry{
			EventType: "user.email_changed",
			ActorID:   actorID,
			TargetID:  actorID,
			IP:        r.RemoteAddr,
			Details:   map[string]any{"from": current.Email, "to": req.Email},
		})
	}
	if req.WeeklyDigest != nil {
		if err := storage.UpdateUserWeeklyDigest(r.Context(), ro.pool, *actorID, *req.WeeklyDigest); err != nil {
			slog.Error("update weekly digest pref", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		u.WeeklyDigest = *req.WeeklyDigest
	}
	writeJSON(w, u)
}

// reauthenticate re-confirms the person holding the session before a
// sensitive account change. It checks the current password when password is
// non-empty and a current two-factor code when mfaCode is non-empty; callers
// decide which are required. Wrong answers count against the session, not the
// account: after storage.ReauthMaxFailures the session ends, so a stolen
// session cannot keep guessing and cannot lock the owner out of signing in. It
// returns the secret a code matched, so a caller can make its change conditional on that factor
// still being active. It writes the error response and returns false when the
// change must not proceed.
func (ro *router) reauthenticate(w http.ResponseWriter, r *http.Request, userID, password, mfaCode string) (mfaSecret *string, ok bool) {
	if password == "" && mfaCode == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return nil, false
	}
	cookie, err := r.Cookie("tindra_session")
	if err != nil {
		http.Error(w, "session required", http.StatusUnauthorized)
		return nil, false
	}
	ctx := r.Context()
	claimed, err := storage.BeginReauth(ctx, ro.pool, cookie.Value, userID)
	if err != nil {
		slog.Error("begin reauth", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	if !claimed {
		// Out of attempts, or ended meanwhile. Make sure it is gone.
		if _, err := storage.FinishReauth(ctx, ro.pool, cookie.Value, false); err != nil {
			slog.Error("end reauth session", "err", err)
		}
		ro.signOutAfterReauth(w)
		return nil, false
	}

	var failure string
	if password != "" {
		matched, err := storage.VerifyUserPassword(ctx, ro.pool, userID, password)
		if err != nil {
			slog.Error("verify password", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return nil, false
		}
		if !matched {
			failure = "the password you entered is incorrect"
		}
	}
	if failure == "" && mfaCode != "" {
		mfaSecret, err = storage.GetMFASecret(ctx, ro.pool, userID)
		if err != nil {
			slog.Error("get mfa secret", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return nil, false
		}
		if mfaSecret == nil || !totp.Validate(mfaCode, *mfaSecret) {
			failure = "the two-factor code you entered is incorrect"
		}
	}

	ended, err := storage.FinishReauth(ctx, ro.pool, cookie.Value, failure == "")
	if err != nil {
		slog.Error("finish reauth", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	if ended {
		storage.WriteAuditLog(ro.pool, storage.AuditEntry{
			EventType: "auth.reauth_session_ended",
			ActorID:   &userID,
			IP:        r.RemoteAddr,
		})
		ro.signOutAfterReauth(w)
		return nil, false
	}
	if failure != "" {
		// 403, not 401: the web client treats 401 as an ended session.
		http.Error(w, failure, http.StatusForbidden)
		return nil, false
	}
	return mfaSecret, true
}

// signOutAfterReauth answers a request whose session ended after too many
// wrong re-confirmations.
func (ro *router) signOutAfterReauth(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "tindra_session", Value: "", MaxAge: -1, Path: "/",
		HttpOnly: true, Secure: ro.cookieSecure, SameSite: http.SameSiteStrictMode})
	http.Error(w, "too many wrong attempts; you have been signed out", http.StatusUnauthorized)
}

// confirmEmailChange re-authenticates the user before their email changes, so a
// hijacked session cannot redirect password resets to an attacker's address.
// Users confirm with their password and, when two-factor is on, a current
// code; users with neither must ask an admin. It writes the error response and
// returns false when the change must not proceed.
func (ro *router) confirmEmailChange(w http.ResponseWriter, r *http.Request, user *storage.User, newEmail, password, mfaCode string) bool {
	if !user.HasPassword && !user.MFAEnabled {
		http.Error(w, "your account has no password or two-factor code to confirm this change; ask an admin to update your email", http.StatusForbidden)
		return false
	}
	if user.HasPassword && password == "" {
		http.Error(w, "enter your current password to change your email", http.StatusBadRequest)
		return false
	}
	if user.MFAEnabled && mfaCode == "" {
		http.Error(w, "enter a two-factor code to change your email", http.StatusBadRequest)
		return false
	}
	if !user.HasPassword {
		password = ""
	}
	if !user.MFAEnabled {
		mfaCode = ""
	}
	if _, ok := ro.reauthenticate(w, r, user.ID, password, mfaCode); !ok {
		return false
	}

	pending, err := storage.HasPendingInvite(r.Context(), ro.pool, newEmail)
	if err != nil {
		slog.Error("check pending invite for email change", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return false
	}
	if pending {
		http.Error(w, "that email address has a pending invite", http.StatusConflict)
		return false
	}
	return true
}

// lastUserManagerMessage is shown as is in the Users settings.
const lastUserManagerMessage = "At least one user must keep the Users permission."

// handleUpdateUserPermissions replaces the named permission set for a user.
// Requires manage_users permission (enforced by router middleware).
func (ro *router) handleUpdateUserPermissions(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	var perms storage.UserPermissions
	if err := json.NewDecoder(r.Body).Decode(&perms); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	u, err := storage.UpdateUserPermissions(r.Context(), ro.pool, userID, perms)
	if errors.Is(err, storage.ErrLastUserManager) {
		http.Error(w, lastUserManagerMessage, http.StatusConflict)
		return
	}
	if err != nil {
		slog.Error("update user permissions", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "user.permissions_changed",
		ActorID:   actorFromContext(r.Context()),
		TargetID:  &userID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"permissions": perms},
	})
	writeJSON(w, u)
}

// handleDeleteUser removes a user. Cannot delete yourself.
func (ro *router) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	actor := actorFromContext(r.Context())
	if actor != nil && *actor == userID {
		http.Error(w, "cannot delete your own account", http.StatusBadRequest)
		return
	}
	found, err := storage.DeleteUser(r.Context(), ro.pool, userID)
	if errors.Is(err, storage.ErrLastUserManager) {
		http.Error(w, lastUserManagerMessage, http.StatusConflict)
		return
	}
	if err != nil {
		slog.Error("delete user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if actor != nil {
		storage.WriteAuditLog(ro.pool, storage.AuditEntry{
			EventType: "auth.user.deleted",
			ActorID:   actor,
			TargetID:  &userID,
			IP:        r.RemoteAddr,
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleChangePassword changes the authenticated user's password.
// Requires session auth (not Bearer token). Only available to password-based accounts.
func (ro *router) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(ctxUserID).(string)
	if !ok || userID == "" {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
		// The push endpoint of the browser making the change, which keeps
		// its notifications while every other device loses them.
		KeepPushEndpoint string `json:"keep_push_endpoint"`
		// Required when two-factor is on, so a guessed password alone cannot
		// mint fresh sessions and sign the owner out.
		MFACode string `json:"mfa_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CurrentPassword == "" || req.NewPassword == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie("tindra_session")
	if err != nil {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	user, err := storage.GetUserByID(r.Context(), ro.pool, userID)
	if err != nil {
		slog.Error("change password: get user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	if user.MFAEnabled && req.MFACode == "" {
		http.Error(w, "enter a two-factor code to change your password", http.StatusBadRequest)
		return
	}
	if !user.MFAEnabled {
		req.MFACode = ""
	}
	if _, ok := ro.reauthenticate(w, r, userID, req.CurrentPassword, req.MFACode); !ok {
		return
	}
	// The change checks the password again under its row lock, in case it
	// changed since.
	session, err := storage.ChangeUserPasswordWithSession(r.Context(), ro.pool, userID, req.CurrentPassword, req.NewPassword, cookie.Value, req.KeepPushEndpoint)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidPassword) {
			http.Error(w, "current password is incorrect", http.StatusForbidden)
			return
		}
		slog.Error("change password", "err", err)
		http.Error(w, "unable to change password", http.StatusBadRequest)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "auth.password_changed",
		ActorID:   &userID,
		IP:        r.RemoteAddr,
	})
	http.SetCookie(w, &http.Cookie{Name: "tindra_session", Value: session.Token, Path: "/", HttpOnly: true, Secure: ro.cookieSecure, SameSite: http.SameSiteStrictMode, Expires: session.ExpiresAt})
	clearMFAChallengeCookie(w, ro.cookieSecure)
	w.WriteHeader(http.StatusOK)
}

// handleListUsers returns all users (for assignee picker and team management).
func (ro *router) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if actorFromContext(r.Context()) == nil {
		http.Error(w, "session required", http.StatusForbidden)
		return
	}
	users, err := storage.ListUsers(r.Context(), ro.pool)
	if err != nil {
		slog.Error("list users", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if users == nil {
		users = []*storage.User{}
	}
	writeJSON(w, users)
}

// handleListComments returns comments for an issue.
func (ro *router) handleListComments(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "issueID")
	if !ro.enforceIssueProject(w, r, issueID) {
		return
	}
	comments, err := storage.ListComments(r.Context(), ro.pool, issueID)
	if err != nil {
		slog.Error("list comments", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if comments == nil {
		comments = []*storage.Comment{}
	}
	writeJSON(w, comments)
}

// handleCreateComment posts a new comment on an issue.
func (ro *router) handleCreateComment(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "issueID")
	actorID := actorFromContext(r.Context())
	if actorID == nil {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Body == "" {
		http.Error(w, "body is required", http.StatusBadRequest)
		return
	}
	comment, err := storage.CreateComment(r.Context(), ro.pool, issueID, *actorID, req.Body)
	if err != nil {
		slog.Error("create comment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSONStatus(w, http.StatusCreated, comment)
}

// handleUpdateComment edits a comment body. Only the author may edit.
func (ro *router) handleUpdateComment(w http.ResponseWriter, r *http.Request) {
	commentID := chi.URLParam(r, "commentID")
	actorID := actorFromContext(r.Context())
	if actorID == nil {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	comment, err := storage.GetComment(r.Context(), ro.pool, commentID)
	if err != nil {
		slog.Error("get comment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if comment == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if comment.UserID != *actorID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var req struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Body == "" {
		http.Error(w, "body is required", http.StatusBadRequest)
		return
	}
	updated, err := storage.UpdateComment(r.Context(), ro.pool, commentID, req.Body)
	if err != nil {
		slog.Error("update comment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, updated)
}

// handleDeleteComment removes a comment. Authors may delete their own; manage_issues permission allows deleting any.
func (ro *router) handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	commentID := chi.URLParam(r, "commentID")
	actorID := actorFromContext(r.Context())
	if actorID == nil {
		http.Error(w, "session required", http.StatusUnauthorized)
		return
	}
	comment, err := storage.GetComment(r.Context(), ro.pool, commentID)
	if err != nil {
		slog.Error("get comment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if comment == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Allow if author, or if actor has manage_issues permission.
	if comment.UserID != *actorID {
		actor, _ := storage.GetUserByID(r.Context(), ro.pool, *actorID)
		if actor == nil || !actor.Permissions.ManageIssues {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}
	if _, err := storage.DeleteComment(r.Context(), ro.pool, commentID); err != nil {
		slog.Error("delete comment", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListReleaseMetadata supplies the same first-page identities for pickers.
func (ro *router) handleListReleaseMetadata(w http.ResponseWriter, r *http.Request) {
	releases, err := storage.ListReleaseMetadata(r.Context(), ro.pool, storage.ReleaseFilter{
		ProjectIDs: bearerProjectIDs(r, r.URL.Query()["project_id"]),
		Limit:      50,
	})
	if err != nil {
		slog.Error("list release metadata", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"releases": releases})
}

func (ro *router) handleRecentReleaseHealth(w http.ResponseWriter, r *http.Request) {
	releases, err := storage.ListRecentReleaseHealth(r.Context(), ro.pool, bearerProjectIDs(r, r.URL.Query()["project_id"]))
	if err != nil {
		slog.Error("release health", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"releases": releases})
}

// handleListReleases returns a paginated release list filtered by project.
func (ro *router) handleListReleases(w http.ResponseWriter, r *http.Request) {
	const pageSize = 50

	filter := storage.ReleaseFilter{
		ProjectIDs: bearerProjectIDs(r, r.URL.Query()["project_id"]),
		Limit:      pageSize,
	}
	if ct := r.URL.Query().Get("cursor_time"); ct != "" {
		if cid := r.URL.Query().Get("cursor_id"); cid != "" {
			t, err := time.Parse(time.RFC3339Nano, ct)
			if err == nil {
				filter.CursorTime = &t
				filter.CursorID = &cid
			}
		}
	}

	total, err := storage.CountReleases(r.Context(), ro.pool, filter)
	if err != nil {
		slog.Error("count releases", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	releases, err := storage.ListReleases(r.Context(), ro.pool, filter)
	if err != nil {
		slog.Error("list releases", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if releases == nil {
		releases = []*storage.Release{}
	}

	resp := map[string]any{
		"releases": releases,
		"total":    total,
		"has_more": len(releases) == pageSize,
	}
	if len(releases) == pageSize {
		last := releases[len(releases)-1]
		resp["next_cursor_time"] = last.DeployedAt.UTC().Format(time.RFC3339Nano)
		resp["next_cursor_id"] = last.ID
	}

	writeJSON(w, resp)
}

// handleGetRelease returns a single release by ID.
func (ro *router) handleGetRelease(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "releaseID")
	rel, err := storage.GetRelease(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get release", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rel == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceTokenProject(w, r, rel.ProjectID) {
		return
	}
	writeJSON(w, rel)
}

// handleGetReleaseTransactions returns per-transaction performance summaries for a release.
func (ro *router) handleGetReleaseTransactions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "releaseID")
	if tokenProjID, ok := r.Context().Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		rel, err := storage.GetRelease(r.Context(), ro.pool, id)
		if err != nil {
			slog.Error("get release for scope check", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if rel == nil || rel.ProjectID != tokenProjID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}
	txns, err := storage.GetReleaseTransactions(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get release transactions", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if txns == nil {
		txns = []*storage.ReleaseTxSummary{}
	}
	writeJSON(w, txns)
}

// handleGetReleaseIssues returns issues that have events tagged with this release.
func (ro *router) handleGetReleaseIssues(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "releaseID")
	if tokenProjID, ok := r.Context().Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		rel, err := storage.GetRelease(r.Context(), ro.pool, id)
		if err != nil {
			slog.Error("get release for scope check", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if rel == nil || rel.ProjectID != tokenProjID {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}
	issues, err := storage.GetReleaseIssues(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get release issues", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issues == nil {
		issues = []*storage.ReleaseIssue{}
	}
	writeJSON(w, issues)
}

// handleListAuditLog returns paginated audit log entries.
func (ro *router) handleListAuditLog(w http.ResponseWriter, r *http.Request) {
	filter := storage.AuditFilter{
		Kind:   r.URL.Query().Get("kind"),
		Search: truncSearch(r.URL.Query().Get("q")),
	}
	rows, err := storage.ListAuditLog(r.Context(), ro.pool, filter)
	if err != nil {
		slog.Error("list audit log", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == nil {
		rows = []*storage.AuditRow{}
	}
	writeJSON(w, rows)
}
