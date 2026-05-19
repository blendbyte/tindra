package api

import (
	"log/slog"
	"net/http"
	"time"

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
