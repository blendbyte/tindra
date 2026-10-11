package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
)

func csrfRouter() http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, nil, false, "https://dash.example.com", "https://tindra.example.com", "", "", 0, 0, 0, 0, 0, 0, nil, false, false, nil)
}

func TestCrossOriginProtection(t *testing.T) {
	h := csrfRouter()
	send := func(method, path, body string, headers map[string]string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		req.AddCookie(authCookie())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	crossSite := map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"}
	sameSite := map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "https://evil.tindra.example.com"}

	// Login CSRF and cookie-authenticated writes from another origin are refused.
	if code := send(http.MethodPost, "/api/auth/login", `{"email":"x@example.com","password":"y"}`, crossSite); code != http.StatusForbidden {
		t.Errorf("cross-site login: expected 403, got %d", code)
	}
	if code := send(http.MethodPatch, "/api/me", `{"name":"pwned"}`, sameSite); code != http.StatusForbidden {
		t.Errorf("sibling-subdomain PATCH /api/me: expected 403, got %d", code)
	}

	// Same-origin browser requests and non-browser clients still work.
	if code := send(http.MethodPatch, "/api/me", `{}`, map[string]string{"Sec-Fetch-Site": "same-origin"}); code == http.StatusForbidden {
		t.Error("same-origin PATCH /api/me must not be refused")
	}
	if code := send(http.MethodPatch, "/api/me", `{}`, nil); code == http.StatusForbidden {
		t.Error("non-browser PATCH /api/me must not be refused")
	}

	// Configured origins (PUBLIC_URL, CORS_ORIGIN) are trusted.
	if code := send(http.MethodPatch, "/api/me", `{}`, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://dash.example.com"}); code == http.StatusForbidden {
		t.Error("CORS_ORIGIN must be a trusted origin")
	}

	// Browser SDKs on customer sites post envelopes cross-origin.
	if code := send(http.MethodPost, "/api/"+testProject.ID+"/envelope/", "{}\n", crossSite); code == http.StatusForbidden {
		t.Error("cross-site envelope POST must not be refused")
	}
}
