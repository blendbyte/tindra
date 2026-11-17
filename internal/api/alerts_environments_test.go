package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

func alertRuleRequest(t *testing.T, method, path string, body map[string]any) (*httptest.ResponseRecorder, storage.AlertRule) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	var rule storage.AlertRule
	if rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), &rule); err != nil {
			t.Fatalf("decode rule: %v (%s)", err, rec.Body.String())
		}
	}
	return rec, rule
}

func envRuleBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"name":        "env rule",
		"trigger":     "new_issue",
		"channel":     "webhook",
		"webhook_url": "https://203.0.113.1/alert",
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func TestCreateAlertRule_filterEnvironments(t *testing.T) {
	truncateAlertRules(t)

	rec, rule := alertRuleRequest(t, http.MethodPost, "/api/alert-rules", envRuleBody(map[string]any{
		"filter_environments": []string{" production ", "staging", "", "production"},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	// Trimmed, blanks and repeats dropped, order kept.
	if got := fmt.Sprint(rule.FilterEnvironments); got != "[production staging]" {
		t.Errorf("filter_environments: got %s, want [production staging]", got)
	}

	// The list wins over the legacy field when both are sent.
	rec, rule = alertRuleRequest(t, http.MethodPost, "/api/alert-rules", envRuleBody(map[string]any{
		"filter_environments": []string{"staging"},
		"filter_environment":  "production",
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := fmt.Sprint(rule.FilterEnvironments); got != "[staging]" {
		t.Errorf("filter_environments: got %s, want [staging]", got)
	}

	// No filter: an empty list in the response, never null.
	rec, _ = alertRuleRequest(t, http.MethodPost, "/api/alert-rules", envRuleBody(nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"filter_environments":[]`) {
		t.Errorf("expected filter_environments:[] in %s", rec.Body.String())
	}
}

func TestCreateAlertRule_filterEnvironmentsRejected(t *testing.T) {
	truncateAlertRules(t)

	tooMany := make([]string, 21)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("env-%d", i)
	}
	// Far past the cap, so the request must be refused at entry 21 rather
	// than after deduplicating everything.
	huge := make([]string, 200000)
	for i := range huge {
		huge[i] = fmt.Sprintf("e%d", i)
	}
	for name, envs := range map[string][]string{
		"too many": tooMany,
		"huge":     huge,
		"too long": {strings.Repeat("x", 513)},
	} {
		rec, _ := alertRuleRequest(t, http.MethodPost, "/api/alert-rules", envRuleBody(map[string]any{
			"filter_environments": envs,
		}))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: expected 400, got %d: %s", name, rec.Code, rec.Body.String())
		}
	}
}

func TestUpdateAlertRule_filterEnvironments(t *testing.T) {
	truncateAlertRules(t)

	rec, rule := alertRuleRequest(t, http.MethodPost, "/api/alert-rules", envRuleBody(map[string]any{
		"filter_environments": []string{"production"},
	}))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	path := "/api/alert-rules/" + rule.ID

	steps := []struct {
		name  string
		patch map[string]any
		want  string
	}{
		{"set list", map[string]any{"filter_environments": []string{"production", "staging"}}, "[production staging]"},
		{"unrelated patch keeps it", map[string]any{"name": "renamed"}, "[production staging]"},
		{"legacy field replaces it", map[string]any{"filter_environment": "qa"}, "[qa]"},
		{"empty list clears it", map[string]any{"filter_environments": []string{}}, "[]"},
		{"set again", map[string]any{"filter_environments": []string{"production"}}, "[production]"},
		{"null clears it", map[string]any{"filter_environments": nil}, "[]"},
		{"list wins over legacy field", map[string]any{"filter_environments": []string{"x"}, "filter_environment": "production"}, "[x]"},
	}
	for _, step := range steps {
		rec, got := alertRuleRequest(t, http.MethodPatch, path, step.patch)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", step.name, rec.Code, rec.Body.String())
		}
		if fmt.Sprint(got.FilterEnvironments) != step.want {
			t.Errorf("%s: got %v, want %s", step.name, got.FilterEnvironments, step.want)
		}
	}

	rec, got := alertRuleRequest(t, http.MethodPatch, path, map[string]any{"filter_environment": ""})
	if rec.Code != http.StatusOK || len(got.FilterEnvironments) != 0 {
		t.Errorf("legacy empty string: got %d %v, want 200 and no environments", rec.Code, got.FilterEnvironments)
	}
}
