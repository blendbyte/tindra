package api

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func mustParseCIDR(s string) *net.IPNet {
	_, cidr, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return cidr
}

func captureRemoteAddr(mw func(http.Handler) http.Handler, req *http.Request) string {
	var got string
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	})).ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func TestRealIPFromTrustedProxy_xForwardedFor(t *testing.T) {
	mw := realIPFromTrustedProxy([]*net.IPNet{mustParseCIDR("10.0.0.0/8")})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 10.0.0.1")

	if got := captureRemoteAddr(mw, req); got != "1.2.3.4:0" {
		t.Errorf("RemoteAddr: got %q, want %q", got, "1.2.3.4:0")
	}
}

func TestRealIPFromTrustedProxy_xRealIP(t *testing.T) {
	mw := realIPFromTrustedProxy([]*net.IPNet{mustParseCIDR("10.0.0.0/8")})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Real-IP", "5.6.7.8")

	if got := captureRemoteAddr(mw, req); got != "5.6.7.8:0" {
		t.Errorf("RemoteAddr: got %q, want %q", got, "5.6.7.8:0")
	}
}

func TestRealIPFromTrustedProxy_xForwardedForTakesPrecedence(t *testing.T) {
	mw := realIPFromTrustedProxy([]*net.IPNet{mustParseCIDR("10.0.0.0/8")})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "9.9.9.9")
	req.Header.Set("X-Real-IP", "5.6.7.8")

	// XFF is checked first; X-Real-IP should not override it.
	if got := captureRemoteAddr(mw, req); got != "9.9.9.9:0" {
		t.Errorf("RemoteAddr: got %q, want %q", got, "9.9.9.9:0")
	}
}

func TestRealIPFromTrustedProxy_untrustedProxyHeaderIgnored(t *testing.T) {
	mw := realIPFromTrustedProxy([]*net.IPNet{mustParseCIDR("10.0.0.0/8")})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.1:5000" // not in trusted CIDR
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := captureRemoteAddr(mw, req); got != "203.0.113.1:5000" {
		t.Errorf("RemoteAddr should not be overwritten for untrusted proxy, got %q", got)
	}
}

func TestRealIPFromTrustedProxy_noTrustedProxiesConfigured(t *testing.T) {
	mw := realIPFromTrustedProxy(nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:9999"
	req.Header.Set("X-Forwarded-For", "9.9.9.9")

	if got := captureRemoteAddr(mw, req); got != "1.2.3.4:9999" {
		t.Errorf("RemoteAddr should not be overwritten when no proxies configured, got %q", got)
	}
}

func TestRealIPFromTrustedProxy_invalidForwardedForIgnored(t *testing.T) {
	mw := realIPFromTrustedProxy([]*net.IPNet{mustParseCIDR("10.0.0.0/8")})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "not-an-ip")

	if got := captureRemoteAddr(mw, req); got != "10.0.0.1:1234" {
		t.Errorf("RemoteAddr should not be overwritten for invalid XFF value, got %q", got)
	}
}

func TestRealIPFromTrustedProxy_invalidXRealIPIgnored(t *testing.T) {
	mw := realIPFromTrustedProxy([]*net.IPNet{mustParseCIDR("10.0.0.0/8")})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Real-IP", "not-an-ip")

	if got := captureRemoteAddr(mw, req); got != "10.0.0.1:1234" {
		t.Errorf("RemoteAddr should not be overwritten for invalid X-Real-IP value, got %q", got)
	}
}

// --- cidrContains ---

func TestCIDRContains_match(t *testing.T) {
	cidr := mustParseCIDR("192.168.0.0/24")
	if !cidrContains([]*net.IPNet{cidr}, net.ParseIP("192.168.0.100")) {
		t.Error("expected true for IP inside range")
	}
}

func TestCIDRContains_noMatch(t *testing.T) {
	cidr := mustParseCIDR("192.168.0.0/24")
	if cidrContains([]*net.IPNet{cidr}, net.ParseIP("10.0.0.1")) {
		t.Error("expected false for IP outside range")
	}
}

func TestCIDRContains_emptyCIDRs(t *testing.T) {
	if cidrContains(nil, net.ParseIP("1.2.3.4")) {
		t.Error("expected false for empty CIDRs slice")
	}
}

func TestCIDRContains_firstMatchWins(t *testing.T) {
	cidrs := []*net.IPNet{
		mustParseCIDR("10.0.0.0/8"),
		mustParseCIDR("192.168.0.0/24"),
	}
	if !cidrContains(cidrs, net.ParseIP("192.168.0.50")) {
		t.Error("expected true when IP matches second CIDR")
	}
	if cidrContains(cidrs, net.ParseIP("172.16.0.1")) {
		t.Error("expected false when IP matches no CIDR")
	}
}

// --- securityHeaders ---

func TestSecurityHeaders_coreHeadersAlwaysSet(t *testing.T) {
	ro := &router{cookieSecure: false}
	h := ro.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	for _, key := range []string{
		"X-Content-Type-Options",
		"X-Frame-Options",
		"Referrer-Policy",
		"Content-Security-Policy",
	} {
		if rec.Header().Get(key) == "" {
			t.Errorf("expected header %q to be set", key)
		}
	}
}

func TestSecurityHeaders_hstsSetWhenCookieSecure(t *testing.T) {
	ro := &router{cookieSecure: true}
	h := ro.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if hsts := rec.Header().Get("Strict-Transport-Security"); hsts == "" {
		t.Error("expected HSTS header when cookieSecure=true")
	}
}

