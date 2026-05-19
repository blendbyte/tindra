package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_allowUnderLimit(t *testing.T) {
	rl := newRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !rl.allow("key") {
			t.Fatalf("expected allow on call %d", i+1)
		}
	}
}

func TestRateLimiter_allowBlocksAtLimit(t *testing.T) {
	rl := newRateLimiter(2, time.Minute)
	rl.allow("key")
	rl.allow("key")
	if rl.allow("key") {
		t.Error("expected allow to block after reaching limit")
	}
}

func TestRateLimiter_disabledWhenLimitZero(t *testing.T) {
	rl := newRateLimiter(0, time.Minute)
	for i := 0; i < 100; i++ {
		if !rl.allow("key") {
			t.Fatalf("expected allow to always return true when limit=0, failed on call %d", i+1)
		}
	}
}

func TestRateLimiter_windowReset(t *testing.T) {
	rl := newRateLimiter(1, 20*time.Millisecond)
	if !rl.allow("key") {
		t.Fatal("first call should be allowed")
	}
	if rl.allow("key") {
		t.Error("second call in same window should be blocked")
	}
	time.Sleep(30 * time.Millisecond)
	if !rl.allow("key") {
		t.Error("call after window expiry should be allowed")
	}
}

func TestRateLimiter_separateKeys(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	if !rl.allow("a") {
		t.Error("first call for key a should be allowed")
	}
	if !rl.allow("b") {
		t.Error("first call for key b should be allowed (different key)")
	}
	if rl.allow("a") {
		t.Error("second call for key a should be blocked")
	}
}

func TestRateLimiter_peekReturnsCountWithoutConsuming(t *testing.T) {
	rl := newRateLimiter(5, time.Minute)
	rl.allow("key")
	rl.allow("key")

	count, resetAt := rl.peek("key")
	if count != 2 {
		t.Errorf("peek count: got %d, want 2", count)
	}
	if resetAt.IsZero() {
		t.Error("peek resetAt should not be zero")
	}
	// peek did not consume a slot - the next allow should still work.
	if !rl.allow("key") {
		t.Error("allow after peek should succeed (peek must not consume a slot)")
	}
}

func TestRateLimiter_peekDisabledReturnsZero(t *testing.T) {
	rl := newRateLimiter(0, time.Minute)
	rl.allow("key")
	count, resetAt := rl.peek("key")
	if count != 0 {
		t.Errorf("peek count for disabled limiter: got %d, want 0", count)
	}
	if !resetAt.IsZero() {
		t.Error("peek resetAt for disabled limiter should be zero")
	}
}

func TestRateLimiter_peekExpiredWindowReturnsZero(t *testing.T) {
	rl := newRateLimiter(5, 20*time.Millisecond)
	rl.allow("key")
	time.Sleep(30 * time.Millisecond)

	count, resetAt := rl.peek("key")
	if count != 0 {
		t.Errorf("peek after window expiry: got count %d, want 0", count)
	}
	if !resetAt.IsZero() {
		t.Error("peek resetAt after window expiry should be zero")
	}
}

func TestRateLimiter_limitByIPMiddlewareBlocks(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	handler := rl.limitByIP()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	makeReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "1.2.3.4:5678"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	if first := makeReq(); first.Code != http.StatusOK {
		t.Errorf("first request: expected 200, got %d", first.Code)
	}
	second := makeReq()
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second request: expected 429, got %d", second.Code)
	}
	if second.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429 response")
	}
}

func TestRateLimiter_limitByIPIsolatesAddresses(t *testing.T) {
	rl := newRateLimiter(1, time.Minute)
	handler := rl.limitByIP()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	makeReq := func(addr string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}

	if makeReq("1.1.1.1:100") != http.StatusOK {
		t.Error("first IP: first request should be allowed")
	}
	if makeReq("2.2.2.2:100") != http.StatusOK {
		t.Error("second IP: first request should be allowed independently")
	}
	if makeReq("1.1.1.1:100") != http.StatusTooManyRequests {
		t.Error("first IP: second request should be blocked")
	}
}

func TestRateLimiter_pruningDoesNotPanic(t *testing.T) {
	rl := newRateLimiter(1000, 5*time.Millisecond)
	// Fill 499 calls with distinct keys, then let windows expire.
	for i := 0; i < 499; i++ {
		rl.allow(fmt.Sprintf("key-%d", i))
	}
	time.Sleep(10 * time.Millisecond) // let all windows expire
	// 500th call triggers the pruning loop - must not panic.
	rl.allow("prune-trigger")
}
