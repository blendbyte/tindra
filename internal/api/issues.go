package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
)

func attachSparklines(ctx context.Context, pool *pgxpool.Pool, issues []*storage.Issue) {
	if len(issues) == 0 {
		return
	}
	ids := make([]string, len(issues))
	for i, iss := range issues {
		ids[i] = iss.ID
	}
	sparklines, err := storage.GetIssueSparklines(ctx, pool, ids)
	if err != nil {
		slog.Warn("get sparklines", "err", err)
		return
	}
	for _, iss := range issues {
		if s, ok := sparklines[iss.ID]; ok {
			iss.Sparkline = s
		}
	}
}

type listIssuesResponse struct {
	Issues         []*storage.Issue `json:"issues"`
	NextCursorTime *time.Time       `json:"next_cursor_time,omitempty"`
	NextCursorID   *string          `json:"next_cursor_id,omitempty"`
}

func (ro *router) handleListIssues(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}

	filter := storage.IssueFilter{
		Status: r.URL.Query().Get("status"),
		Level:  r.URL.Query().Get("level"),
		Kind:   r.URL.Query().Get("kind"),
		Limit:  50,
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

	issues, err := storage.ListIssues(r.Context(), ro.pool, project.ID, filter)
	if err != nil {
		slog.Error("list issues", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	attachSparklines(r.Context(), ro.pool, issues)

	resp := listIssuesResponse{Issues: issues}
	if len(issues) == filter.Limit {
		last := issues[len(issues)-1]
		resp.NextCursorTime = &last.LastSeen
		resp.NextCursorID = &last.ID
	}

	writeJSON(w, resp)
}

func (ro *router) handleGetIssue(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "issueID")
	issue, err := storage.GetIssue(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get issue", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issue == nil || issue.ProjectID != project.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	writeJSON(w, issue)
}

func (ro *router) handleUpdateIssue(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "issueID")

	var req struct {
		Status           string     `json:"status"`
		IgnoreUntil      *time.Time `json:"ignore_until"`
		IgnoreCountLimit *int       `json:"ignore_count_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	existing, err := storage.GetIssue(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get issue for update", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var ignoreOpts *storage.IgnoreOptions
	if req.Status == "ignored" && (req.IgnoreUntil != nil || req.IgnoreCountLimit != nil) {
		ignoreOpts = &storage.IgnoreOptions{Until: req.IgnoreUntil, CountLimit: req.IgnoreCountLimit}
	}

	issue, err := storage.UpdateIssueStatus(r.Context(), ro.pool, project.ID, id, req.Status, ignoreOpts)
	if err != nil {
		slog.Error("update issue", "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if issue == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	actor := actorFromContext(r.Context())
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "issue.status_changed",
		ActorID:   actor,
		ProjectID: &project.ID,
		TargetID:  &id,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"status": req.Status},
	})
	historyDetails := map[string]any{"from": existing.Status, "to": req.Status}
	if req.Status == "ignored" {
		if req.IgnoreUntil != nil {
			historyDetails["ignore_until"] = req.IgnoreUntil.Format(time.RFC3339)
		}
		if req.IgnoreCountLimit != nil {
			historyDetails["ignore_count_limit"] = *req.IgnoreCountLimit
		}
	}
	if err := storage.InsertIssueHistory(r.Context(), ro.pool, storage.IssueHistoryEntry{
		IssueID:   id,
		ActorID:   actor,
		EventType: "status_changed",
		Details:   historyDetails,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		slog.Error("insert issue history", "err", err)
	}

	writeJSON(w, issue)
}

func (ro *router) handleGetIssueFingerprints(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}
	issueID := chi.URLParam(r, "issueID")
	issue, err := storage.GetIssue(r.Context(), ro.pool, issueID)
	if err != nil {
		slog.Error("get issue", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issue == nil || issue.ProjectID != project.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	fps, err := storage.GetIssueFingerprints(r.Context(), ro.pool, issueID)
	if err != nil {
		slog.Error("get fingerprints", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"fingerprints": fps})
}

func (ro *router) handleMergeIssues(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}

	var req struct {
		IssueIDs []string `json:"issue_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IssueIDs) < 2 {
		http.Error(w, "issue_ids must contain at least 2 IDs", http.StatusBadRequest)
		return
	}

	// Validate all issues belong to this project.
	for _, id := range req.IssueIDs {
		iss, err := storage.GetIssue(r.Context(), ro.pool, id)
		if err != nil {
			slog.Error("get issue for merge", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if iss == nil || iss.ProjectID != project.ID {
			http.Error(w, "not found: "+id, http.StatusNotFound)
			return
		}
	}

	primaryID := req.IssueIDs[0]
	mergeIDs := req.IssueIDs[1:]

	merged, err := storage.MergeIssues(r.Context(), ro.pool, primaryID, mergeIDs)
	if err != nil {
		slog.Error("merge issues", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	actor := actorFromContext(r.Context())
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "issue.merged",
		ActorID:   actor,
		ProjectID: &project.ID,
		TargetID:  &primaryID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"merged_ids": mergeIDs},
	})
	now := time.Now().UTC()
	if err := storage.InsertIssueHistory(r.Context(), ro.pool, storage.IssueHistoryEntry{
		IssueID:   primaryID,
		ActorID:   actor,
		EventType: "merged_into",
		Details:   map[string]any{"merged_ids": mergeIDs},
		CreatedAt: now,
	}); err != nil {
		slog.Error("insert issue history (merge primary)", "err", err)
	}
	for _, mid := range mergeIDs {
		if err := storage.InsertIssueHistory(r.Context(), ro.pool, storage.IssueHistoryEntry{
			IssueID:   mid,
			ActorID:   actor,
			EventType: "merged",
			Details:   map[string]any{"into": primaryID},
			CreatedAt: now,
		}); err != nil {
			slog.Error("insert issue history (merge source)", "err", err, "issue_id", mid)
		}
	}

	writeJSON(w, merged)
}

func (ro *router) handleUnmergeIssue(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}
	issueID := chi.URLParam(r, "issueID")

	issue, err := storage.GetIssue(r.Context(), ro.pool, issueID)
	if err != nil {
		slog.Error("get issue", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issue == nil || issue.ProjectID != project.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var req struct {
		Fingerprints []string `json:"fingerprints"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Fingerprints) == 0 {
		http.Error(w, "fingerprints must be a non-empty array", http.StatusBadRequest)
		return
	}

	newIssues, err := storage.UnmergeFingerprints(r.Context(), ro.pool, issueID, req.Fingerprints)
	if err != nil {
		slog.Error("unmerge fingerprints", "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	actor := actorFromContext(r.Context())
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "issue.unmerged",
		ActorID:   actor,
		ProjectID: &project.ID,
		TargetID:  &issueID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"fingerprints": req.Fingerprints},
	})
	now := time.Now().UTC()
	if err := storage.InsertIssueHistory(r.Context(), ro.pool, storage.IssueHistoryEntry{
		IssueID:   issueID,
		ActorID:   actor,
		EventType: "unmerged",
		Details:   map[string]any{"fingerprints": req.Fingerprints},
		CreatedAt: now,
	}); err != nil {
		slog.Error("insert issue history (unmerge source)", "err", err)
	}
	for _, ni := range newIssues {
		if err := storage.InsertIssueHistory(r.Context(), ro.pool, storage.IssueHistoryEntry{
			IssueID:   ni.ID,
			ActorID:   actor,
			EventType: "unmerged_from",
			Details:   map[string]any{"from": issueID},
			CreatedAt: now,
		}); err != nil {
			slog.Error("insert issue history (unmerge new)", "err", err, "issue_id", ni.ID)
		}
	}

	writeJSON(w, map[string]any{"issues": newIssues})
}

// projectFromSlug looks up a project by the {projectSlug} URL param.
// When the request was authenticated via Bearer token, the token's project must
// match - prevents a token for project A from reading project B.
// Writes the appropriate error response and returns false on failure.
func (ro *router) handleListPerfEvents(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "issueID")
	if !ro.enforceIssueProject(w, r, issueID) {
		return
	}
	events, err := storage.ListPerfEvents(r.Context(), ro.pool, issueID, 25)
	if err != nil {
		slog.Error("list perf events", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if events == nil {
		events = []*storage.PerfEvent{}
	}
	writeJSON(w, events)
}

func (ro *router) projectFromSlug(w http.ResponseWriter, r *http.Request) (*storage.Project, bool) {
	slug := chi.URLParam(r, "projectSlug")
	project, err := storage.GetProjectBySlug(r.Context(), ro.pool, slug)
	if err != nil {
		slog.Error("project lookup", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	if project == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return nil, false
	}
	if tokenProjID, ok := r.Context().Value(ctxTokenProjID).(string); ok {
		if tokenProjID != project.ID {
			http.Error(w, "forbidden", http.StatusForbidden)
			return nil, false
		}
	}
	return project, true
}
