package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/sourcemaps"
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

	// Allow 1 MiB for multipart headers and fields beyond the map itself.
	r.Body = http.MaxBytesReader(w, r.Body, sourcemaps.MaxUploadSize+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "source map upload exceeds the 11 MiB request limit", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	defer r.MultipartForm.RemoveAll() //nolint:errcheck

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
		if errors.Is(err, sourcemaps.ErrUploadTooLarge) {
			http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
			return
		}
		if errors.Is(err, sourcemaps.ErrInvalidMap) {
			http.Error(w, "invalid source map", http.StatusBadRequest)
			return
		}
		slog.Error("upload sourcemap", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	writeJSONStatus(w, http.StatusCreated, sm)
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
