package api

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

const (
	maxAuthBodyBytes    = 4 * 1024
	authBodyReadTimeout = 10 * time.Second

	// Cron check-in bodies are tiny JSON documents (status, duration).
	maxCronBodyBytes = 16 * 1024
)

// limitAuthBody bounds the entire body before authentication handlers decode it.
func limitAuthBody(timeout time.Duration) func(http.Handler) http.Handler {
	return limitBody(maxAuthBodyBytes, timeout)
}

// limitBody reads at most maxBytes of the body within timeout before the handler
// runs, so unauthenticated routes cannot be held open by a slow or huge body.
// Reading through EOF also catches oversized trailing data and chunked bodies.
// The network deadline applies only to reading the body, not hashing or SSO calls.
func limitBody(maxBytes int64, timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			controller := http.NewResponseController(w)
			if err := controller.SetReadDeadline(time.Now().Add(timeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
				r.Close = true
				w.Header().Set("Connection", "close")
				http.Error(w, "unable to read request", http.StatusInternalServerError)
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBytes))
			if err != nil {
				// Do not clear an expired deadline or drain an untrusted remaining body.
				r.Close = true
				w.Header().Set("Connection", "close")
				var tooLarge *http.MaxBytesError
				var timeoutError net.Error
				switch {
				case errors.As(err, &tooLarge):
					http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
				case errors.As(err, &timeoutError) && timeoutError.Timeout():
					http.Error(w, "request body read timed out", http.StatusRequestTimeout)
				default:
					http.Error(w, "unable to read request", http.StatusBadRequest)
				}
				return
			}
			if err := controller.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
				r.Close = true
				w.Header().Set("Connection", "close")
				http.Error(w, "unable to read request", http.StatusInternalServerError)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}
}
