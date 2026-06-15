package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func setupProjectForAlerts(t *testing.T) *storage.Project {
	t.Helper()
	truncateProjects(t)
	p, err := storage.CreateProject(context.Background(), testPool, "alert-test", "Alert Test")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return p
}

func truncateAlerts(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), "TRUNCATE alert_rules CASCADE"); err != nil {
		t.Fatalf("truncate alert_rules: %v", err)
	}
}

func webhookRule(projectID string) *storage.AlertRule {
	url := "https://example.com/webhook"
	return &storage.AlertRule{
		ProjectIDs:   []string{projectID},
		Name:         "test webhook",
		Enabled:      true,
		Trigger:      "new_issue",
		Channel:      "webhook",
		WebhookURL:   &url,
		CooldownMins: 60,
	}
}

func emailRule(projectID string) *storage.AlertRule {
	to := "alert@example.com"
	return &storage.AlertRule{
		ProjectIDs:   []string{projectID},
		Name:         "test email",
		Enabled:      true,
		Trigger:      "event_count",
		Threshold:    intPtr(100),
		WindowMins:   intPtr(5),
		Channel:      "email",
		EmailTo:      &to,
		CooldownMins: 30,
	}
}

func intPtr(n int) *int { return &n }

func TestCreateAlertRule_webhook(t *testing.T) {
	p := setupProjectForAlerts(t)

	rule, err := storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rule.ID == "" {
		t.Error("expected non-empty ID")
	}
	if rule.Name != "test webhook" {
		t.Errorf("name: got %q", rule.Name)
	}
	if rule.Trigger != "new_issue" {
		t.Errorf("trigger: got %q", rule.Trigger)
	}
	if !rule.Enabled {
		t.Error("expected enabled=true")
	}
	if rule.LastFiredAt != nil {
		t.Error("last_fired_at should be nil on creation")
	}
}

func TestCreateAlertRule_email(t *testing.T) {
	p := setupProjectForAlerts(t)

	rule, err := storage.CreateAlertRule(context.Background(), testPool, emailRule(p.ID))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rule.EmailTo == nil || *rule.EmailTo != "alert@example.com" {
		t.Errorf("email_to: got %v", rule.EmailTo)
	}
	if rule.Threshold == nil || *rule.Threshold != 100 {
		t.Errorf("threshold: got %v", rule.Threshold)
	}
}

func TestGetAlertRule(t *testing.T) {
	p := setupProjectForAlerts(t)
	created, _ := storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))

	got, err := storage.GetAlertRule(context.Background(), testPool, created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, got)
	if got.ID != created.ID {
		t.Errorf("ID mismatch")
	}
}

func TestGetAlertRule_notFound(t *testing.T) {
	got, err := storage.GetAlertRule(context.Background(), testPool, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestListAlertRules(t *testing.T) {
	p := setupProjectForAlerts(t)
	truncateAlerts(t)

	storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))
	storage.CreateAlertRule(context.Background(), testPool, emailRule(p.ID))

	rules, err := storage.ListAlertRules(context.Background(), testPool, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2, got %d", len(rules))
	}
}

func TestListAlertRules_filteredByProject(t *testing.T) {
	truncateProjects(t)
	truncateAlerts(t)

	p1, _ := storage.CreateProject(context.Background(), testPool, "proj-a", "Project A")
	p2, _ := storage.CreateProject(context.Background(), testPool, "proj-b", "Project B")

	storage.CreateAlertRule(context.Background(), testPool, webhookRule(p1.ID))
	storage.CreateAlertRule(context.Background(), testPool, emailRule(p2.ID))

	rules, err := storage.ListAlertRules(context.Background(), testPool, p1.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule for project p1, got %d", len(rules))
	}
	if len(rules[0].ProjectIDs) == 0 || rules[0].ProjectIDs[0] != p1.ID {
		t.Errorf("returned rule does not belong to p1: %v", rules[0].ProjectIDs)
	}
}

func TestUpdateAlertRule_disable(t *testing.T) {
	p := setupProjectForAlerts(t)
	created, _ := storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))

	created.Enabled = false
	updated, err := storage.UpdateAlertRule(context.Background(), testPool, created)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Enabled {
		t.Error("expected enabled=false after update")
	}
}

