package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/storage"
)

func (ro *router) handleListUptimeMonitors(w http.ResponseWriter, r *http.Request) {
	projectIDs := bearerProjectIDs(r, r.URL.Query()["project_id"])
	monitors, err := storage.ListUptimeMonitors(r.Context(), ro.pool, projectIDs)
	if err != nil {
		slog.Error("list uptime monitors", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if monitors == nil {
		monitors = []*storage.UptimeMonitor{}
	}
	writeJSON(w, monitors)
}

func (ro *router) handleCreateUptimeMonitor(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProjectID     string  `json:"project_id"`
		Name          string  `json:"name"`
		URL           string  `json:"url"`
		Method        string  `json:"method"`
		IntervalSecs  int     `json:"interval_secs"`
		TimeoutSecs   int     `json:"timeout_secs"`
		ExpectedCodes string  `json:"expected_codes"`
		BodyContains  *string `json:"body_contains"`
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
	if req.URL == "" {
		http.Error(w, "url required", http.StatusBadRequest)
		return
	}
	if u, err := url.ParseRequestURI(req.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		http.Error(w, "url must be a valid http or https URL", http.StatusBadRequest)
		return
	}
	if req.Method == "" {
		req.Method = "GET"
	}
	if req.Method != "GET" && req.Method != "HEAD" {
		http.Error(w, "method must be GET or HEAD", http.StatusBadRequest)
		return
	}
	if req.IntervalSecs <= 0 {
		req.IntervalSecs = 300
	}
	if req.TimeoutSecs <= 0 {
		req.TimeoutSecs = 10
	}
	if req.ExpectedCodes == "" {
		req.ExpectedCodes = "200-299"
	}
	if _, err := storage.ParseExpectedCodes(req.ExpectedCodes); err != nil {
		http.Error(w, "invalid expected_codes: "+err.Error(), http.StatusBadRequest)
		return
	}

	m, err := storage.CreateUptimeMonitor(r.Context(), ro.pool, &storage.UptimeMonitor{
		ProjectID:     req.ProjectID,
		Name:          req.Name,
		URL:           req.URL,
		Method:        req.Method,
		IntervalSecs:  req.IntervalSecs,
		TimeoutSecs:   req.TimeoutSecs,
		ExpectedCodes: req.ExpectedCodes,
		BodyContains:  req.BodyContains,
	})
	if err != nil {
		slog.Error("create uptime monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "uptime_monitor.created",
		ActorID:   actorFromContext(r.Context()),
		ProjectID: &req.ProjectID,
		TargetID:  &m.ID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"name": m.Name, "url": m.URL},
	})
	writeJSONStatus(w, http.StatusCreated, m)
}

func (ro *router) handleGetUptimeMonitor(w http.ResponseWriter, r *http.Request) {
	m, ok := ro.resolveUptimeMonitor(w, r)
	if !ok {
		return
	}
	writeJSON(w, m)
}

func (ro *router) handleUpdateUptimeMonitor(w http.ResponseWriter, r *http.Request) {
	m, ok := ro.resolveUptimeMonitor(w, r)
	if !ok {
		return
	}
	var req struct {
		Name          *string `json:"name"`
		URL           *string `json:"url"`
		Method        *string `json:"method"`
		IntervalSecs  *int    `json:"interval_secs"`
		TimeoutSecs   *int    `json:"timeout_secs"`
		ExpectedCodes *string `json:"expected_codes"`
		BodyContains  *string `json:"body_contains"`
		Status        *string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if req.Name != nil {
		m.Name = *req.Name
	}
	if req.URL != nil {
		if u, err := url.ParseRequestURI(*req.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			http.Error(w, "url must be a valid http or https URL", http.StatusBadRequest)
			return
		}
		m.URL = *req.URL
	}
	if req.Method != nil {
		if *req.Method != "GET" && *req.Method != "HEAD" {
			http.Error(w, "method must be GET or HEAD", http.StatusBadRequest)
			return
		}
		m.Method = *req.Method
	}
	if req.IntervalSecs != nil {
		m.IntervalSecs = *req.IntervalSecs
	}
	if req.TimeoutSecs != nil {
		m.TimeoutSecs = *req.TimeoutSecs
	}
	if req.ExpectedCodes != nil {
		if _, err := storage.ParseExpectedCodes(*req.ExpectedCodes); err != nil {
			http.Error(w, "invalid expected_codes: "+err.Error(), http.StatusBadRequest)
			return
		}
		m.ExpectedCodes = *req.ExpectedCodes
	}
	if req.BodyContains != nil {
		if *req.BodyContains == "" {
			m.BodyContains = nil
		} else {
			m.BodyContains = req.BodyContains
		}
	}
	if req.Status != nil {
		if *req.Status != "active" && *req.Status != "paused" {
			http.Error(w, "status must be active or paused", http.StatusBadRequest)
			return
		}
		m.Status = *req.Status
	}

	updated, err := storage.UpdateUptimeMonitor(r.Context(), ro.pool, m)
	if err != nil {
		slog.Error("update uptime monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if updated == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "uptime_monitor.updated",
		ActorID:   actorFromContext(r.Context()),
		ProjectID: &updated.ProjectID,
		TargetID:  &updated.ID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"name": updated.Name},
	})
	writeJSON(w, updated)
}

func (ro *router) handleDeleteUptimeMonitor(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "monitorID")
	ok, err := storage.DeleteUptimeMonitor(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("delete uptime monitor", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "uptime_monitor.deleted",
		ActorID:   actorFromContext(r.Context()),
		TargetID:  &id,
		IP:        r.RemoteAddr,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (ro *router) handleListUptimeChecks(w http.ResponseWriter, r *http.Request) {
	m, ok := ro.resolveUptimeMonitor(w, r)
	if !ok {
		return
	}
	limit := 100
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			limit = n
		}
	}
	checks, err := storage.ListUptimeChecks(r.Context(), ro.pool, m.ID, limit)
	if err != nil {
		slog.Error("list uptime checks", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if checks == nil {
		checks = []*storage.UptimeCheck{}
	}
	writeJSON(w, checks)
}

func (ro *router) handleGetUptimeStats(w http.ResponseWriter, r *http.Request) {
	m, ok := ro.resolveUptimeMonitor(w, r)
	if !ok {
		return
	}
	stats, err := storage.GetUptimeStats(r.Context(), ro.pool, m.ID)
	if err != nil {
		slog.Error("get uptime stats", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, stats)
}

func (ro *router) resolveUptimeMonitor(w http.ResponseWriter, r *http.Request) (*storage.UptimeMonitor, bool) {
	id := chi.URLParam(r, "monitorID")
	m, err := storage.GetUptimeMonitor(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get uptime monitor", "err", err)
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