func TestSecurityHeaders_noHSTSWithoutCookieSecure(t *testing.T) {
	ro := &router{cookieSecure: false}
	h := ro.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if hsts := rec.Header().Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("expected no HSTS header when cookieSecure=false, got %q", hsts)
	}
}

func TestSecurityHeaders_nextHandlerIsCalled(t *testing.T) {
	ro := &router{}
	called := false
	h := ro.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Error("next handler should have been called")
	}
	if rec.Code != http.StatusTeapot {
		t.Errorf("expected status from next handler, got %d", rec.Code)
	}
}

func TestCORS_headersSent(t *testing.T) {
	mw := corsMiddleware("http://localhost:3000")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	for _, key := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
		"Access-Control-Allow-Credentials",
		"Access-Control-Max-Age",
		"Vary",
	} {
		if rec.Header().Get(key) == "" {
			t.Errorf("expected header %q to be set", key)
		}
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:3000" {
		t.Errorf("Access-Control-Allow-Origin: got %q", got)
	}
}

func TestCORS_preflightOptions(t *testing.T) {
	mw := corsMiddleware("http://localhost:3000")
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS preflight, got %d", rec.Code)
	}
	if called {
		t.Error("next handler should not be called for OPTIONS preflight")
	}
}

func TestRealIPFromTrustedProxyTrustBoundary(t *testing.T) {
	trusted := []*net.IPNet{mustParseCIDR("10.0.0.0/8"), mustParseCIDR("fd00::/8")}
	for _, tc := range []struct {
		name, peer string
		xff, xri   []string
		want       string
	}{
		{"spoofed leftmost", "10.0.0.1:1234", []string{"192.0.2.99, 198.51.100.2"}, nil, "198.51.100.2:0"},
		{"multiple trusted hops", "10.0.0.1:1234", []string{"192.0.2.99, 198.51.100.2, 10.1.1.1, 10.2.2.2"}, nil, "198.51.100.2:0"},
		{"untrusted intervening proxy", "10.0.0.1:1234", []string{"192.0.2.99, 198.51.100.2, 203.0.113.3"}, nil, "203.0.113.3:0"},
		{"repeated XFF headers", "10.0.0.1:1234", []string{"192.0.2.99", "198.51.100.2, 10.2.2.2"}, nil, "198.51.100.2:0"},
		{"malformed untrusted prefix ignored", "10.0.0.1:1234", []string{"garbage, 198.51.100.2"}, nil, "198.51.100.2:0"},
		{"malformed nearest hop", "10.0.0.1:1234", []string{"192.0.2.99, garbage, 10.2.2.2"}, []string{"192.0.2.98"}, "10.0.0.1:1234"},
		{"empty hop", "10.0.0.1:1234", []string{"192.0.2.99,"}, nil, "10.0.0.1:1234"},
		{"empty XFF blocks fallback", "10.0.0.1:1234", []string{""}, []string{"192.0.2.99"}, "10.0.0.1:1234"},
		{"all hops trusted", "10.0.0.1:1234", []string{"10.1.1.1, 10.2.2.2"}, []string{"192.0.2.99"}, "10.0.0.1:1234"},
		{"no forwarding headers", "10.0.0.1:1234", nil, nil, "10.0.0.1:1234"},
		{"ambiguous XRI", "10.0.0.1:1234", nil, []string{"192.0.2.99", "198.51.100.2"}, "10.0.0.1:1234"},
		{"IPv6 client and proxies", "[fd00::1]:1234", []string{"192.0.2.99, 2001:0db8:0000:0000:0000:0000:0000:0002, fd00::2"}, nil, "[2001:db8::2]:0"},
		{"IPv6 real IP fallback", "10.0.0.1:1234", nil, []string{" 2001:db8::2 "}, "[2001:db8::2]:0"},
		{"mapped IPv4 canonicalized", "10.0.0.1:1234", []string{"::ffff:198.51.100.2"}, nil, "198.51.100.2:0"},
		{"untrusted peer", "198.51.100.2:1234", []string{"192.0.2.99"}, nil, "198.51.100.2:1234"},
		{"peer missing port", "10.0.0.1", []string{"192.0.2.99"}, nil, "10.0.0.1"},
		{"peer not an IP", "hostname:1234", []string{"192.0.2.99"}, nil, "hostname:1234"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.peer
			for _, value := range tc.xff {
				req.Header.Add("X-Forwarded-For", value)
			}
			for _, value := range tc.xri {
				req.Header.Add("X-Real-IP", value)
			}
			if got := captureRemoteAddr(realIPFromTrustedProxy(trusted), req); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
			if req.RemoteAddr != tc.peer {
				t.Fatal("middleware mutated the original request")
			}
		})
	}
}

func TestForwardedSpoofingCannotResetIPRateLimit(t *testing.T) {
	limiter := newRateLimiter(1, time.Minute)
	h := realIPFromTrustedProxy([]*net.IPNet{mustParseCIDR("10.0.0.0/8")})(limiter.limitByIP()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })))
	for _, group := range [][]string{
		{"198.51.100.2", "198.51.100.2", "198.51.100.2"},
		{"2001:db8::2", "2001:0db8:0000:0000:0000:0000:0000:0002", "2001:db8::2"},
	} {
		for i, client := range group {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "10.0.0.1:1234"
			req.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d, %s, 10.1.1.1", i+1, client))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			want := http.StatusOK
			if i > 0 {
				want = http.StatusTooManyRequests
			}
			if rec.Code != want {
				t.Fatalf("client %s request %d: got %d want %d", client, i, rec.Code, want)
			}
		}
	}
}
