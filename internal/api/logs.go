package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/storage"
)

func (ro *router) handleListLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter := storage.LogFilter{
		ProjectIDs:  bearerProjectIDs(r, q["project_id"]),
		Level:       q.Get("level"),
		Environment: q.Get("environment"),
		Search:      q.Get("search"),
		TraceID:     q.Get("trace_id"),
		Limit:       100,
	}
	if min := q.Get("min_level"); min != "" {
		if min == "warn" {
			min = "warning"
		}
		filter.Level = ""
		filter.Levels = alerts.LogLevelsAtOrAbove(min)
	}

	if ct := q.Get("cursor_time"); ct != "" {
		if t, err := time.Parse(time.RFC3339Nano, ct); err == nil {
			filter.CursorTime = &t
		}
	}
	if ci := q.Get("cursor_id"); ci != "" {
		filter.CursorID = &ci
	}

	logs, hasMore, err := storage.ListLogs(r.Context(), ro.pool, filter)
	if err != nil {
		slog.Error("list logs", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if logs == nil {
		logs = []*storage.Log{}
	}

	type response struct {
		Logs           []*storage.Log `json:"logs"`
		HasMore        bool           `json:"has_more"`
		NextCursorTime *string        `json:"next_cursor_time,omitempty"`
		NextCursorID   *string        `json:"next_cursor_id,omitempty"`
	}
	resp := response{Logs: logs, HasMore: hasMore}
	if hasMore && len(logs) > 0 {
		last := logs[len(logs)-1]
		ts := last.Timestamp.Format(time.RFC3339Nano)
		resp.NextCursorTime = &ts
		resp.NextCursorID = &last.ID
	}

	writeJSON(w, resp)
}

func (ro *router) handleCountLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	projectIDs := bearerProjectIDs(r, q["project_id"])
	if len(projectIDs) == 0 {
		http.Error(w, "project_id is required", http.StatusBadRequest)
		return
	}

	level := q.Get("level")
	if level == "warn" {
		level = "warning"
	}
	if !validLogCountLevels[level] {
		http.Error(w, "level must be fatal, error, or warning", http.StatusBadRequest)
		return
	}

	windowMins := 5
	if s := q.Get("window_mins"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 60 {
			http.Error(w, "window_mins must be between 1 and 60", http.StatusBadRequest)
			return
		}
		windowMins = n
	}

	search := q.Get("search")
	if len(search) > maxFilterSearchLen {
		http.Error(w, "search must be at most 200 characters", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	count, err := storage.CountLogs(ctx, ro.pool, storage.LogFilter{
		ProjectIDs:  projectIDs,
		Levels:      alerts.LogLevelsAtOrAbove(level),
		Environment: q.Get("environment"),
		Search:      search,
		WindowMins:  windowMins,
	})
	if err != nil {
		if ctx.Err() != nil {
			http.Error(w, "count timed out", http.StatusGatewayTimeout)
			return
		}
		slog.Error("count logs", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, struct {
		Count      int `json:"count"`
		WindowMins int `json:"window_mins"`
	}{Count: count, WindowMins: windowMins})
}
