package api

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/storage"
)

func (ro *router) handleUploadSourcemap(w http.ResponseWriter, r *http.Request) {
	if ro.smStore == nil {
		http.Error(w, "sourcemap storage not configured", http.StatusServiceUnavailable)
		return
	}
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	release := r.FormValue("release")
	if release == "" {
		http.Error(w, "release is required", http.StatusBadRequest)
		return
	}
	url := r.FormValue("url")
	if url == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	sm, err := ro.smStore.Upload(r.Context(), project.ID, release, url, file)
	if err != nil {
		slog.Error("upload sourcemap", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	writeJSON(w, sm)
}

func (ro *router) handleListSourcemaps(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}

	release := r.URL.Query().Get("release")
	maps, err := storage.ListSourcemaps(r.Context(), ro.pool, project.ID, release)
	if err != nil {
		slog.Error("list sourcemaps", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSON(w, struct {
		Sourcemaps []*storage.Sourcemap `json:"sourcemaps"`
	}{Sourcemaps: maps})
}

func (ro *router) handleDeleteSourcemap(w http.ResponseWriter, r *http.Request) {
	project, ok := ro.projectFromSlug(w, r)
	if !ok {
		return
	}

	id := chi.URLParam(r, "smID")
	deleted, err := ro.smStore.Delete(r.Context(), id, project.ID)
	if err != nil {
		slog.Error("delete sourcemap", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !deleted {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
