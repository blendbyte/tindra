package alerts

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

// --- conditionMet: uptime_down ---

func TestConditionMet_uptimeDown_noMonitors(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM uptime_monitors WHERE project_id = $1", testProject.ID)

	rule := &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Trigger:    "uptime_down",
		CreatedAt:  time.Now().Add(-time.Hour),
	}

	met, _, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if met {
		t.Error("expected false when no monitors are down")
	}
}

func TestConditionMet_uptimeDown_hasDown(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM uptime_monitors WHERE project_id = $1", testProject.ID)

	testPool.Exec(context.Background(), `
		INSERT INTO uptime_monitors
			(project_id, name, url, method, interval_secs, timeout_secs, expected_codes, status, state, consecutive_failures)
		VALUES ($1, 'down-monitor', 'https://example.com', 'GET', 300, 10, '200-299', 'active', 'down', 2)
	`, testProject.ID)

	rule := &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Trigger:    "uptime_down",
		CreatedAt:  time.Now().Add(-time.Hour),
	}

	met, details, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !met {
		t.Error("expected true when a monitor is down")
	}
	if details["down_count"].(int) < 1 {
		t.Errorf("down_count: got %v", details["down_count"])
	}
}

func TestConditionMet_uptimeDown_pausedMonitorIgnored(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM uptime_monitors WHERE project_id = $1", testProject.ID)

	// Paused monitors with state=down should NOT count
	testPool.Exec(context.Background(), `
		INSERT INTO uptime_monitors
			(project_id, name, url, method, interval_secs, timeout_secs, expected_codes, status, state, consecutive_failures)
		VALUES ($1, 'paused-down', 'https://example.com', 'GET', 300, 10, '200-299', 'paused', 'down', 2)
	`, testProject.ID)

	rule := &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Trigger:    "uptime_down",
		CreatedAt:  time.Now().Add(-time.Hour),
	}

	met, _, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if met {
		t.Error("expected false: paused monitors should not trigger uptime_down")
	}
}

func TestConditionMet_uptimeDown_noProjectFilter(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM uptime_monitors WHERE project_id = $1", testProject.ID)

	testPool.Exec(context.Background(), `
		INSERT INTO uptime_monitors
			(project_id, name, url, method, interval_secs, timeout_secs, expected_codes, status, state, consecutive_failures)
		VALUES ($1, 'global-down', 'https://example.com', 'GET', 300, 10, '200-299', 'active', 'down', 2)
	`, testProject.ID)

	// Rule with no project filter — should match any project
	rule := &storage.AlertRule{
		ProjectIDs: []string{},
		Trigger:    "uptime_down",
		CreatedAt:  time.Now().Add(-time.Hour),
	}

	met, details, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !met {
		t.Error("expected true: global rule should match any project's down monitors")
	}
	if details["down_count"].(int) < 1 {
		t.Errorf("down_count: got %v", details["down_count"])
	}
}

// --- conditionMet: uptime_recovered ---

func TestConditionMet_uptimeRecovered_noMonitors(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM uptime_monitors WHERE project_id = $1", testProject.ID)

	rule := &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Trigger:    "uptime_recovered",
		CreatedAt:  time.Now().Add(-time.Hour),
	}

	met, _, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if met {
		t.Error("expected false when no monitors have recovered")
	}
}

func TestConditionMet_uptimeRecovered_hasRecovered(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM uptime_monitors WHERE project_id = $1", testProject.ID)

	testPool.Exec(context.Background(), `
		INSERT INTO uptime_monitors
			(project_id, name, url, method, interval_secs, timeout_secs, expected_codes, status, state, last_ok_at, went_down_at)
		VALUES ($1, 'recovered-monitor', 'https://example.com', 'GET', 300, 10, '200-299', 'active', 'up', NOW(), NOW() - INTERVAL '1 hour')
	`, testProject.ID)

	rule := &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Trigger:    "uptime_recovered",
		CreatedAt:  time.Now().Add(-time.Hour),
	}

	met, details, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !met {
		t.Error("expected true when a monitor recently recovered")
	}
	if details["recovered_count"].(int) < 1 {
		t.Errorf("recovered_count: got %v", details["recovered_count"])
	}
}

func TestConditionMet_uptimeRecovered_neverDownMonitorIgnored(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM uptime_monitors WHERE project_id = $1", testProject.ID)

	// Monitor is up but has never been down (went_down_at IS NULL)
	testPool.Exec(context.Background(), `
		INSERT INTO uptime_monitors
			(project_id, name, url, method, interval_secs, timeout_secs, expected_codes, status, state, last_ok_at)
		VALUES ($1, 'always-up', 'https://example.com', 'GET', 300, 10, '200-299', 'active', 'up', NOW())
	`, testProject.ID)

	rule := &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Trigger:    "uptime_recovered",
		CreatedAt:  time.Now().Add(-time.Hour),
	}

	met, _, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if met {
		t.Error("expected false: monitor was never down, should not trigger recovery alert")
	}
}

