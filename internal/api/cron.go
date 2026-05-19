package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/storage"
)

// handleListMonitors returns all monitors visible to the caller, optionally
// filtered by project_id query params.
func (ro *router) handleListMonitors(w http.ResponseWriter, r *http.Request) {
	projectIDs := bearerProjectIDs(r, r.URL.Query()["project_id"])
	monitors, err := storage.ListCronMonitors(r.Context(), ro.pool, projectIDs)
	if err != nil {
		slog.Error("list monitors", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if monitors == nil {
		monitors = []*storage.CronMonitor{}
	}
	writeJSON(w, monitors)
}

func (ro *router) handleCreateMonitor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID       string `json:"project_id"`
		Name            string `json:"name"`
		Schedule        string `json:"schedule"`
		GracePeriodSecs int    `json:"grace_period_secs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.ProjectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if req.Schedule == "" {
		http.Error(w, "schedule required", http.StatusBadRequest)
		return
	}
	if _, err := storage.ParseCronSchedule(req.Schedule); err != nil {
		http.Error(w, "invalid schedule: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.GracePeriodSecs <= 0 {
		req.GracePeriodSecs = 300
	}

	m, err := storage.CreateCronMonitor(r.Context(), ro.pool, &storage.CronMonitor{
		ProjectID:       req.ProjectID,
		Name:            req.Name,
		Schedule:        req.Schedule,
		GracePeriodSecs: req.GracePeriodSecs,
	})
	if err != nil {
		slog.Error("create monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSONStatus(w, http.StatusCreated, m)
}

func (ro *router) handleGetMonitor(w http.ResponseWriter, r *http.Request) {
	m, ok := ro.resolveMonitor(w, r)
	if !ok {
		return
	}
	writeJSON(w, m)
}

func (ro *router) handleUpdateMonitor(w http.ResponseWriter, r *http.Request) {
	m, ok := ro.resolveMonitor(w, r)
	if !ok {
		return
	}
	var req struct {
		Name            *string `json:"name"`
		Schedule        *string `json:"schedule"`
		GracePeriodSecs *int    `json:"grace_period_secs"`
		Status          *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Name != nil {
		m.Name = *req.Name
	}
	if req.Schedule != nil {
		if _, err := storage.ParseCronSchedule(*req.Schedule); err != nil {
			http.Error(w, "invalid schedule: "+err.Error(), http.StatusBadRequest)
			return
		}
		m.Schedule = *req.Schedule
	}
	if req.GracePeriodSecs != nil {
		m.GracePeriodSecs = *req.GracePeriodSecs
	}
	if req.Status != nil {
		if *req.Status != "active" && *req.Status != "paused" {
			http.Error(w, "status must be active or paused", http.StatusBadRequest)
			return
		}
		m.Status = *req.Status
	}
	updated, err := storage.UpdateCronMonitor(r.Context(), ro.pool, m)
	if err != nil {
		slog.Error("update monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if updated == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, updated)
}

func (ro *router) handleDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "monitorID")
	ok, err := storage.DeleteCronMonitor(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("delete monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (ro *router) handleListCheckins(w http.ResponseWriter, r *http.Request) {
	m, ok := ro.resolveMonitor(w, r)
	if !ok {
		return
	}
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		}
	}
	checkins, err := storage.ListCheckins(r.Context(), ro.pool, m.ID, limit)
	if err != nil {
		slog.Error("list checkins", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if checkins == nil {
		checkins = []*storage.CronCheckin{}
	}
	writeJSON(w, checkins)
}

// handleCronPing accepts a simple GET or POST ping from a cron job.
// The monitor UUID is the only auth - no session or bearer token needed.
// Query params: status (ok|error, default ok), duration (seconds, float).
func (ro *router) handleCronPing(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "monitorID")

	m, err := storage.GetCronMonitor(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("cron ping: get monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if m == nil || m.Status == "paused" {
		w.WriteHeader(http.StatusOK) // silently accept pings for unknown/paused monitors
		return
	}

	status := r.URL.Query().Get("status")
	if status == "" {
		status = "ok"
	}
	if status != "ok" && status != "error" {
		http.Error(w, "status must be ok or error", http.StatusBadRequest)
		return
	}

	var durationMs *int
	if d := r.URL.Query().Get("duration"); d != "" {
		if secs, err := strconv.ParseFloat(d, 64); err == nil && secs >= 0 {
			ms := int(secs * 1000)
			durationMs = &ms
		}
	}

	now := time.Now().UTC()
	ci := &storage.CronCheckin{
		Status:     status,
		DurationMs: durationMs,
		FinishedAt: &now,
	}
	if env := r.URL.Query().Get("environment"); env != "" {
		ci.Environment = &env
	}

	if _, err := storage.RecordCheckin(r.Context(), ro.pool, id, ci); err != nil {
		slog.Error("cron ping: record", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleCronCheckinStart starts a new in_progress check-in (Sentry-compat).
func (ro *router) handleCronCheckinStart(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "monitorID")

	m, err := storage.GetCronMonitor(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("checkin start: get monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if m == nil || m.Status == "paused" {
		// Return a synthetic ID so the SDK doesn't error out
		writeJSONStatus(w, http.StatusCreated, map[string]string{"id": "00000000-0000-0000-0000-000000000000"})
		return
	}

	var req struct {
		Status      string  `json:"status"`
		Environment *string `json:"environment"`
	}
	// Non-fatal decode - SDK may send partial body
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck
	if req.Status == "" {
		req.Status = "in_progress"
	}

	now := time.Now().UTC()
	ci := &storage.CronCheckin{
		Status:      req.Status,
		Environment: req.Environment,
		StartedAt:   &now,
	}
	created, err := storage.RecordCheckin(r.Context(), ro.pool, id, ci)
	if err != nil {
		slog.Error("checkin start: record", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]string{"id": created.ID})
}

// handleCronCheckinFinish finishes an in_progress check-in (Sentry-compat).
func (ro *router) handleCronCheckinFinish(w http.ResponseWriter, r *http.Request) {
	monitorID := chi.URLParam(r, "monitorID")
	checkinID := chi.URLParam(r, "checkinID")

	var req struct {
		Status   string   `json:"status"`
		Duration *float64 `json:"duration"` // seconds
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Status == "" {
		http.Error(w, "status required", http.StatusBadRequest)
		return
	}
	if req.Status != "ok" && req.Status != "error" {
		http.Error(w, "status must be ok or error", http.StatusBadRequest)
		return
	}

	var durationMs *int
	if req.Duration != nil && *req.Duration >= 0 {
		ms := int(*req.Duration * 1000)
		durationMs = &ms
	}

	updated, err := storage.FinishCheckin(r.Context(), ro.pool, monitorID, checkinID, req.Status, durationMs)
	if err != nil {
		slog.Error("checkin finish: update", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if updated == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleOhDearStarting handles POST /api/cron/{monitorID}/starting (Oh Dear / Spatie compat).
func (ro *router) handleOhDearStarting(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "monitorID")
	m, err := storage.GetCronMonitor(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("oh dear starting: get monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if m == nil || m.Status == "paused" {
		w.WriteHeader(http.StatusOK)
		return
	}
	now := time.Now().UTC()
	ci := &storage.CronCheckin{Status: "in_progress", StartedAt: &now}
	created, err := storage.RecordCheckin(r.Context(), ro.pool, id, ci)
	if err != nil {
		slog.Error("oh dear starting: record", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSONStatus(w, http.StatusCreated, map[string]string{"id": created.ID})
}

// handleOhDearFinished handles POST /api/cron/{monitorID}/finished (Oh Dear / Spatie compat).
// Payload may include: runtime (float, seconds), memory (int, KB), exit_code (int).
func (ro *router) handleOhDearFinished(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "monitorID")
	m, err := storage.GetCronMonitor(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("oh dear finished: get monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if m == nil || m.Status == "paused" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var body struct {
		Runtime *float64 `json:"runtime"` // seconds
	}
	json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck

	var durationMs *int
	if body.Runtime != nil && *body.Runtime >= 0 {
		ms := int(*body.Runtime * 1000)
		durationMs = &ms
	}
	now := time.Now().UTC()
	ci := &storage.CronCheckin{Status: "ok", DurationMs: durationMs, FinishedAt: &now}
	if _, err := storage.RecordCheckin(r.Context(), ro.pool, id, ci); err != nil {
		slog.Error("oh dear finished: record", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleOhDearFailed handles POST /api/cron/{monitorID}/failed (Oh Dear / Spatie compat).
func (ro *router) handleOhDearFailed(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "monitorID")
	m, err := storage.GetCronMonitor(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("oh dear failed: get monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if m == nil || m.Status == "paused" {
		w.WriteHeader(http.StatusOK)
		return
	}
	now := time.Now().UTC()
	ci := &storage.CronCheckin{Status: "error", FinishedAt: &now}
	if _, err := storage.RecordCheckin(r.Context(), ro.pool, id, ci); err != nil {
		slog.Error("oh dear failed: record", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// resolveMonitor loads a monitor by ID from the URL param, writing 404 if missing.
func (ro *router) resolveMonitor(w http.ResponseWriter, r *http.Request) (*storage.CronMonitor, bool) {
	id := chi.URLParam(r, "monitorID")
	m, err := storage.GetCronMonitor(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	if m == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return nil, false
	}
	if !enforceTokenProject(w, r, m.ProjectID) {
		return nil, false
	}
	return m, true
}
