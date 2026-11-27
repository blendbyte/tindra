package api

import (
	"context"
	"log/slog"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/blendbyte/tindra/internal/storage"
)

// Project scope. requireAuth puts ONE value in the request context (ctxProjectAccess,
// a storage.ProjectAccess): the token's own project for a bearer token, the projects
// of the user's teams for a session user who only sees their teams' projects, and
// "all" for everyone else. Every read of project data goes through a helper in this
// file that consults that value, so a path hardened for tokens is hardened for
// restricted users too. The route-coverage test in route_scope_test.go fails for any
// route that is not classified, which keeps a new endpoint from skipping this.
//
// noProjects is a project id that matches nothing. An empty or nil project list
// means "all projects" almost everywhere in storage, so a scope that selects no
// project must be expressed with this value instead of an empty slice.
const noProjects = "00000000-0000-0000-0000-000000000000"

// accessFromContext returns the projects the caller may see. Requests that carry
// none (unauthenticated callers) are not restricted by it.
func accessFromContext(ctx context.Context) storage.ProjectAccess {
	if a, ok := ctx.Value(ctxProjectAccess).(storage.ProjectAccess); ok {
		return a
	}
	return storage.ProjectAccess{All: true}
}

// projectAllowed reports whether the caller may read the project.
func projectAllowed(ctx context.Context, projectID string) bool {
	return accessFromContext(ctx).Allows(projectID)
}

// scopedProjectIDs enforces project scope on list endpoints.
// A project-scoped bearer token overrides any caller-supplied project_id params
// with its own project (clients rely on that). A user who cannot see every project
// gets the intersection of the requested ids and the projects they can see, or all
// of those projects when none were requested. Otherwise ids is returned unchanged.
// When nothing remains it returns [noProjects], never an empty slice.
func scopedProjectIDs(r *http.Request, ids []string) []string {
	if projID, ok := r.Context().Value(ctxTokenProjID).(string); ok && projID != "" {
		return []string{projID}
	}
	return restrictProjectIDs(accessFromContext(r.Context()), ids)
}

// restrictProjectIDs limits a requested project list to the projects in access:
// the intersection with ids, or all of them when ids is empty. Callers who can
// see every project get ids back unchanged.
func restrictProjectIDs(access storage.ProjectAccess, ids []string) []string {
	if access.All {
		return ids
	}
	if len(ids) == 0 {
		return allowedOrNone(access.IDs)
	}
	allowed := make(map[string]struct{}, len(access.IDs))
	for _, id := range access.IDs {
		allowed[id] = struct{}{}
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, ok := allowed[id]; ok {
			out = append(out, id)
		}
	}
	return allowedOrNone(out)
}

func allowedOrNone(ids []string) []string {
	if len(ids) == 0 {
		return []string{noProjects}
	}
	return slices.Clone(ids)
}

// projectScope returns the projects the caller is limited to, or nil when they are
// not limited. Pass it to storage lookups that would otherwise follow a trace_id
// across project boundaries.
func projectScope(ctx context.Context) []string {
	access := accessFromContext(ctx)
	if access.All {
		return nil
	}
	return allowedOrNone(access.IDs)
}

// enforceProjectAccess returns false and writes a 404 if the caller may not read
// projID: a bearer token scoped to another project, or a session user who cannot
// see it. Use after fetching a resource by ID to prevent cross-project reads
// without revealing resource existence.
func enforceProjectAccess(w http.ResponseWriter, r *http.Request, projID string) bool {
	if !projectAllowed(r.Context(), projID) {
		http.Error(w, "not found", http.StatusNotFound)
		return false
	}
	return true
}

// enforceIssueProject verifies that the caller may read the project that owns
// the given issue. Used by sub-resource handlers (history, tags, events, etc.)
// that receive only an issueID and do not otherwise fetch the issue.
func (ro *router) enforceIssueProject(w http.ResponseWriter, r *http.Request, issueID string) bool {
	if accessFromContext(r.Context()).All {
		return true
	}
	issue, err := storage.GetIssueMetadata(r.Context(), ro.pool, issueID)
	if err != nil {
		slog.Error("get issue for project scope check", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return false
	}
	if issue == nil || !projectAllowed(r.Context(), issue.ProjectID) {
		http.Error(w, "not found", http.StatusNotFound)
		return false
	}
	return true
}

// enforceReleaseProject verifies that the caller may read the project that owns
// the given release. Used by sub-resource handlers that receive only a releaseID.
func (ro *router) enforceReleaseProject(w http.ResponseWriter, r *http.Request, releaseID string) bool {
	if accessFromContext(r.Context()).All {
		return true
	}
	rel, err := storage.GetRelease(r.Context(), ro.pool, releaseID)
	if err != nil {
		slog.Error("get release for scope check", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return false
	}
	if rel == nil || !projectAllowed(r.Context(), rel.ProjectID) {
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