func TestConditionMet_uptimeRecovered_usesLastFiredAt(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM uptime_monitors WHERE project_id = $1", testProject.ID)

	// Monitor recovered 30 minutes ago (went down an hour ago)
	testPool.Exec(context.Background(), `
		INSERT INTO uptime_monitors
			(project_id, name, url, method, interval_secs, timeout_secs, expected_codes, status, state, last_ok_at, went_down_at)
		VALUES ($1, 'old-recovery', 'https://example.com', 'GET', 300, 10, '200-299', 'active', 'up', NOW() - INTERVAL '30 minutes', NOW() - INTERVAL '1 hour')
	`, testProject.ID)

	// LastFiredAt = 10 minutes ago — so the recovery happened BEFORE last firing
	lastFired := time.Now().Add(-10 * time.Minute)
	rule := &storage.AlertRule{
		ProjectIDs:  []string{testProject.ID},
		Trigger:     "uptime_recovered",
		CreatedAt:   time.Now().Add(-2 * time.Hour),
		LastFiredAt: &lastFired,
	}

	met, _, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if met {
		t.Error("expected false: recovery happened before last firing, already notified")
	}
}

// --- enrichPayload: uptime_down ---

func TestEnrichPayload_uptimeDown(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM uptime_monitors WHERE project_id = $1", testProject.ID)

	testPool.Exec(context.Background(), `
		INSERT INTO uptime_monitors
			(project_id, name, url, method, interval_secs, timeout_secs, expected_codes, status, state, consecutive_failures)
		VALUES ($1, 'enrich-down', 'https://example.com', 'GET', 300, 10, '200-299', 'active', 'down', 2)
	`, testProject.ID)

	rule := &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Trigger:    "uptime_down",
	}

	payload := &AlertPayload{Trigger: "uptime_down"}
	testEvaluator(nil).enrichPayload(context.Background(), payload, rule)

	if len(payload.UptimeMonitors) == 0 {
		t.Error("expected uptime_monitors to be populated for uptime_down")
	}
	if payload.UptimeMonitors[0].State != "down" {
		t.Errorf("monitor state: got %q, want down", payload.UptimeMonitors[0].State)
	}
}

// --- enrichPayload: uptime_recovered ---

func TestEnrichPayload_uptimeRecovered(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM uptime_monitors WHERE project_id = $1", testProject.ID)

	testPool.Exec(context.Background(), `
		INSERT INTO uptime_monitors
			(project_id, name, url, method, interval_secs, timeout_secs, expected_codes, status, state, last_ok_at, went_down_at)
		VALUES ($1, 'enrich-recovered', 'https://example.com', 'GET', 300, 10, '200-299', 'active', 'up', NOW(), NOW() - INTERVAL '1 hour')
	`, testProject.ID)

	rule := &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Trigger:    "uptime_recovered",
		CreatedAt:  time.Now().Add(-time.Hour),
	}

	payload := &AlertPayload{Trigger: "uptime_recovered"}
	testEvaluator(nil).enrichPayload(context.Background(), payload, rule)

	if len(payload.UptimeMonitors) == 0 {
		t.Error("expected uptime_monitors to be populated for uptime_recovered")
	}
	if payload.UptimeMonitors[0].State != "up" {
		t.Errorf("monitor state: got %q, want up", payload.UptimeMonitors[0].State)
	}
}

// --- buildAlertSubject: uptime_down / uptime_recovered ---

func TestEmailSubject_uptimeDown_single(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "uptime_down",
		ProjectName: "acme",
		Details:     map[string]any{"down_count": 1},
	})
	if got != "[Tindra] acme - 1 uptime monitor down" {
		t.Errorf("got %q", got)
	}
}

func TestEmailSubject_uptimeDown_multiple(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "uptime_down",
		ProjectName: "acme",
		Details:     map[string]any{"down_count": 3},
	})
	if got != "[Tindra] acme - 3 uptime monitors down" {
		t.Errorf("got %q", got)
	}
}

func TestEmailSubject_uptimeRecovered_single(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "uptime_recovered",
		ProjectName: "acme",
		Details:     map[string]any{"recovered_count": 1},
	})
	if got != "[Tindra] acme - 1 uptime monitor recovered" {
		t.Errorf("got %q", got)
	}
}

func TestEmailSubject_uptimeRecovered_multiple(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "uptime_recovered",
		ProjectName: "acme",
		Details:     map[string]any{"recovered_count": 4},
	})
	if got != "[Tindra] acme - 4 uptime monitors recovered" {
		t.Errorf("got %q", got)
	}
}

// --- fireSlack: uptime trigger labels ---

func TestFireSlack_uptimeTriggerLabels(t *testing.T) {
	tests := []struct {
		trigger string
		want    string
	}{
		{"uptime_down", "Uptime monitor down"},
		{"uptime_recovered", "Uptime monitor recovered"},
	}

	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	for _, tt := range tests {
		t.Run(tt.trigger, func(t *testing.T) {
			e := &Evaluator{pool: testPool, client: srv.Client()}
			url := srv.URL
			rule := &storage.AlertRule{WebhookURL: &url}
			payload := AlertPayload{Trigger: tt.trigger, FiredAt: time.Now(), Details: map[string]any{}}
			if _, err := e.fireSlack(context.Background(), rule, payload); err != nil {
				t.Fatalf("fireSlack: %v", err)
			}
			if !strings.Contains(string(body), tt.want) {
				t.Errorf("expected %q in Slack payload, got: %s", tt.want, body)
			}
		})
	}
}

