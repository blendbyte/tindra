package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/storage"
)

func (ro *router) handleGetLatestEvent(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}

	issueID := chi.URLParam(r, "issueID")
	issue, err := storage.GetIssue(r.Context(), ro.pool, issueID)
	if err != nil {
		slog.Error("get issue for event", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if issue == nil || issue.ProjectID != project.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	ev, err := storage.GetLatestEventForIssue(r.Context(), ro.pool, issueID)
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
		payload = ro.smStore.ResolveEventPayload(r.Context(), project.ID, release, payload)
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
