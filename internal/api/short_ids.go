package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/blendbyte/tindra/internal/storage"
)

// resolveIssueRef turns an issue reference into an issue ID. It accepts the
// UUID or a short ID such as BACKEND-42, which follows merges. ok is false
// when neither names an issue.
func (ro *router) resolveIssueRef(ctx context.Context, ref string) (id string, ok bool, err error) {
	if _, err := uuid.Parse(ref); err == nil {
		return ref, true, nil
	}
	id, err = storage.ResolveShortID(ctx, ro.pool, ref)
	if err != nil {
		return "", false, err
	}
	return id, id != "", nil
}

// issueRefParam lets an {issueID} route take a short ID: it puts the issue's
// UUID in the URL parameter before the handler reads it, so the UI can request
// an issue's sections by the short ID in its address without waiting for the
// issue itself.
func (ro *router) issueRefParam(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, found, err := ro.resolveIssueRef(r.Context(), chi.URLParam(r, "issueID"))
		if err != nil {
			slog.Error("resolve issue reference", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !found {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		params := &chi.RouteContext(r.Context()).URLParams
		for i, key := range params.Keys {
			if key == "issueID" {
				params.Values[i] = id
			}
		}
		next.ServeHTTP(w, r)
	})
}
