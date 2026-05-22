package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Email == "" {
		http.Error(w, "email is required", http.StatusBadRequest)
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
	if err != nil {
		slog.Error("update me", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
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
	if err != nil {
		slog.Error("update user permissions", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CurrentPassword == "" || req.NewPassword == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := storage.ChangeUserPassword(r.Context(), ro.pool, userID, req.CurrentPassword, req.NewPassword); err != nil {
		if errors.Is(err, storage.ErrInvalidPassword) {
			http.Error(w, "current password is incorrect", http.StatusUnauthorized)
			return
		}
		slog.Error("change password", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "auth.password_changed",
		ActorID:   &userID,
		IP:        r.RemoteAddr,
	})
	w.WriteHeader(http.StatusOK)
}

// handleListUsers returns all users (for assignee picker and team management).
func (ro *router) handleListUsers(w http.ResponseWriter, r *http.Request) {
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
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, comment)
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