// --- fireDiscord: uptime trigger labels ---

func TestFireDiscord_uptimeTriggerLabels(t *testing.T) {
	tests := []struct {
		trigger string
		want    string
	}{
		{"uptime_down", "Uptime monitor down"},
		{"uptime_recovered", "Uptime monitor recovered"},
	}

	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	for _, tt := range tests {
		t.Run(tt.trigger, func(t *testing.T) {
			e := &Evaluator{pool: testPool, client: srv.Client()}
			url := srv.URL
			rule := &storage.AlertRule{WebhookURL: &url}
			payload := AlertPayload{Trigger: tt.trigger, FiredAt: time.Now(), Details: map[string]any{}}
			if _, err := e.fireDiscord(context.Background(), rule, payload); err != nil {
				t.Fatalf("fireDiscord: %v", err)
			}
			if !strings.Contains(string(body), tt.want) {
				t.Errorf("expected %q in Discord payload, got: %s", tt.want, body)
			}
		})
	}
}

// --- fireSlack: uptime monitor details in payload ---

func TestFireSlack_uptimeDown_monitorDetails(t *testing.T) {
	code := 503
	errMsg := ""
	wentDown := time.Now().Add(-30 * time.Minute)
	payload := AlertPayload{
		Trigger: "uptime_down",
		FiredAt: time.Now(),
		Details: map[string]any{"down_count": 1},
		UptimeMonitors: []*storage.UptimeMonitor{
			{Name: "Homepage", URL: "https://example.com", State: "down", LastStatusCode: &code, LastError: &errMsg, WentDownAt: &wentDown},
		},
	}

	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client()}
	url := srv.URL
	rule := &storage.AlertRule{WebhookURL: &url}
	if _, err := e.fireSlack(context.Background(), rule, payload); err != nil {
		t.Fatalf("fireSlack: %v", err)
	}
	for _, want := range []string{"Homepage", "https://example.com", "HTTP 503", "Monitors down"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("expected %q in Slack payload, got: %s", want, body)
		}
	}
}

func TestFireSlack_uptimeRecovered_monitorDetails(t *testing.T) {
	wentDown := time.Now().Add(-47 * time.Minute)
	lastOk := time.Now()
	payload := AlertPayload{
		Trigger: "uptime_recovered",
		FiredAt: time.Now(),
		Details: map[string]any{"recovered_count": 1},
		UptimeMonitors: []*storage.UptimeMonitor{
			{Name: "Homepage", URL: "https://example.com", State: "up", WentDownAt: &wentDown, LastOkAt: &lastOk},
		},
	}

	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client()}
	url := srv.URL
	rule := &storage.AlertRule{WebhookURL: &url}
	if _, err := e.fireSlack(context.Background(), rule, payload); err != nil {
		t.Fatalf("fireSlack: %v", err)
	}
	for _, want := range []string{"Homepage", "https://example.com", "down for", "Recovered"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("expected %q in Slack payload, got: %s", want, body)
		}
	}
}

// --- fireDiscord: uptime monitor details ---

func TestFireDiscord_uptimeDown_monitorDetails(t *testing.T) {
	code := 503
	errMsg := ""
	payload := AlertPayload{
		Trigger: "uptime_down",
		FiredAt: time.Now(),
		Details: map[string]any{"down_count": 1},
		UptimeMonitors: []*storage.UptimeMonitor{
			{Name: "Homepage", URL: "https://example.com", State: "down", LastStatusCode: &code, LastError: &errMsg},
		},
	}

	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client()}
	url := srv.URL
	rule := &storage.AlertRule{WebhookURL: &url}
	if _, err := e.fireDiscord(context.Background(), rule, payload); err != nil {
		t.Fatalf("fireDiscord: %v", err)
	}
	for _, want := range []string{"Homepage", "https://example.com", "HTTP 503"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("expected %q in Discord payload, got: %s", want, body)
		}
	}
	// red color for down
	if !strings.Contains(string(body), "15548997") {
		t.Errorf("expected red color (15548997) in Discord down payload, got: %s", body)
	}
}

func TestFireDiscord_uptimeRecovered_greenColor(t *testing.T) {
	payload := AlertPayload{
		Trigger: "uptime_recovered",
		FiredAt: time.Now(),
		Details: map[string]any{"recovered_count": 1},
	}

	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client()}
	url := srv.URL
	rule := &storage.AlertRule{WebhookURL: &url}
	if _, err := e.fireDiscord(context.Background(), rule, payload); err != nil {
		t.Fatalf("fireDiscord: %v", err)
	}
	// green color for recovered
	if !strings.Contains(string(body), "5763719") {
		t.Errorf("expected green color (5763719) in Discord recovered payload, got: %s", body)
	}
}