func TestUpdateAlertRule_changeCooldown(t *testing.T) {
	p := setupProjectForAlerts(t)
	created, _ := storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))

	created.CooldownMins = 120
	updated, err := storage.UpdateAlertRule(context.Background(), testPool, created)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.CooldownMins != 120 {
		t.Errorf("cooldown_mins: got %d, want 120", updated.CooldownMins)
	}
}

func TestUpdateAlertRule_notFound(t *testing.T) {
	p := setupProjectForAlerts(t)
	url := "https://x.example.com"
	dummy := &storage.AlertRule{
		ID: "00000000-0000-0000-0000-000000000000", ProjectIDs: []string{p.ID},
		Name: "ghost", Enabled: false, Trigger: "new_issue",
		Channel: "webhook", WebhookURL: &url, CooldownMins: 60,
	}

	got, err := storage.UpdateAlertRule(context.Background(), testPool, dummy)
	if err != nil {
		t.Fatalf("unexpected error for not-found update: %v", err)
	}
	if got != nil {
		t.Error("expected nil for non-existent rule")
	}
}

func TestDeleteAlertRule(t *testing.T) {
	p := setupProjectForAlerts(t)
	created, _ := storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))

	deleted, err := storage.DeleteAlertRule(context.Background(), testPool, created.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Error("expected deleted=true")
	}

	deleted, _ = storage.DeleteAlertRule(context.Background(), testPool, created.ID)
	if deleted {
		t.Error("expected deleted=false on second delete")
	}
}

func TestListDueAlertRules_neverFired(t *testing.T) {
	p := setupProjectForAlerts(t)
	truncateAlerts(t)

	storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))

	due, err := storage.ListDueAlertRules(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(due) != 1 {
		t.Errorf("expected 1 due rule (never fired), got %d", len(due))
	}
}

func TestListDueAlertRules_recentlyFired(t *testing.T) {
	p := setupProjectForAlerts(t)
	truncateAlerts(t)

	created, _ := storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))

	// Simulate a recent fire by setting last_fired_at to now
	testPool.Exec(context.Background(),
		`UPDATE alert_rules SET last_fired_at = NOW() WHERE id = $1`, created.ID)

	due, err := storage.ListDueAlertRules(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Cooldown is 60 mins, fired just now - should not be due
	for _, r := range due {
		if r.ID == created.ID {
			t.Error("rule fired just now should not be due yet")
		}
	}
}

func TestListDueAlertRules_disabled(t *testing.T) {
	p := setupProjectForAlerts(t)
	truncateAlerts(t)

	r := webhookRule(p.ID)
	r.Enabled = false
	storage.CreateAlertRule(context.Background(), testPool, r)

	due, err := storage.ListDueAlertRules(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(due) != 0 {
		t.Errorf("disabled rules must not appear in due list, got %d", len(due))
	}
}

func TestMarkAlertFired(t *testing.T) {
	p := setupProjectForAlerts(t)
	created, _ := storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))

	if created.LastFiredAt != nil {
		t.Fatal("last_fired_at should be nil before firing")
	}

	storage.MarkAlertFired(context.Background(), testPool, created.ID)

	got, _ := storage.GetAlertRule(context.Background(), testPool, created.ID)
	if got.LastFiredAt == nil {
		t.Error("last_fired_at should be set after MarkAlertFired")
	}
}

func TestListEnabledAlertRules(t *testing.T) {
	p := setupProjectForAlerts(t)
	truncateAlerts(t)

	// Create one enabled and one disabled rule
	storage.CreateAlertRule(context.Background(), testPool, webhookRule(p.ID))
	disabled := webhookRule(p.ID)
	disabled.Enabled = false
	storage.CreateAlertRule(context.Background(), testPool, disabled)

	rules, err := storage.ListEnabledAlertRules(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, r := range rules {
		if !r.Enabled {
			t.Error("ListEnabledAlertRules returned a disabled rule")
		}
	}
	if len(rules) < 1 {
		t.Error("expected at least 1 enabled rule")
	}
}

func TestListEnabledAlertRules_empty(t *testing.T) {
	truncateAlerts(t)

	rules, err := storage.ListEnabledAlertRules(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules, got %d", len(rules))
	}
}
