package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
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
