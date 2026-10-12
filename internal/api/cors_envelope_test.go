package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
)

// With CORS_ORIGIN set for the dashboard, browser SDKs on other sites must still
// get the envelope route's own wildcard preflight answer.
func TestCORSOrigin_doesNotOverrideEnvelopePreflight(t *testing.T) {
	h := api.NewRouter(api.Config{Pool: testPool, Events: ingest.NewBuffer(1), CORSOrigin: "https://dashboard.example.com"})

	req := httptest.NewRequest(http.MethodOptions, "/api/"+testProject.ID+"/envelope/", nil)
	req.Header.Set("Origin", "https://customer-site.example.org")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "x-sentry-auth, content-type")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-Sentry-Auth") {
		t.Errorf("Access-Control-Allow-Headers = %q, want it to include X-Sentry-Auth", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("envelope preflight must not allow credentials, got %q", got)
	}

	// Dashboard API routes still use the configured origin, including PUT.
	req = httptest.NewRequest(http.MethodOptions, "/api/comments/x", nil)
	req.Header.Set("Origin", "https://dashboard.example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dashboard.example.com" {
		t.Errorf("dashboard Access-Control-Allow-Origin = %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "PUT") {
		t.Errorf("Access-Control-Allow-Methods = %q, want it to include PUT", got)
	}
}
