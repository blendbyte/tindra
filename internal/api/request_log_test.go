package api_test

import (
	"bytes"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Debug request logs must not contain path secrets such as invite tokens.
func TestRequestLog_usesRoutePatternNotRawPath(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	previousWriter := log.Writer()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer func() { slog.SetDefault(previous); log.SetOutput(previousWriter) }()

	const secret = "SECRETINVITETOKEN123"
	for _, path := range []string{"/api/auth/invite/" + secret, "/invite/" + secret} {
		rec := httptest.NewRecorder()
		globalHandler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	}

	logs := output.String()
	if strings.Contains(logs, secret) {
		t.Errorf("request log leaked a path secret:\n%s", logs)
	}
	if !strings.Contains(logs, "/api/auth/invite/{token}") {
		t.Errorf("request log should record the route pattern:\n%s", logs)
	}
}
