package api

import (
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

// truncSearch caps a free-text search parameter to prevent oversized LIKE patterns.
func truncSearch(s string) string {
	const max = 200
	if len(s) > max {
		return s[:max]
	}
	return s
}

func (ro *router) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{
		"public_url": ro.publicURL,
	})
}

// handleGetStats returns aggregate usage for the current calendar month.
// Intended for the managed hosting control plane to poll.
func (ro *router) handleGetStats(w http.ResponseWriter, r *http.Request) {
	if ro.statsAPIKey == "" {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(ro.statsAPIKey)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx := r.Context()
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	projects, err := storage.CountProjects(ctx, ro.pool)
	if err != nil {
		slog.Error("stats: count projects", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	users, err := storage.CountUsers(ctx, ro.pool)
	if err != nil {
		slog.Error("stats: count users", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	events, err := storage.CountMonthlyEvents(ctx, ro.pool)
	if err != nil {
		slog.Error("stats: count monthly events", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	lastMonthEvents, err := storage.CountLastMonthEvents(ctx, ro.pool)
	if err != nil {
		slog.Error("stats: count last month events", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	lastPeriodStart := time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)

	writeJSON(w, struct {
		Projects        int64  `json:"projects"`
		Users           int64  `json:"users"`
		EventsThisMonth int64  `json:"events_this_month"`
		EventsLastMonth int64  `json:"events_last_month"`
		PeriodStart     string `json:"period_start"`
		LastPeriodStart string `json:"last_period_start"`
		EventLimit      int    `json:"event_limit"`
		Version         string `json:"version"`
		UptimeSeconds   int64  `json:"uptime_seconds"`
	}{
		Projects:        projects,
		Users:           users,
		EventsThisMonth: events,
		EventsLastMonth: lastMonthEvents,
		PeriodStart:     periodStart.Format(time.RFC3339),
		LastPeriodStart: lastPeriodStart.Format(time.RFC3339),
		EventLimit:      int(ro.eventLimit.Load()),
		Version:         AppVersion,
		UptimeSeconds:   int64(time.Since(ro.startedAt).Seconds()),
	})
}

func (ro *router) handleGetInstanceHealth(w http.ResponseWriter, r *http.Request) {
	h, err := storage.GetInstanceHealth(r.Context(), ro.pool)
	if err != nil {
		slog.Error("instance health", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, struct {
		*storage.InstanceHealth
		RetentionDays int `json:"retention_days"`
	}{h, ro.retentionDays})
}

// handleGetSettings returns server-wide limits and version info for use by the UI.
func (ro *router) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	latest, releaseURL := ro.getLatestRelease()
	writeJSON(w, struct {
		ProjectLimit    int    `json:"project_limit"`
		EventLimit      int    `json:"event_limit"`
		UserLimit       int    `json:"user_limit"`
		Version         string `json:"version"`
		Commit          string `json:"commit"`
		BillingURL      string `json:"billing_url,omitempty"`
		LatestVersion   string `json:"latest_version,omitempty"`
		UpdateAvailable bool   `json:"update_available"`
		ReleaseURL      string `json:"release_url,omitempty"`
	}{
		ProjectLimit:    int(ro.projectLimit.Load()),
		EventLimit:      int(ro.eventLimit.Load()),
		UserLimit:       int(ro.userLimit.Load()),
		Version:         AppVersion,
		Commit:          AppCommit,
		BillingURL:      ro.billingURL,
		LatestVersion:   latest,
		UpdateAvailable: semverGT(latest, AppVersion),
		ReleaseURL:      releaseURL,
	})
}

// handleListProjects returns all projects. Single-tenant - every session user sees all projects.
func (ro *router) handleListProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := storage.ListProjects(r.Context(), ro.pool)
	if err != nil {
		slog.Error("list projects", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []*storage.Project{}
	}
	writeJSON(w, projects)
}

var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func (ro *router) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Slug == "" {
		http.Error(w, "name and slug required", http.StatusBadRequest)
		return
	}
	if !slugRe.MatchString(req.Slug) {
		http.Error(w, "slug must be lowercase alphanumeric with hyphens", http.StatusBadRequest)
		return
	}
	if lim := ro.projectLimit.Load(); lim > 0 {
		count, err := storage.CountProjects(r.Context(), ro.pool)
		if err != nil {
			slog.Error("count projects", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if count >= int64(lim) {
			http.Error(w, "project limit reached", http.StatusTooManyRequests)
			return
		}
	}
	p, err := storage.CreateProject(r.Context(), ro.pool, req.Slug, req.Name)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "slug already taken", http.StatusConflict)
			return
		}
		slog.Error("create project", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "project.created",
		TargetID:  &p.ID,
		IP:        r.RemoteAddr,
	})
	writeJSONStatus(w, http.StatusCreated, p)
}

func (ro *router) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "projectID")
	var req struct {
		Name           string  `json:"name"`
		Slug           string  `json:"slug"`
		PassthroughDSN *string `json:"passthrough_dsn"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.Slug == "" {
		http.Error(w, "name and slug required", http.StatusBadRequest)
		return
	}
	if !slugRe.MatchString(req.Slug) {
		http.Error(w, "slug must be lowercase alphanumeric with hyphens", http.StatusBadRequest)
		return
	}
	// Treat empty string as "clear the passthrough DSN".
	if req.PassthroughDSN != nil && *req.PassthroughDSN == "" {
		req.PassthroughDSN = nil
	}
	if req.PassthroughDSN != nil {
		if err := alerts.ValidateWebhookURL(r.Context(), *req.PassthroughDSN, ro.webhookAllowPrivateIPs); err != nil {
			http.Error(w, "invalid passthrough_dsn: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	p, err := storage.UpdateProject(r.Context(), ro.pool, id, req.Name, req.Slug, req.PassthroughDSN)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "slug already taken", http.StatusConflict)
			return
		}
		slog.Error("update project", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if p == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "project.updated",
		TargetID:  &p.ID,
		IP:        r.RemoteAddr,
	})
	writeJSON(w, p)
}

func (ro *router) handleUpdateProjectPrivacy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "projectID")
	var req struct {
		ScrubFields   []string        `json:"scrub_fields"`
		ScrubPatterns json.RawMessage `json:"scrub_patterns"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.ScrubFields == nil {
		req.ScrubFields = []string{}
	}
	if len(req.ScrubPatterns) == 0 {
		req.ScrubPatterns = json.RawMessage("[]")
	}
	var scrubPatterns []ingest.ScrubPattern
	if err := json.Unmarshal(req.ScrubPatterns, &scrubPatterns); err != nil {
		http.Error(w, "invalid scrub_patterns", http.StatusBadRequest)
		return
	}
	if err := ingest.ValidateScrubPatterns(scrubPatterns); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p, err := storage.UpdateProjectScrubbing(r.Context(), ro.pool, id, req.ScrubFields, req.ScrubPatterns)
	if err != nil {
		slog.Error("update project privacy", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if p == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "project.updated",
		TargetID:  &p.ID,
		IP:        r.RemoteAddr,
	})
	writeJSON(w, p)
}

func (ro *router) handleGetProjectQuota(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectID")

	events, err := storage.CountProjectEvents(r.Context(), ro.pool, projectID)
	if err != nil {
		slog.Error("quota: count events", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	daily, err := storage.DailyEventVolume(r.Context(), ro.pool, projectID)
	if err != nil {
		slog.Error("quota: daily volume", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if daily == nil {
		daily = []int64{}
	}

	rlCount, rlResetAt := ro.envelopeRL.peek(projectID)

	resp := struct {
		EventsThisMonth  int64   `json:"events_this_month"`
		EventLimit       int     `json:"event_limit"`
		RateLimitPerMin  int     `json:"rate_limit_per_min"`
		RateLimitUsed    int     `json:"rate_limit_used"`
		RateLimitResetAt *string `json:"rate_limit_reset_at,omitempty"`
		DailyVolume      []int64 `json:"daily_volume"`
	}{
		EventsThisMonth: events,
		EventLimit:      int(ro.eventLimit.Load()),
		RateLimitPerMin: ro.envelopeRL.limit,
		RateLimitUsed:   rlCount,
		DailyVolume:     daily,
	}
	if !rlResetAt.IsZero() {
		s := rlResetAt.UTC().Format(time.RFC3339)
		resp.RateLimitResetAt = &s
	}

	writeJSON(w, resp)
}

func (ro *router) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "projectID")
	found, err := storage.DeleteProjectByID(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("delete project", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "project.deleted",
		TargetID:  &id,
		IP:        r.RemoteAddr,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleListAllIssues returns a paginated issue list across all projects.
// Response: { issues, total, has_more, next_cursor_time?, next_cursor_id? }
func (ro *router) handleListAllIssues(w http.ResponseWriter, r *http.Request) {
	const pageSize = 50
	filter := storage.IssueFilter{
		Status:      r.URL.Query().Get("status"),
		Level:       r.URL.Query().Get("level"),
		Environment: r.URL.Query().Get("env"),
		AssigneeID:  r.URL.Query().Get("assignee_id"),
		TagKey:      r.URL.Query().Get("tag_key"),
		TagValue:    r.URL.Query().Get("tag_value"),
		Title:       truncSearch(r.URL.Query().Get("q")),
		ProjectIDs:  bearerProjectIDs(r, r.URL.Query()["project_id"]),
		Limit:       pageSize,
	}
	if since := r.URL.Query().Get("since"); since != "" {
		var d time.Duration
		switch since {
		case "24h":
			d = 24 * time.Hour
		case "7d":
			d = 7 * 24 * time.Hour
		case "30d":
			d = 30 * 24 * time.Hour
		case "90d":
			d = 90 * 24 * time.Hour
		}
		if d > 0 {
			t := time.Now().Add(-d)
			filter.SinceLast = &t
		}
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

	total, err := storage.CountAllIssues(r.Context(), ro.pool, filter)
	if err != nil {
		slog.Error("count all issues", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	issues, err := storage.ListAllIssues(r.Context(), ro.pool, filter)
	if err != nil {
		slog.Error("list all issues", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issues == nil {
		issues = []*storage.Issue{}
	}
	attachSparklines(r.Context(), ro.pool, issues)

	resp := map[string]any{
		"issues":   issues,
		"total":    total,
		"has_more": len(issues) == pageSize,
	}
	if len(issues) == pageSize {
		last := issues[len(issues)-1]
		resp["next_cursor_time"] = last.LastSeen.UTC().Format(time.RFC3339Nano)
		resp["next_cursor_id"] = last.ID
	}

	writeJSON(w, resp)
}

// handleExportIssues streams all matching issues as CSV or JSON (max 10,000 rows).
// Query params mirror handleListAllIssues; add format=csv (default) or format=json.
func (ro *router) handleExportIssues(w http.ResponseWriter, r *http.Request) {
	const maxRows = 10_000
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != "json" {
		http.Error(w, "format must be csv or json", http.StatusBadRequest)
		return
	}

	filter := storage.IssueFilter{
		Status:      r.URL.Query().Get("status"),
		Level:       r.URL.Query().Get("level"),
		Environment: r.URL.Query().Get("env"),
		AssigneeID:  r.URL.Query().Get("assignee_id"),
		TagKey:      r.URL.Query().Get("tag_key"),
		TagValue:    r.URL.Query().Get("tag_value"),
		ProjectIDs:  bearerProjectIDs(r, r.URL.Query()["project_id"]),
		Limit:       maxRows,
	}
	if since := r.URL.Query().Get("since"); since != "" {
		var d time.Duration
		switch since {
		case "24h":
			d = 24 * time.Hour
		case "7d":
			d = 7 * 24 * time.Hour
		case "30d":
			d = 30 * 24 * time.Hour
		case "90d":
			d = 90 * 24 * time.Hour
		}
		if d > 0 {
			t := time.Now().Add(-d)
			filter.SinceLast = &t
		}
	}

	issues, err := storage.ListAllIssues(r.Context(), ro.pool, filter)
	if err != nil {
		slog.Error("export issues", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issues == nil {
		issues = []*storage.Issue{}
	}

	ts := time.Now().UTC().Format("2006-01-02")
	filename := fmt.Sprintf("issues-%s.%s", ts, format)

	if format == "json" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
		writeJSON(w, issues)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "project_id", "title", "level", "status", "environment", "assignee", "event_count", "first_seen", "last_seen"})
	for _, iss := range issues {
		env := ""
		if iss.Environment != nil {
			env = *iss.Environment
		}
		assignee := ""
		if iss.AssigneeEmail != nil {
			assignee = *iss.AssigneeEmail
		}
		_ = cw.Write([]string{
			iss.ID,
			iss.ProjectID,
			iss.Title,
			iss.Level,
			iss.Status,
			env,
			assignee,
			fmt.Sprintf("%d", iss.EventCount),
			iss.FirstSeen.UTC().Format(time.RFC3339),
			iss.LastSeen.UTC().Format(time.RFC3339),
		})
	}
	cw.Flush()
}

// handleBulkUpdateIssues sets the same status on multiple issues at once.
func (ro *router) handleBulkUpdateIssues(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs              []string   `json:"ids"`
		Status           string     `json:"status"`
		IgnoreUntil      *time.Time `json:"ignore_until"`
		IgnoreCountLimit *int       `json:"ignore_count_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) == 0 {
		http.Error(w, "ids must be a non-empty array", http.StatusBadRequest)
		return
	}
	var ignoreOpts *storage.IgnoreOptions
	if req.Status == "ignored" && (req.IgnoreUntil != nil || req.IgnoreCountLimit != nil) {
		ignoreOpts = &storage.IgnoreOptions{Until: req.IgnoreUntil, CountLimit: req.IgnoreCountLimit}
	}
	n, err := storage.BulkUpdateIssueStatus(r.Context(), ro.pool, req.IDs, req.Status, ignoreOpts, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	actor := actorFromContext(r.Context())
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "issue.bulk_status_changed",
		ActorID:   actor,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"status": req.Status, "count": n},
	})
	now := time.Now().UTC()
	historyDetails := map[string]any{"to": req.Status}
	if req.Status == "ignored" {
		if req.IgnoreUntil != nil {
			historyDetails["ignore_until"] = req.IgnoreUntil.Format(time.RFC3339)
		}
		if req.IgnoreCountLimit != nil {
			historyDetails["ignore_count_limit"] = *req.IgnoreCountLimit
		}
	}
	for _, issueID := range req.IDs {
		if err := storage.InsertIssueHistory(r.Context(), ro.pool, storage.IssueHistoryEntry{
			IssueID:   issueID,
			ActorID:   actor,
			EventType: "status_changed",
			Details:   historyDetails,
			CreatedAt: now,
		}); err != nil {
			slog.Error("insert bulk issue history", "err", err, "issue_id", issueID)
		}
	}
	writeJSON(w, map[string]any{"updated": n})
}

// handleGetIssueGlobal fetches a single issue by ID without project scoping.
func (ro *router) handleGetIssueGlobal(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "issueID")
	issue, err := storage.GetIssue(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get issue", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issue == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceTokenProject(w, r, issue.ProjectID) {
		return
	}
	writeJSON(w, issue)
}

// handleUpdateIssueGlobal updates an issue's status or assignee without project scoping.
func (ro *router) handleUpdateIssueGlobal(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "issueID")

	issue, err := storage.GetIssue(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get issue for update", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issue == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var req struct {
		Status           string     `json:"status"`
		AssigneeID       *string    `json:"assignee_id"`
		IgnoreUntil      *time.Time `json:"ignore_until"`
		IgnoreCountLimit *int       `json:"ignore_count_limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	actor := actorFromContext(r.Context())
	var updated *storage.Issue
	if req.Status != "" {
		if req.Status == issue.Status {
			writeJSON(w, issue)
			return
		}
		var ignoreOpts *storage.IgnoreOptions
		if req.Status == "ignored" && (req.IgnoreUntil != nil || req.IgnoreCountLimit != nil) {
			ignoreOpts = &storage.IgnoreOptions{Until: req.IgnoreUntil, CountLimit: req.IgnoreCountLimit}
		}
		updated, err = storage.UpdateIssueStatus(r.Context(), ro.pool, issue.ProjectID, id, req.Status, ignoreOpts)
		if err != nil {
			slog.Error("update issue status", "err", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		storage.WriteAuditLog(ro.pool, storage.AuditEntry{
			EventType: "issue.status_changed",
			ActorID:   actor,
			ProjectID: &issue.ProjectID,
			TargetID:  &id,
			IP:        r.RemoteAddr,
			Details:   map[string]any{"status": req.Status},
		})
		historyDetails := map[string]any{"from": issue.Status, "to": req.Status}
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
			slog.Error("insert issue history (status)", "err", err)
		}
	} else {
		// assignee update (req.AssigneeID may be nil to unassign)
		updated, err = storage.UpdateIssueAssignee(r.Context(), ro.pool, id, req.AssigneeID)
		if err != nil {
			slog.Error("update issue assignee", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		details := map[string]any{"to_id": nil}
		if req.AssigneeID != nil {
			details["to_id"] = *req.AssigneeID
		}
		if err := storage.InsertIssueHistory(r.Context(), ro.pool, storage.IssueHistoryEntry{
			IssueID:   id,
			ActorID:   actor,
			EventType: "assigned",
			Details:   details,
			CreatedAt: time.Now().UTC(),
		}); err != nil {
			slog.Error("insert issue history (assigned)", "err", err)
		}
	}

	if updated == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, updated)
}

// handleGetIssueHistory returns the timeline of system and user events for an issue.
func (ro *router) handleGetIssueHistory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "issueID")
	if !ro.enforceIssueProject(w, r, id) {
		return
	}
	entries, err := storage.GetIssueHistory(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get issue history", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if entries == nil {
		entries = []*storage.IssueHistoryEntry{}
	}
	writeJSON(w, entries)
}

// handleGetIssueTags returns aggregated tag distribution across all events for an issue.
func (ro *router) handleGetIssueTags(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "issueID")
	if !ro.enforceIssueProject(w, r, id) {
		return
	}
	tags, err := storage.GetIssueTags(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get issue tags", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if tags == nil {
		tags = []storage.TagSummary{}
	}
	writeJSON(w, tags)
}

// handleGetLatestEventGlobal returns an event for an issue by reverse-chronological
// offset (?offset=0 is newest, ?offset=1 is second newest, etc.).
func (ro *router) handleGetLatestEventGlobal(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "issueID")
	issue, err := storage.GetIssue(r.Context(), ro.pool, issueID)
	if err != nil {
		slog.Error("get issue for event", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issue == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceTokenProject(w, r, issue.ProjectID) {
		return
	}

	offset := 0
	if s := r.URL.Query().Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}

	ev, err := storage.GetEventForIssueAtOffset(r.Context(), ro.pool, issueID, offset)
	if err != nil {
		slog.Error("get latest event", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if ev == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	payload := ev.Payload
	if ro.smStore != nil {
		release := ""
		if ev.Release != nil {
			release = *ev.Release
		}
		payload = ro.smStore.ResolveEventPayload(r.Context(), issue.ProjectID, release, payload)
	}

	writeJSON(w, struct {
		ID         string          `json:"id"`
		TraceID    *string         `json:"trace_id,omitempty"`
		ReceivedAt time.Time       `json:"received_at"`
		Payload    json.RawMessage `json:"payload"`
	}{
		ID:         ev.ID,
		TraceID:    ev.TraceID,
		ReceivedAt: ev.ReceivedAt,
		Payload:    payload,
	})
}

func (ro *router) handleGetIssueTrace(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "issueID")
	issue, err := storage.GetIssue(r.Context(), ro.pool, issueID)
	if err != nil {
		slog.Error("get issue for trace", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issue == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceTokenProject(w, r, issue.ProjectID) {
		return
	}

	offset := 0
	if s := r.URL.Query().Get("offset"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			offset = n
		}
	}

	ev, err := storage.GetEventForIssueAtOffset(r.Context(), ro.pool, issueID, offset)
	if err != nil || ev == nil || ev.TraceID == nil || *ev.TraceID == "" {
		writeJSON(w, nil)
		return
	}

	tx, err := storage.GetTransactionByTraceID(r.Context(), ro.pool, *ev.TraceID)
	if err != nil {
		slog.Error("get transaction by trace", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, tx)
}

func (ro *router) handleGetIssueHistogram(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "issueID")
	issue, err := storage.GetIssue(r.Context(), ro.pool, issueID)
	if err != nil {
		slog.Error("get issue for histogram", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issue == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceTokenProject(w, r, issue.ProjectID) {
		return
	}
	result, err := storage.GetIssueHistogram(r.Context(), ro.pool, issueID, issue.FirstSeen)
	if err != nil {
		slog.Error("get issue histogram", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, result)
}

func (ro *router) handleListEventsForIssue(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "issueID")
	if !ro.enforceIssueProject(w, r, issueID) {
		return
	}

	var cursorTime *time.Time
	var cursorID *string
	if ct := r.URL.Query().Get("cursor_time"); ct != "" {
		if t, err := time.Parse(time.RFC3339Nano, ct); err == nil {
			cursorTime = &t
		}
	}
	if ci := r.URL.Query().Get("cursor_id"); ci != "" {
		cursorID = &ci
	}

	events, hasMore, err := storage.ListEventsForIssue(r.Context(), ro.pool, issueID, cursorTime, cursorID, 50)
	if err != nil {
		slog.Error("list events for issue", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	type response struct {
		Events         []storage.EventSummary `json:"events"`
		HasMore        bool                   `json:"has_more"`
		NextCursorTime *string                `json:"next_cursor_time,omitempty"`
		NextCursorID   *string                `json:"next_cursor_id,omitempty"`
	}
	resp := response{Events: events, HasMore: hasMore}
	if hasMore && len(events) > 0 {
		last := events[len(events)-1]
		t := last.ReceivedAt.UTC().Format(time.RFC3339Nano)
		resp.NextCursorTime = &t
		resp.NextCursorID = &last.ID
	}
	writeJSON(w, resp)
}

// handleListTransactionSummaries returns aggregated transaction stats grouped by name+op.
func (ro *router) handleListTransactionSummaries(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n >= 1 && n <= 720 {
			hours = n
		}
	}
	offsetHours := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		if n, err := strconv.Atoi(o); err == nil && n >= 0 && n <= 720 {
			offsetHours = n
		}
	}
	env := r.URL.Query().Get("env")
	name := r.URL.Query().Get("name")
	op := r.URL.Query().Get("op")
	release := r.URL.Query().Get("release")
	projectIDs := bearerProjectIDs(r, r.URL.Query()["project_id"])
	if projectIDs == nil {
		projectIDs = []string{}
	}

	summaries, err := storage.ListTransactionSummaries(r.Context(), ro.pool, projectIDs, hours, offsetHours, env, name, op, release)
	if err != nil {
		slog.Error("list transaction summaries", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if summaries == nil {
		summaries = []*storage.TransactionSummary{}
	}
	writeJSON(w, summaries)
}

// handleTransactionTimeseries returns bucketed request counts and latency percentiles.
func (ro *router) handleTransactionTimeseries(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n >= 1 && n <= 720 {
			hours = n
		}
	}
	env := r.URL.Query().Get("env")
	name := r.URL.Query().Get("name")
	op := r.URL.Query().Get("op")
	projectIDs := bearerProjectIDs(r, r.URL.Query()["project_id"])
	if projectIDs == nil {
		projectIDs = []string{}
	}

	ts, err := storage.GetTransactionTimeseries(r.Context(), ro.pool, projectIDs, hours, env, name, op)
	if err != nil {
		slog.Error("get transaction timeseries", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, ts)
}

// handleListAllTransactions returns transactions across all projects with keyset pagination.
func (ro *router) handleListAllTransactions(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n >= 1 && n <= 100 {
			limit = n
		}
	}
	filter := storage.TransactionFilter{
		ProjectIDs:  bearerProjectIDs(r, r.URL.Query()["project_id"]),
		Op:          r.URL.Query().Get("op"),
		Status:      r.URL.Query().Get("status"),
		Environment: r.URL.Query().Get("environment"),
		Name:        r.URL.Query().Get("name"),
		Limit:       limit,
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

	txns, err := storage.ListAllTransactions(r.Context(), ro.pool, filter)
	if err != nil {
		slog.Error("list all transactions", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if txns == nil {
		txns = []*storage.Transaction{}
	}

	type response struct {
		Transactions   []*storage.Transaction `json:"transactions"`
		NextCursorTime *string                `json:"next_cursor_time,omitempty"`
		NextCursorID   *string                `json:"next_cursor_id,omitempty"`
	}
	resp := response{Transactions: txns}
	if len(txns) == limit {
		last := txns[len(txns)-1]
		ts := last.StartTimestamp.UTC().Format(time.RFC3339Nano)
		resp.NextCursorTime = &ts
		resp.NextCursorID = &last.ID
	}
	writeJSON(w, resp)
}

// handleGetTransactionGlobal fetches a transaction by ID without project scoping.
func (ro *router) handleGetTransactionGlobal(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "txID")
	tx, err := storage.GetTransaction(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get transaction", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if tx == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceTokenProject(w, r, tx.ProjectID) {
		return
	}
	writeJSON(w, tx)
}

type spanResponse struct {
	ID               string          `json:"id"`
	TransactionID    string          `json:"transaction_id"`
	SpanID           string          `json:"span_id"`
	ParentSpanID     string          `json:"parent_span_id,omitempty"`
	Op               string          `json:"op"`
	Description      string          `json:"description"`
	Status           string          `json:"status"`
	StartOffsetMs    int64           `json:"start_offset_ms"`
	DurationMs       int             `json:"duration_ms"`
	IsCritical       bool            `json:"is_critical"`
	StartTimestampMs int64           `json:"start_timestamp_ms"`
	Data             json.RawMessage `json:"data,omitempty"`
}

// computeCriticalPath returns the set of span_ids on the critical path.
// The critical path is the chain of dependent spans where, if any ran slower,
// the whole transaction would take longer. Spans not on it are parallelizable.
func computeCriticalPath(tx *storage.Transaction, spans []*storage.Span) map[string]bool {
	if len(spans) == 0 {
		return nil
	}

	bySpanID := make(map[string]*storage.Span, len(spans))
	for _, s := range spans {
		if s.SpanID != "" {
			bySpanID[s.SpanID] = s
		}
	}

	// parent span_id → child spans (only within this transaction's span set)
	children := make(map[string][]*storage.Span)
	for _, s := range spans {
		if s.ParentSpanID != "" {
			if _, ok := bySpanID[s.ParentSpanID]; ok {
				children[s.ParentSpanID] = append(children[s.ParentSpanID], s)
			}
		}
	}

	// effectiveEnd[spanID] = latest end time (ms from tx start) reachable from this span,
	// following its descendants. This is the "ceiling" that determines the critical path.
	txStart := tx.StartTimestamp
	effectiveEnd := make(map[string]int64, len(spans))

	var computeEnd func(s *storage.Span, depth int) int64
	computeEnd = func(s *storage.Span, depth int) int64 {
		if v, ok := effectiveEnd[s.SpanID]; ok {
			return v
		}
		if depth > len(spans) { // cycle guard - should never happen in valid trace data
			effectiveEnd[s.SpanID] = 0
			return 0
		}
		ownEnd := s.StartTimestamp.Sub(txStart).Milliseconds() + int64(s.DurationMs)
		best := ownEnd
		for _, child := range children[s.SpanID] {
			if ce := computeEnd(child, depth+1); ce > best {
				best = ce
			}
		}
		effectiveEnd[s.SpanID] = best
		return best
	}

	for _, s := range spans {
		if s.SpanID != "" {
			computeEnd(s, 0)
		}
	}

	var critEnd int64
	for _, v := range effectiveEnd {
		if v > critEnd {
			critEnd = v
		}
	}
	if critEnd <= 0 {
		return nil
	}

	result := make(map[string]bool)
	for _, s := range spans {
		if s.SpanID != "" && effectiveEnd[s.SpanID] == critEnd {
			result[s.SpanID] = true
		}
	}
	return result
}

// handleGetSpansGlobal returns spans for a transaction with pre-computed start_offset_ms
// and is_critical flags marking the spans on the critical path.
func (ro *router) handleGetSpansGlobal(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "txID")
	tx, err := storage.GetTransaction(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get transaction for spans", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if tx == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceTokenProject(w, r, tx.ProjectID) {
		return
	}

	spans, err := storage.GetSpansForTransaction(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get spans", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	critical := computeCriticalPath(tx, spans)

	out := make([]spanResponse, 0, len(spans))
	for _, s := range spans {
		out = append(out, spanResponse{
			ID:               s.ID,
			TransactionID:    s.TransactionID,
			SpanID:           s.SpanID,
			ParentSpanID:     s.ParentSpanID,
			Op:               s.Op,
			Description:      s.Description,
			Status:           s.Status,
			StartOffsetMs:    s.StartTimestamp.Sub(tx.StartTimestamp).Milliseconds(),
			DurationMs:       s.DurationMs,
			IsCritical:       critical[s.SpanID],
			StartTimestampMs: s.StartTimestamp.UnixMilli(),
			Data:             s.Data,
		})
	}
	writeJSON(w, out)
}

// handleGetTransactionErrors returns errors that share the transaction's trace_id,
// joined to their parent issue. Used to correlate errors with spans in the waterfall.
func (ro *router) handleGetTransactionErrors(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "txID")
	tx, err := storage.GetTransaction(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get transaction for errors", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if tx == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceTokenProject(w, r, tx.ProjectID) {
		return
	}
	if tx.TraceID == "" {
		writeJSON(w, []*storage.TraceError{})
		return
	}
	errs, err := storage.GetErrorsForTrace(r.Context(), ro.pool, tx.TraceID)
	if err != nil {
		slog.Error("get trace errors", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if errs == nil {
		errs = []*storage.TraceError{}
	}
	writeJSON(w, errs)
}

// handleListAllTokens returns all API tokens across all projects.
func (ro *router) handleListAllTokens(w http.ResponseWriter, r *http.Request) {
	var tokens []*storage.APIToken
	var err error
	if tokenProjID, ok := r.Context().Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		tokens, err = storage.ListAPITokens(r.Context(), ro.pool, tokenProjID)
	} else {
		tokens, err = storage.ListAllAPITokens(r.Context(), ro.pool)
	}
	if err != nil {
		slog.Error("list all tokens", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if tokens == nil {
		tokens = []*storage.APIToken{}
	}
	writeJSON(w, tokens)
}

// handleCreateTokenGlobal creates an API token. Requires project_id in the request body.
func (ro *router) handleCreateTokenGlobal(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name      string `json:"name"`
		ProjectID string `json:"project_id"`
		Writable  bool   `json:"writable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.ProjectID == "" {
		http.Error(w, "name and project_id are required", http.StatusBadRequest)
		return
	}

	token, plaintext, err := storage.CreateAPIToken(r.Context(), ro.pool, req.ProjectID, req.Name, req.Writable)
	if err != nil {
		slog.Error("create api token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "token.created",
		ActorID:   actorFromContext(r.Context()),
		ProjectID: &req.ProjectID,
		TargetID:  &token.ID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"name": token.Name},
	})

	writeJSONStatus(w, http.StatusCreated, struct {
		Token string            `json:"token"`
		Meta  *storage.APIToken `json:"meta"`
	}{Token: plaintext, Meta: token})
}

// handleUpdateTokenGlobal updates an API token's name, project, and writable flag.
func (ro *router) handleUpdateTokenGlobal(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "tokenID")
	var req struct {
		Name      string `json:"name"`
		ProjectID string `json:"project_id"`
		Writable  bool   `json:"writable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.ProjectID == "" {
		http.Error(w, "name and project_id are required", http.StatusBadRequest)
		return
	}
	token, err := storage.UpdateAPIToken(r.Context(), ro.pool, id, req.Name, req.ProjectID, req.Writable)
	if err != nil {
		slog.Error("update api token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if token == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "token.updated",
		ActorID:   actorFromContext(r.Context()),
		ProjectID: &token.ProjectID,
		TargetID:  &id,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"name": token.Name, "writable": token.Writable},
	})
	writeJSON(w, token)
}

// handleDeleteTokenGlobal revokes an API token by ID without project scoping.
func (ro *router) handleDeleteTokenGlobal(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "tokenID")
	deleted, err := storage.DeleteAPITokenByID(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("delete api token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !deleted {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "token.deleted",
		ActorID:   actorFromContext(r.Context()),
		IP:        r.RemoteAddr,
		TargetID:  &id,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleSpanSummaries returns span summaries for a given category (db/cache/job).
func (ro *router) handleSpanSummaries(category string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hours := 24
		if h := r.URL.Query().Get("hours"); h != "" {
			if n, err := strconv.Atoi(h); err == nil && n > 0 && n <= 720 {
				hours = n
			}
		}
		projectIDs := bearerProjectIDs(r, r.URL.Query()["project_id"])
		env := r.URL.Query().Get("env")
		release := r.URL.Query().Get("release")

		summaries, err := storage.GetSpanSummaries(r.Context(), ro.pool, category, projectIDs, hours, env, release)
		if err != nil {
			slog.Error("span summaries", "category", category, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, summaries)
	}
}

func (ro *router) handleSpanSamples(w http.ResponseWriter, r *http.Request) {
	op := r.URL.Query().Get("op")
	description := r.URL.Query().Get("description")
	env := r.URL.Query().Get("env")
	release := r.URL.Query().Get("release")
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 {
		hours = 24
	}
	projectIDs := bearerProjectIDs(r, r.URL.Query()["project_id"])

	samples, err := storage.GetSpanSamples(r.Context(), ro.pool, op, description, projectIDs, hours, env, release)
	if err != nil {
		slog.Error("span samples", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, samples)
}

// handleSpanTimeseries returns bucketed timeseries for a span category.
func (ro *router) handleSpanTimeseries(category string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hours := 24
		if h := r.URL.Query().Get("hours"); h != "" {
			if n, err := strconv.Atoi(h); err == nil && n > 0 && n <= 720 {
				hours = n
			}
		}
		projectIDs := bearerProjectIDs(r, r.URL.Query()["project_id"])
		env := r.URL.Query().Get("env")
		release := r.URL.Query().Get("release")

		ts, err := storage.GetSpanTimeseries(r.Context(), ro.pool, category, projectIDs, hours, env, release)
		if err != nil {
			slog.Error("span timeseries", "category", category, "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, ts)
	}
}

func (ro *router) handleGetWebVitals(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n >= 1 && n <= 720 {
			hours = n
		}
	}
	projectIDs := bearerProjectIDs(r, r.URL.Query()["project_id"])
	if len(projectIDs) == 0 {
		projectIDs = []string{}
	}
	env := r.URL.Query().Get("env")

	now := time.Now().UTC()
	from := now.Add(-time.Duration(hours) * time.Hour)

	summary, err := storage.GetWebVitalsSummary(r.Context(), ro.pool, projectIDs, from, now, env)
	if err != nil {
		slog.Error("get web vitals summary", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, summary)
}

func (ro *router) handleGetWebVitalsPages(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n >= 1 && n <= 720 {
			hours = n
		}
	}
	projectIDs := bearerProjectIDs(r, r.URL.Query()["project_id"])
	if len(projectIDs) == 0 {
		projectIDs = []string{}
	}
	env := r.URL.Query().Get("env")

	now := time.Now().UTC()
	from := now.Add(-time.Duration(hours) * time.Hour)

	pages, err := storage.GetWebVitalsByPage(r.Context(), ro.pool, projectIDs, from, now, env)
	if err != nil {
		slog.Error("get web vitals pages", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if pages == nil {
		pages = []storage.WebVitalsPage{}
	}
	writeJSON(w, pages)
}
