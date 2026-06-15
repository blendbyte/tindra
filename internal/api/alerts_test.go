package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func alertHandler() http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

func truncateAlertRules(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE alert_rules CASCADE"); err != nil {
		t.Fatalf("truncate alert_rules: %v", err)
	}
}

func createWebhookRuleBody(name, trigger, webhookURL string) *bytes.Buffer {
	b, _ := json.Marshal(map[string]any{
		"name":        name,
		"trigger":     trigger,
		"channel":     "webhook",
		"webhook_url": webhookURL,
	})
	return bytes.NewBuffer(b)
}

func TestCreateAlertRule_unauthenticated(t *testing.T) {
	body := createWebhookRuleBody("test", "new_issue", "https://example.com")
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", body)
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestCreateAlertRule_webhook(t *testing.T) {
	truncateAlertRules(t)

	body := createWebhookRuleBody("deploy alert", "new_issue", "https://203.0.113.1/alert")
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules", body)
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var rule storage.AlertRule
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rule.ID == "" {
		t.Error("expected non-empty ID")
	}
	if rule.Name != "deploy alert" {
		t.Errorf("name: got %q", rule.Name)
	}
	if !rule.Enabled {
		t.Error("expected enabled=true by default")
	}
}

func TestCreateAlertRule_eventCount(t *testing.T) {
	truncateAlertRules(t)

	b, _ := json.Marshal(map[string]any{
		"name":        "volume spike",
		"trigger":     "event_count",
		"threshold":   500,
		"window_mins": 10,
		"channel":     "webhook",
		"webhook_url": "https://example.com/wh",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateAlertRule_invalidTrigger(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name": "bad", "trigger": "nonsense", "channel": "webhook", "webhook_url": "https://x.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateAlertRule_missingWebhookURL(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name": "no url", "trigger": "new_or_regressed", "channel": "webhook",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestCreateAlertRule_eventCountMissingThreshold(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name": "missing threshold", "trigger": "event_count",
		"channel": "webhook", "webhook_url": "https://x.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestListAlertRules(t *testing.T) {
	truncateAlertRules(t)

	storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "a", Enabled: true,
		Trigger: "new_issue", Channel: "webhook",
		WebhookURL: strPtr("https://a.example.com"), CooldownMins: 60,
	})

	req := httptest.NewRequest(http.MethodGet, "/api/alert-rules", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var resp struct {
		Rules []*storage.AlertRule `json:"rules"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Rules) != 1 {
		t.Errorf("expected 1, got %d", len(resp.Rules))
	}
}

func TestGetAlertRule(t *testing.T) {
	truncateAlertRules(t)

	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "get me", Enabled: true,
		Trigger: "new_or_regressed", Channel: "webhook",
		WebhookURL: strPtr("https://203.0.113.1/hook"), CooldownMins: 60,
	})

	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("/api/alert-rules/%s", created.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got storage.AlertRule
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID mismatch")
	}
}

func TestUpdateAlertRule_disable(t *testing.T) {
	truncateAlertRules(t)

	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "disable me", Enabled: true,
		Trigger: "new_issue", Channel: "webhook",
		WebhookURL: strPtr("https://203.0.113.1/hook"), CooldownMins: 60,
	})

	b, _ := json.Marshal(map[string]any{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/alert-rules/%s", created.ID),
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got storage.AlertRule
	json.NewDecoder(rec.Body).Decode(&got)
	if got.Enabled {
		t.Error("expected enabled=false after patch")
	}
	// Other fields unchanged
	if got.Name != "disable me" {
		t.Errorf("name should not change, got %q", got.Name)
	}
}

func TestUpdateAlertRule_notFound(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"enabled": false})
	req := httptest.NewRequest(http.MethodPatch,
		"/api/alert-rules/00000000-0000-0000-0000-000000000000",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestDeleteAlertRule(t *testing.T) {
	truncateAlertRules(t)

	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "del", Enabled: true,
		Trigger: "new_or_regressed", Channel: "webhook",
		WebhookURL: strPtr("https://203.0.113.1/hook"), CooldownMins: 60,
	})

	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/alert-rules/%s", created.ID), nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", rec.Code)
	}
}

func TestDeleteAlertRule_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete,
		"/api/alert-rules/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestGetAlertRule_notFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet,
		"/api/alert-rules/00000000-0000-0000-0000-000000000000", nil)
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown rule, got %d", rec.Code)
	}
}

func TestCreateAlertRule_badBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

func TestCreateAlertRule_emailChannel(t *testing.T) {
	truncateAlertRules(t)

	b, _ := json.Marshal(map[string]any{
		"name":     "email alert",
		"trigger":  "new_issue",
		"channel":  "email",
		"email_to": "ops@example.com",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for email channel rule, got %d: %s", rec.Code, rec.Body.String())
	}
	var rule storage.AlertRule
	json.NewDecoder(rec.Body).Decode(&rule)
	if rule.Channel != "email" {
		t.Errorf("channel: got %q", rule.Channel)
	}
}

func TestCreateAlertRule_invalidChannel(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name": "bad channel", "trigger": "new_or_regressed", "channel": "sms",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid channel, got %d", rec.Code)
	}
}

func TestCreateAlertRule_slackChannel(t *testing.T) {
	truncateAlertRules(t)

	b, _ := json.Marshal(map[string]any{
		"name":        "slack alert",
		"trigger":     "new_issue",
		"channel":     "slack",
		"webhook_url": "https://203.0.113.1/slack",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for slack channel, got %d: %s", rec.Code, rec.Body.String())
	}
	var rule storage.AlertRule
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rule.Channel != "slack" {
		t.Errorf("channel: got %q, want slack", rule.Channel)
	}
	if rule.WebhookURL == nil || *rule.WebhookURL == "" {
		t.Error("webhook_url should be stored for slack channel")
	}
}

func TestCreateAlertRule_slackMissingWebhookURL(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name": "slack no url", "trigger": "new_or_regressed", "channel": "slack",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for slack without webhook_url, got %d", rec.Code)
	}
}

func TestCreateAlertRule_slackEmptyWebhookURL(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name": "slack empty url", "trigger": "new_or_regressed", "channel": "slack", "webhook_url": "",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for slack with empty webhook_url, got %d", rec.Code)
	}
}

func TestCreateAlertRule_discordChannel(t *testing.T) {
	truncateAlertRules(t)

	b, _ := json.Marshal(map[string]any{
		"name":        "discord alert",
		"trigger":     "new_issue",
		"channel":     "discord",
		"webhook_url": "https://203.0.113.1/discord",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 for discord channel, got %d: %s", rec.Code, rec.Body.String())
	}
	var rule storage.AlertRule
	if err := json.NewDecoder(rec.Body).Decode(&rule); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rule.Channel != "discord" {
		t.Errorf("channel: got %q, want discord", rule.Channel)
	}
	if rule.WebhookURL == nil || *rule.WebhookURL == "" {
		t.Error("webhook_url should be stored for discord channel")
	}
}

func TestCreateAlertRule_discordMissingWebhookURL(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name": "discord no url", "trigger": "new_or_regressed", "channel": "discord",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for discord without webhook_url, got %d", rec.Code)
	}
}

func TestCreateAlertRule_discordEmptyWebhookURL(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name": "discord empty url", "trigger": "new_or_regressed", "channel": "discord", "webhook_url": "",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for discord with empty webhook_url, got %d", rec.Code)
	}
}

func TestUpdateAlertRule_switchToDiscord(t *testing.T) {
	truncateAlertRules(t)

	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "to-discord", Enabled: true,
		Trigger: "new_or_regressed", Channel: "email",
		EmailTo: strPtr("x@example.com"), CooldownMins: 60,
	})

	b, _ := json.Marshal(map[string]any{
		"channel":     "discord",
		"webhook_url": "https://203.0.113.1/discord",
	})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/alert-rules/%s", created.ID),
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 switching channel to discord, got %d: %s", rec.Code, rec.Body.String())
	}
	var got storage.AlertRule
	json.NewDecoder(rec.Body).Decode(&got)
	if got.Channel != "discord" {
		t.Errorf("channel: got %q, want discord", got.Channel)
	}
}

func TestUpdateAlertRule_switchToSlack(t *testing.T) {
	truncateAlertRules(t)

	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "to-slack", Enabled: true,
		Trigger: "new_or_regressed", Channel: "email",
		EmailTo: strPtr("x@example.com"), CooldownMins: 60,
	})

	b, _ := json.Marshal(map[string]any{
		"channel":     "slack",
		"webhook_url": "https://203.0.113.1/slack",
	})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/alert-rules/%s", created.ID),
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 switching channel to slack, got %d: %s", rec.Code, rec.Body.String())
	}
	var got storage.AlertRule
	json.NewDecoder(rec.Body).Decode(&got)
	if got.Channel != "slack" {
		t.Errorf("channel: got %q, want slack", got.Channel)
	}
}

func TestCreateAlertRule_missingName(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"trigger": "new_or_regressed", "channel": "webhook", "webhook_url": "https://203.0.113.1/hook",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing name, got %d", rec.Code)
	}
}

func TestCreateAlertRule_eventCountZeroThreshold(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name": "zero threshold", "trigger": "event_count",
		"threshold": 0, "window_mins": 10,
		"channel": "webhook", "webhook_url": "https://203.0.113.1/hook",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for zero threshold, got %d", rec.Code)
	}
}

func TestUpdateAlertRule_badBody(t *testing.T) {
	truncateAlertRules(t)

	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "patch me", Enabled: true,
		Trigger: "new_or_regressed", Channel: "webhook",
		WebhookURL: strPtr("https://203.0.113.1/hook"), CooldownMins: 60,
	})

	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/alert-rules/%s", created.ID),
		bytes.NewBufferString("not json"))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad body, got %d", rec.Code)
	}
}

func TestUpdateAlertRule_validationFailAfterPatch(t *testing.T) {
	truncateAlertRules(t)

	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "validate me", Enabled: true,
		Trigger: "new_or_regressed", Channel: "webhook",
		WebhookURL: strPtr("https://203.0.113.1/hook"), CooldownMins: 60,
	})

	// Patch trigger to "event_count" without providing threshold/window_mins → validation fails
	b, _ := json.Marshal(map[string]any{"trigger": "event_count"})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/alert-rules/%s", created.ID),
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when patched rule fails validation, got %d", rec.Code)
	}
}

func TestCreateAlertRule_emailMissingEmailTo(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name": "email no to", "trigger": "new_or_regressed", "channel": "email",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for email channel without email_to, got %d", rec.Code)
	}
}

func TestUpdateAlertRule_allPatchFields(t *testing.T) {
	truncateAlertRules(t)

	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "full-patch", Enabled: true,
		Trigger: "new_issue", Channel: "webhook",
		WebhookURL: strPtr("https://orig.example.com"), CooldownMins: 60,
	})

	threshold := 100
	windowMins := 5
	cooldown := 30
	b, _ := json.Marshal(map[string]any{
		"name":          "updated name",
		"enabled":       false,
		"trigger":       "event_count",
		"threshold":     threshold,
		"window_mins":   windowMins,
		"channel":       "email",
		"email_to":      "ops@example.com",
		"cooldown_mins": cooldown,
	})
	req := httptest.NewRequest(http.MethodPatch,
		fmt.Sprintf("/api/alert-rules/%s", created.ID),
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got storage.AlertRule
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "updated name" {
		t.Errorf("name: got %q", got.Name)
	}
	if got.Channel != "email" {
		t.Errorf("channel: got %q", got.Channel)
	}
	if got.CooldownMins != 30 {
		t.Errorf("cooldown_mins: got %d", got.CooldownMins)
	}
}

func strPtr(s string) *string { return &s }

func TestCreateAlertRule_emailEmptyEmailTo(t *testing.T) {
	b, _ := json.Marshal(map[string]any{
		"name": "empty to", "trigger": "new_or_regressed", "channel": "email", "email_to": "",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/alert-rules",
		bytes.NewBuffer(b))
	req.AddCookie(authCookie())
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	alertHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for email channel with empty email_to, got %d", rec.Code)
	}
}
