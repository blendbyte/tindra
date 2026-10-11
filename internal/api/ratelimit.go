package api

import (
	"maps"
	"net"
	"net/http"
	"net/netip"
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
	// maxKeys, when > 0, bounds how many keys are tracked. Use it when keys come
	// from attacker-chosen input. Once full, unseen keys are allowed without being
	// tracked, so the limiter fails open rather than locking out new callers.
	maxKeys int
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
		rl.pruneExpired(now)
	}
	e, tracked := rl.entries[key]
	if !tracked && rl.maxKeys > 0 && len(rl.entries) >= rl.maxKeys {
		rl.pruneExpired(now)
		if len(rl.entries) >= rl.maxKeys {
			return true
		}
	}
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

// pruneExpired drops entries whose window has ended. Callers hold rl.mu.
func (rl *rateLimiter) pruneExpired(now time.Time) {
	maps.DeleteFunc(rl.entries, func(_ string, e rlEntry) bool { return now.After(e.resetAt) })
}

// limitByIP rate-limits by the real client IP (after realIPFromTrustedProxy has run).
func (rl *rateLimiter) limitByIP() func(http.Handler) http.Handler {
	return rl.limitBy(func(r *http.Request) string { return ipRateLimitKey(r.RemoteAddr) })
}

// ipRateLimitKey keys IPv4 clients by address and IPv6 clients by their /64,
// since a single host usually controls a whole /64 and could otherwise rotate
// addresses to get a fresh budget on every request.
func ipRateLimitKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr.WithZone(""), 64).Masked().String()
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
