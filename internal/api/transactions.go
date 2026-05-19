package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/storage"
)

func (ro *router) handleListTransactions(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}

	filter := storage.TransactionFilter{
		Op:          r.URL.Query().Get("op"),
		Status:      r.URL.Query().Get("status"),
		Environment: r.URL.Query().Get("environment"),
		Limit:       50,
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

	txns, err := storage.ListTransactions(r.Context(), ro.pool, project.ID, filter)
	if err != nil {
		slog.Error("list transactions", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := struct {
		Transactions   []*storage.Transaction `json:"transactions"`
		NextCursorTime *time.Time             `json:"next_cursor_time,omitempty"`
		NextCursorID   *string                `json:"next_cursor_id,omitempty"`
	}{Transactions: txns}

	if len(txns) == filter.Limit {
		last := txns[len(txns)-1]
		resp.NextCursorTime = &last.StartTimestamp
		resp.NextCursorID = &last.ID
	}

	writeJSON(w, resp)
}

func (ro *router) handleGetTransaction(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}
	id := chi.URLParam(r, "txID")

	tx, err := storage.GetTransaction(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get transaction", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if tx == nil || tx.ProjectID != project.ID {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	spans, err := storage.GetSpansForTransaction(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get spans", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, struct {
		Transaction *storage.Transaction `json:"transaction"`
		Spans       []*storage.Span      `json:"spans"`
	}{Transaction: tx, Spans: spans})
}

func (ro *router) handleTransactionStats(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}

	hours := 24
	if h := r.URL.Query().Get("hours"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n >= 1 && n <= 168 {
			hours = n
		}
	}

	stats, err := storage.GetTransactionPercentiles(r.Context(), ro.pool, project.ID, hours)
	if err != nil {
		slog.Error("transaction stats", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, stats)
}
