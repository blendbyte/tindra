package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

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

// tokenScope returns the bearer token's project as a one-element list, or nil for
// session callers. Pass it to storage lookups that would otherwise follow a
// trace_id across project boundaries.
func tokenScope(ctx context.Context) []string {
	if projID, ok := ctx.Value(ctxTokenProjID).(string); ok && projID != "" {
		return []string{projID}
	}
	return nil
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
	issue, err := storage.GetIssueMetadata(r.Context(), ro.pool, issueID)
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

// requireUUIDParams responds 404 when a named URL parameter is not a UUID, so
// malformed IDs never reach rate limiters or the database (where they would
// surface as 500s).
func requireUUIDParams(names ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for _, name := range names {
				if uuid.Validate(chi.URLParam(r, name)) != nil {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// maxIssueIDsPerRequest bounds bulk issue operations so one request cannot
// fan out into an unbounded number of queries and history rows.
const maxIssueIDsPerRequest = 1000

// uniqueStrings returns ids without duplicates, keeping first occurrences in order.
func uniqueStrings(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := ids[:0:0]
	for _, id := range ids {
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}
