package api

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// rateLimiter is a fixed-window in-memory rate limiter keyed by arbitrary strings.
// Suitable for single-process deployments - no shared state across replicas.
type rateLimiter struct {
	mu      sync.Mutex
	entries map[string]rlEntry
	limit   int
	window  time.Duration
	calls   int // used to trigger periodic pruning
}

type rlEntry struct {
	count   int
	resetAt time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{
		entries: make(map[string]rlEntry),
		limit:   limit,
		window:  window,
	}
}

func (rl *rateLimiter) allow(key string) bool {
	if rl.limit == 0 {
		return true // disabled
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	rl.calls++
	if rl.calls >= 500 {
		rl.calls = 0
		for k, e := range rl.entries {
			if now.After(e.resetAt) {
				delete(rl.entries, k)
			}
		}
	}
	e := rl.entries[key]
	if now.After(e.resetAt) {
		rl.entries[key] = rlEntry{count: 1, resetAt: now.Add(rl.window)}
		return true
	}
	if e.count >= rl.limit {
		return false
	}
	e.count++
	rl.entries[key] = e
	return true
}

// limitByIP rate-limits by the real client IP (after realIPFromTrustedProxy has run).
func (rl *rateLimiter) limitByIP() func(http.Handler) http.Handler {
	return rl.limitBy(func(r *http.Request) string {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			return r.RemoteAddr
		}
		return host
	})
}

// peek returns the current window count and reset time for a key without consuming a slot.
// Returns (0, zero time) when the key has no active window or rate limiting is disabled.
func (rl *rateLimiter) peek(key string) (count int, resetAt time.Time) {
	if rl.limit == 0 {
		return 0, time.Time{}
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	e := rl.entries[key]
	if time.Now().After(e.resetAt) {
		return 0, time.Time{}
	}
	return e.count, e.resetAt
}

// limitBy rate-limits by an arbitrary key derived from the request.
func (rl *rateLimiter) limitBy(keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFn(r)
			if !rl.allow(key) {
				_, resetAt := rl.peek(key)
				secs := max(int(time.Until(resetAt).Seconds())+1, 1)
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				http.Error(w, "too many requests", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
