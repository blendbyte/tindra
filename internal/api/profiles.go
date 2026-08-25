package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/profiles"
	"github.com/blendbyte/tindra/internal/storage"
)

// handleGetTransactionFlameGraph returns the folded call tree for a
// transaction's profile.
//
// The response is a tree of a few hundred nodes rather than the thousands of
// raw samples behind it, so the folding cost is paid once here instead of
// shipping megabytes of JSON for the browser to aggregate.
func (ro *router) handleGetTransactionFlameGraph(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "txID")

	tx, err := storage.GetTransaction(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get transaction for flame graph", "err", err)
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

	graph, err := profiles.FlameGraphForTransaction(r.Context(), ro.pool, id)
	if errors.Is(err, profiles.ErrNoProfile) {
		// Not an error state. Most transactions have no profile, either because
		// the SDK sampled it out or because profiling is off, and the UI needs
		// to tell those apart from a failure.
		http.Error(w, "no profile", http.StatusNotFound)
		return
	}
	if err != nil {
		slog.Error("build flame graph", "tx", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, graph)
}
