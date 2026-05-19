package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/storage"
)

func (ro *router) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}

	var req struct {
		Name     string `json:"name"`
		Writable bool   `json:"writable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	token, plaintext, err := storage.CreateAPIToken(r.Context(), ro.pool, project.ID, req.Name, req.Writable)
	if err != nil {
		slog.Error("create api token", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "token.created",
		ActorID:   actorFromContext(r.Context()),
		ProjectID: &project.ID,
		TargetID:  &token.ID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"name": token.Name},
	})

	writeJSONStatus(w, http.StatusCreated, struct {
		*storage.APIToken
		Token string `json:"token"`
	}{APIToken: token, Token: plaintext})
}

func (ro *router) handleListTokens(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}

	tokens, err := storage.ListAPITokens(r.Context(), ro.pool, project.ID)
	if err != nil {
		slog.Error("list api tokens", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, struct {
		Tokens []*storage.APIToken `json:"tokens"`
	}{Tokens: tokens})
}

func (ro *router) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "tokenID")
	deleted, err := storage.DeleteAPIToken(r.Context(), ro.pool, id, project.ID)
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
		ProjectID: &project.ID,
		TargetID:  &id,
		IP:        r.RemoteAddr,
	})

	w.WriteHeader(http.StatusNoContent)
}
