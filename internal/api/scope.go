package api

import (
	"log/slog"
	"net/http"

	"github.com/blendbyte/tindra/internal/storage"
)

// bearerProjectIDs enforces project scope on list endpoints for bearer-token requests.
// When a project-scoped token is present its project overrides any caller-supplied
// project_id params. For session auth the ids slice is returned unchanged.
func bearerProjectIDs(r *http.Request, ids []string) []string {
	if projID, ok := r.Context().Value(ctxTokenProjID).(string); ok && projID != "" {
		return []string{projID}
	}
	return ids
}

// enforceTokenProject returns false and writes a 404 if the request carries a bearer
// token whose project differs from projID. Use after fetching a resource by ID to
// prevent cross-project reads without revealing resource existence.
func enforceTokenProject(w http.ResponseWriter, r *http.Request, projID string) bool {
	if tokenProjID, ok := r.Context().Value(ctxTokenProjID).(string); ok && tokenProjID != "" && projID != tokenProjID {
		http.Error(w, "not found", http.StatusNotFound)
		return false
	}
	return true
}

// enforceIssueProject verifies that a bearer token, if present, is scoped to the
// project that owns the given issue. Used by sub-resource handlers (history, tags,
// events, etc.) that receive only an issueID and do not otherwise fetch the issue.
func (ro *router) enforceIssueProject(w http.ResponseWriter, r *http.Request, issueID string) bool {
	tokenProjID, isBearer := r.Context().Value(ctxTokenProjID).(string)
	if !isBearer || tokenProjID == "" {
		return true
	}
	issue, err := storage.GetIssue(r.Context(), ro.pool, issueID)
	if err != nil {
		slog.Error("get issue for project scope check", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return false
	}
	if issue == nil || issue.ProjectID != tokenProjID {
		http.Error(w, "not found", http.StatusNotFound)
		return false
	}
	return true
}
