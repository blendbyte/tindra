package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

const secretWebhook = "https://203.0.113.1/services/T000/B000/SECRETTOKENabcd"

func seedWebhookRule(t *testing.T) *storage.AlertRule {
	t.Helper()
	truncateAlertRules(t)
	url := secretWebhook
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "secret hook", Enabled: true,
		Trigger: "new_issue", Channel: "slack", WebhookURL: &url, CooldownMins: 60,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	return rule
}

func getBody(t *testing.T, path string, auth func(*http.Request)) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	auth(req)
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: expected 200, got %d", path, rec.Code)
	}
	return rec.Body.String()
}

func TestAlertRules_webhookURLRedactedWithoutManageAlerts(t *testing.T) {
	rule := seedWebhookRule(t)
	readOnly := makeReadOnlyUser(t, "webhook-redact@example.com")
	_, token, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "redact-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	for name, auth := range map[string]func(*http.Request){
		"session without manage_alerts": func(r *http.Request) { r.AddCookie(readOnly) },
		"bearer token":                  func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) },
	} {
		for _, path := range []string{"/api/alert-rules", "/api/alert-rules/" + rule.ID} {
			body := getBody(t, path, auth)
			if strings.Contains(body, "SECRETTOKEN") {
				t.Errorf("%s: %s leaked the webhook URL: %s", name, path, body)
			}
			if !strings.Contains(body, "https://203.0.113.1/…abcd") {
				t.Errorf("%s: %s should show the redacted URL: %s", name, path, body)
			}
		}
	}

	// Managers still see the full URL so they can edit it.
	if body := getBody(t, "/api/alert-rules/"+rule.ID, func(r *http.Request) { r.AddCookie(authCookie()) }); !strings.Contains(body, secretWebhook) {
		t.Errorf("manager should see the full webhook URL: %s", body)
	}
}

func TestUpdateAlertRule_echoedRedactedURLKeepsStoredURL(t *testing.T) {
	rule := seedWebhookRule(t)
	b, _ := json.Marshal(map[string]any{"name": "renamed", "webhook_url": "https://203.0.113.1/…abcd"})
	req := httptest.NewRequest(http.MethodPatch, "/api/alert-rules/"+rule.ID, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	got, err := storage.GetAlertRule(context.Background(), testPool, rule.ID)
	if err != nil || got == nil {
		t.Fatalf("get rule: %v", err)
	}
	if got.WebhookURL == nil || *got.WebhookURL != secretWebhook {
		t.Errorf("stored webhook URL changed to %v", got.WebhookURL)
	}
	if got.Name != "renamed" {
		t.Errorf("other fields should still update, name = %q", got.Name)
	}
}

func TestMCP_listAlerts_redactsWebhookURL(t *testing.T) {
	seedWebhookRule(t)
	result := toolCall(t, mcpHandler(), "list_alerts", map[string]any{}, authCookie())
	if text := toolText(t, result); strings.Contains(text, "SECRETTOKEN") {
		t.Errorf("MCP list_alerts leaked the webhook URL: %s", text)
	}
}
