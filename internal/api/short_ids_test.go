package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func getIssueJSON(t *testing.T, path string, auth func(*http.Request)) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	auth(req)
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)
	var body map[string]any
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	return rec.Code, body
}

func withCookie(r *http.Request) { r.AddCookie(authCookie()) }

func TestGetIssue_byShortID(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "short-id-api", "Short ID Error")
	if !strings.HasPrefix(iss.ShortID, "TEST-PROJECT-") {
		t.Fatalf("short ID: got %q, want TEST-PROJECT-<n>", iss.ShortID)
	}

	for _, path := range []string{
		"/api/issues/" + iss.ShortID,
		"/api/issues/" + strings.ToLower(iss.ShortID),
		"/api/issues/" + iss.ID,
		"/api/projects/" + testProject.Slug + "/issues/" + iss.ShortID,
	} {
		code, body := getIssueJSON(t, path, withCookie)
		if code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", path, code)
		}
		if body["id"] != iss.ID || body["short_id"] != iss.ShortID {
			t.Errorf("%s: got id %v short_id %v, want %s %s", path, body["id"], body["short_id"], iss.ID, iss.ShortID)
		}
	}

	for _, path := range []string{
		"/api/issues/TEST-PROJECT-999999",
		"/api/issues/NOPE-1",
		"/api/issues/not-a-short-id",
		"/api/projects/" + testProject.Slug + "/issues/NOPE-1",
	} {
		if code, _ := getIssueJSON(t, path, withCookie); code != http.StatusNotFound {
			t.Errorf("%s: expected 404, got %d", path, code)
		}
	}
}

// The issue page requests its sections and changes by the short ID in its
// address, so every /api/issues/{id} route takes one.
func TestIssueSubroutes_byShortID(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "short-id-subroutes", "Subroute Error")

	status := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(authCookie())
		rec := httptest.NewRecorder()
		mcpHandler().ServeHTTP(rec, req)
		return rec.Code
	}
	for _, sub := range []string{"/comments", "/history", "/tags", "/events/histogram"} {
		if code := status("/api/issues/" + iss.ShortID + sub); code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", sub, code)
		}
		if code := status("/api/issues/TEST-PROJECT-999999" + sub); code != http.StatusNotFound {
			t.Errorf("unknown short ID %s: expected 404, got %d", sub, code)
		}
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/issues/"+strings.ToLower(iss.ShortID), strings.NewReader(`{"status":"resolved"}`))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH by short ID: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	got, err := storage.GetIssue(context.Background(), testPool, iss.ID)
	if err != nil || got.Status != "resolved" {
		t.Errorf("PATCH by short ID: status %v, err %v", got, err)
	}
}

// A short ID does not get a token past its project scope.
func TestGetIssue_byShortIDTokenScoped(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "short-id-scope", "Scoped Error")
	other, err := storage.CreateProject(context.Background(), testPool, "short-id-other", "Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID) })
	_, token, err := storage.CreateAPIToken(context.Background(), testPool, other.ID, "short-id-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	code, _ := getIssueJSON(t, "/api/issues/"+iss.ShortID, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) })
	if code == http.StatusOK {
		t.Errorf("token for another project read %s", iss.ShortID)
	}
}

func TestMCP_issueToolsTakeShortIDs(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "short-id-mcp", "MCP Short Error")
	seedMCPIssueEvent(t, iss.ID, "short-id-mcp", `{"message":"hi"}`, time.Now().UTC())

	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"get_issue", map[string]any{"id": iss.ShortID}},
		{"get_event_detail", map[string]any{"issue_id": strings.ToLower(iss.ShortID)}},
		{"update_issue", map[string]any{"id": iss.ShortID, "status": "resolved"}},
	} {
		result := toolCall(t, mcpHandler(), call.tool, call.args, authCookie())
		if isErr, _ := result["isError"].(bool); isErr {
			t.Fatalf("%s: unexpected isError: %s", call.tool, toolText(t, result))
		}
		if text := toolText(t, result); !strings.Contains(text, iss.ID) && call.tool != "get_event_detail" {
			t.Errorf("%s: result does not name issue %s: %s", call.tool, iss.ID, text)
		}
	}

	got, err := storage.GetIssue(context.Background(), testPool, iss.ID)
	if err != nil || got.Status != "resolved" {
		t.Errorf("update_issue by short ID: status %v, err %v", got, err)
	}

	result := toolCall(t, mcpHandler(), "get_issue", map[string]any{"id": "TEST-PROJECT-999999"}, authCookie())
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Errorf("unknown short ID: expected isError, got %v", result)
	}
}
