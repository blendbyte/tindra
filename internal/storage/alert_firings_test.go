package storage_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupRuleForFirings(t *testing.T) *storage.AlertRule {
	t.Helper()
	p := setupProjectForAlerts(t)
	rule, err := storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	return rule
}

func truncateFirings(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE alert_firings"); err != nil {
		t.Fatalf("truncate alert_firings: %v", err)
	}
}

func TestCreateAlertFiring_success(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	f := &storage.AlertFiring{
		RuleID:  rule.ID,
		Trigger: "new_issue",
		Channel: "webhook",
		Status:  "success",
		Attempt: 1,
	}
	id, err := storage.CreateAlertFiring(context.Background(), testPool, f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}
}

func TestCreateAlertFiring_pending(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	errMsg := "connection refused"
	code := 503
	nextRetry := time.Now().Add(2 * time.Minute)
	payload, _ := json.Marshal(map[string]any{"trigger": "new_issue"})

	f := &storage.AlertFiring{
		RuleID:      rule.ID,
		Trigger:     "new_issue",
		Channel:     "webhook",
		Status:      "pending",
		StatusCode:  &code,
		Error:       &errMsg,
		Attempt:     1,
		NextRetryAt: &nextRetry,
		Payload:     payload,
	}
	id, err := storage.CreateAlertFiring(context.Background(), testPool, f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}
}

func TestCreateAlertFiring_withItemCount(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	count := 5
	f := &storage.AlertFiring{
		RuleID:    rule.ID,
		Trigger:   "event_count",
		Channel:   "slack",
		Status:    "success",
		ItemCount: &count,
		Attempt:   1,
	}
	id, err := storage.CreateAlertFiring(context.Background(), testPool, f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id == "" {
		t.Error("expected non-empty ID")
	}
}

func TestListAlertFirings_empty(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	firings, err := storage.ListAlertFirings(context.Background(), testPool, rule.ID, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(firings) != 0 {
		t.Errorf("expected 0 firings, got %d", len(firings))
	}
}

func TestListAlertFirings_orderedNewestFirst(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	for i := 0; i < 3; i++ {
		f := &storage.AlertFiring{RuleID: rule.ID, Trigger: "new_issue", Channel: "webhook", Status: "success", Attempt: 1}
		storage.CreateAlertFiring(context.Background(), testPool, f)
	}

	firings, err := storage.ListAlertFirings(context.Background(), testPool, rule.ID, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(firings) != 3 {
		t.Fatalf("expected 3 firings, got %d", len(firings))
	}
	for i := 1; i < len(firings); i++ {
		if firings[i].FiredAt.After(firings[i-1].FiredAt) {
			t.Error("firings should be ordered newest first")
		}
	}
}

func TestListAlertFirings_limit(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	for i := 0; i < 5; i++ {
		f := &storage.AlertFiring{RuleID: rule.ID, Trigger: "new_issue", Channel: "webhook", Status: "success", Attempt: 1}
		storage.CreateAlertFiring(context.Background(), testPool, f)
	}

	firings, err := storage.ListAlertFirings(context.Background(), testPool, rule.ID, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(firings) != 3 {
		t.Errorf("expected 3 (limit), got %d", len(firings))
	}
}

func TestListAlertFirings_onlyForRule(t *testing.T) {
	p := setupProjectForAlerts(t)
	truncateAlerts(t)
	truncateFirings(t)

	rule1, _ := storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))
	rule2, _ := storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))

	storage.CreateAlertFiring(context.Background(), testPool, &storage.AlertFiring{RuleID: rule1.ID, Trigger: "new_issue", Channel: "webhook", Status: "success", Attempt: 1})
	storage.CreateAlertFiring(context.Background(), testPool, &storage.AlertFiring{RuleID: rule2.ID, Trigger: "new_issue", Channel: "webhook", Status: "success", Attempt: 1})

	firings, err := storage.ListAlertFirings(context.Background(), testPool, rule1.ID, 50)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(firings) != 1 {
		t.Errorf("expected 1 firing for rule1, got %d", len(firings))
	}
	if firings[0].RuleID != rule1.ID {
		t.Errorf("wrong rule_id: got %s", firings[0].RuleID)
	}
}

func TestResolveAlertFiring_success(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	errMsg := "timeout"
	nextRetry := time.Now().Add(time.Minute)
	payload, _ := json.Marshal(map[string]any{"x": 1})
	f := &storage.AlertFiring{
		RuleID: rule.ID, Trigger: "new_issue", Channel: "webhook",
		Status: "pending", Error: &errMsg, Attempt: 1, NextRetryAt: &nextRetry, Payload: payload,
	}
	id, _ := storage.CreateAlertFiring(context.Background(), testPool, f)

	code := 200
	storage.ResolveAlertFiring(context.Background(), testPool, id, "success", 2, &code, nil)

	firings, _ := storage.ListAlertFirings(context.Background(), testPool, rule.ID, 1)
	if len(firings) != 1 {
		t.Fatal("expected 1 firing")
	}
	got := firings[0]
	if got.Status != "success" {
		t.Errorf("status: got %q, want success", got.Status)
	}
	if got.Attempt != 2 {
		t.Errorf("attempt: got %d, want 2", got.Attempt)
	}
	if got.NextRetryAt != nil {
		t.Error("next_retry_at should be cleared on resolve")
	}
}

func TestResolveAlertFiring_failed(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	errMsg := "timeout"
	nextRetry := time.Now().Add(time.Minute)
	payload, _ := json.Marshal(map[string]any{"x": 1})
	f := &storage.AlertFiring{
		RuleID: rule.ID, Trigger: "new_issue", Channel: "webhook",
		Status: "pending", Error: &errMsg, Attempt: 2, NextRetryAt: &nextRetry, Payload: payload,
	}
	id, _ := storage.CreateAlertFiring(context.Background(), testPool, f)

	finalErr := "permanent failure"
	storage.ResolveAlertFiring(context.Background(), testPool, id, "failed", 3, nil, &finalErr)

	firings, _ := storage.ListAlertFirings(context.Background(), testPool, rule.ID, 1)
	if len(firings) != 1 {
		t.Fatal("expected 1 firing")
	}
	got := firings[0]
	if got.Status != "failed" {
		t.Errorf("status: got %q, want failed", got.Status)
	}
	if got.Attempt != 3 {
		t.Errorf("attempt: got %d, want 3", got.Attempt)
	}
	if got.Error == nil || *got.Error != "permanent failure" {
		t.Errorf("error: got %v", got.Error)
	}
}

func TestScheduleAlertFiringRetry(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	errMsg := "timeout"
	f := &storage.AlertFiring{
		RuleID: rule.ID, Trigger: "new_issue", Channel: "webhook",
		Status: "pending", Error: &errMsg, Attempt: 1,
	}
	id, _ := storage.CreateAlertFiring(context.Background(), testPool, f)

	newErr := "still failing"
	code := 502
	nextRetry := time.Now().Add(10 * time.Minute)
	payload, _ := json.Marshal(map[string]any{"y": 2})
	storage.ScheduleAlertFiringRetry(context.Background(), testPool, id, 2, nextRetry, &code, &newErr, payload)

	firings, _ := storage.ListAlertFirings(context.Background(), testPool, rule.ID, 1)
	if len(firings) != 1 {
		t.Fatal("expected 1 firing")
	}
	got := firings[0]
	if got.Attempt != 2 {
		t.Errorf("attempt: got %d, want 2", got.Attempt)
	}
	if got.Status != "pending" {
		t.Errorf("status: got %q, want pending", got.Status)
	}
	if got.NextRetryAt == nil {
		t.Error("next_retry_at should be set")
	}
}

func TestListPendingRetries_dueNow(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	past := time.Now().Add(-1 * time.Minute)
	payload, _ := json.Marshal(map[string]any{"trigger": "new_issue"})
	errMsg := "err"
	f := &storage.AlertFiring{
		RuleID: rule.ID, Trigger: "new_issue", Channel: "webhook",
		Status: "pending", Error: &errMsg, Attempt: 1, NextRetryAt: &past, Payload: payload,
	}
	storage.CreateAlertFiring(context.Background(), testPool, f)

	firings, err := storage.ListPendingRetries(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, f := range firings {
		if f.RuleID == rule.ID {
			found = true
			if len(f.Payload) == 0 {
				t.Error("payload should be returned for retry")
			}
		}
	}
	if !found {
		t.Error("expected to find the pending-retry firing")
	}
}

func TestListPendingRetries_notDueYet(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	future := time.Now().Add(10 * time.Minute)
	payload, _ := json.Marshal(map[string]any{"trigger": "new_issue"})
	errMsg := "err"
	f := &storage.AlertFiring{
		RuleID: rule.ID, Trigger: "new_issue", Channel: "webhook",
		Status: "pending", Error: &errMsg, Attempt: 1, NextRetryAt: &future, Payload: payload,
	}
	storage.CreateAlertFiring(context.Background(), testPool, f)

	firings, err := storage.ListPendingRetries(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, f := range firings {
		if f.RuleID == rule.ID {
			t.Error("future retry should not appear in pending retries")
		}
	}
}

func TestListPendingRetries_successNotReturned(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	f := &storage.AlertFiring{
		RuleID: rule.ID, Trigger: "new_issue", Channel: "webhook",
		Status: "success", Attempt: 1,
	}
	storage.CreateAlertFiring(context.Background(), testPool, f)

	firings, _ := storage.ListPendingRetries(context.Background(), testPool)
	for _, f := range firings {
		if f.RuleID == rule.ID {
			t.Error("success firing should not appear in pending retries")
		}
	}
}

func TestAlertFirings_cascadeDeleteOnRuleDelete(t *testing.T) {
	rule := setupRuleForFirings(t)
	truncateFirings(t)

	storage.CreateAlertFiring(context.Background(), testPool, &storage.AlertFiring{
		RuleID: rule.ID, Trigger: "new_issue", Channel: "webhook", Status: "success", Attempt: 1,
	})

	storage.DeleteAlertRule(context.Background(), testPool, rule.ID)

	firings, _ := storage.ListAlertFirings(context.Background(), testPool, rule.ID, 50)
	if len(firings) != 0 {
		t.Errorf("firings should be deleted with rule (ON DELETE CASCADE), got %d", len(firings))
	}
}
