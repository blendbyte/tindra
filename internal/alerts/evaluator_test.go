package alerts

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

type mockEmailSender struct {
	lastMsg EmailMessage
}

func (m *mockEmailSender) Send(_ context.Context, msg EmailMessage) error {
	m.lastMsg = msg
	return nil
}

func testEvaluator(email EmailSender) *Evaluator {
	return &Evaluator{
		pool:   testPool,
		client: &http.Client{Timeout: 5 * time.Second},
		email:  email,
	}
}

func webhookRule(projectID, url string) *storage.AlertRule {
	return &storage.AlertRule{
		ProjectIDs: []string{projectID}, Name: "wh rule", Enabled: true,
		Trigger: "new_issue", Channel: "webhook", WebhookURL: &url, CooldownMins: 60,
	}
}

// --- NewEvaluator and Run ---

func TestNewEvaluator(t *testing.T) {
	e := NewEvaluator(testPool, nil, "", false)
	require.NotNil(t, e)
	if e.pool != testPool {
		t.Error("expected pool to be set")
	}
	require.NotNil(t, e.client)
}

func TestEvaluator_Run_stopsOnCancel(t *testing.T) {
	e := NewEvaluator(testPool, nil, "", false)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		e.Run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("Run did not return after context cancellation")
	}
}

// --- conditionMet ---

func TestConditionMet_newIssue_noIssues(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM issues WHERE project_id = $1", testProject.ID)

	rule, _ := storage.CreateAlertRule(context.Background(), testPool, webhookRule(testProject.ID, "https://x.example.com"))
	rule.Trigger = "new_issue"

	met, _, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if met {
		t.Error("expected false when no new issues exist")
	}
}

func TestConditionMet_newIssue_hasNew(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM issues WHERE project_id = $1", testProject.ID)

	rule, _ := storage.CreateAlertRule(context.Background(), testPool, webhookRule(testProject.ID, "https://x.example.com"))
	rule.Trigger = "new_issue"

	testPool.Exec(context.Background(), `
		INSERT INTO issues (project_id, fingerprint, title, level, first_seen, last_seen)
		VALUES ($1, 'fp-cond-new', 'new issue', 'error', NOW(), NOW())
	`, testProject.ID)

	met, details, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !met {
		t.Error("expected true when a new issue exists after rule creation")
	}
	if details["new_issue_count"].(int) < 1 {
		t.Errorf("new_issue_count: got %v", details["new_issue_count"])
	}
}

func TestConditionMet_eventCount_underThreshold(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM events WHERE project_id = $1", testProject.ID)

	threshold := 10
	window := 60
	rule := &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Trigger: "event_count",
		Threshold: &threshold, WindowMins: &window,
	}

	// Insert fewer events than threshold
	for i := 0; i < 5; i++ {
		testPool.Exec(context.Background(), `
			INSERT INTO events (project_id, timestamp, payload)
			VALUES ($1, NOW(), '{"level":"error"}'::jsonb)
		`, testProject.ID)
	}

	met, _, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if met {
		t.Error("expected false when event count is below threshold")
	}
}

func TestConditionMet_eventCount_overThreshold(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM events WHERE project_id = $1", testProject.ID)

	threshold := 3
	window := 60
	rule := &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Trigger: "event_count",
		Threshold: &threshold, WindowMins: &window,
	}

	for i := 0; i < 5; i++ {
		testPool.Exec(context.Background(), `
			INSERT INTO events (project_id, timestamp, payload)
			VALUES ($1, NOW(), '{"level":"error"}'::jsonb)
		`, testProject.ID)
	}

	met, details, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !met {
		t.Error("expected true when event count exceeds threshold")
	}
	if details["event_count"].(int) < threshold {
		t.Errorf("event_count detail: got %v", details["event_count"])
	}
	if details["threshold"].(int) != threshold {
		t.Errorf("threshold detail: got %v", details["threshold"])
	}
}

// --- fireWebhook ---

func TestFireWebhook_success(t *testing.T) {
	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client()}
	url := srv.URL
	rule := &storage.AlertRule{ID: "r1", ProjectIDs: []string{testProject.ID}, WebhookURL: &url}
	payload := AlertPayload{RuleID: "r1", RuleName: "test", FiredAt: time.Now(), Details: map[string]any{}}

	if err := e.fireWebhook(context.Background(), rule, payload); err != nil {
		t.Fatalf("fireWebhook: %v", err)
	}

	var got AlertPayload
	if err := json.Unmarshal(received, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.RuleID != "r1" {
		t.Errorf("rule_id: got %q", got.RuleID)
	}
}

func TestFireWebhook_nilURL(t *testing.T) {
	e := testEvaluator(nil)
	rule := &storage.AlertRule{WebhookURL: nil}
	payload := AlertPayload{}
	if err := e.fireWebhook(context.Background(), rule, payload); err == nil {
		t.Error("expected error for nil webhook_url")
	}
}

func TestFireWebhook_emptyURL(t *testing.T) {
	e := testEvaluator(nil)
	empty := ""
	rule := &storage.AlertRule{WebhookURL: &empty}
	if err := e.fireWebhook(context.Background(), rule, AlertPayload{}); err == nil {
		t.Error("expected error for empty webhook_url")
	}
}

func TestFireWebhook_non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client()}
	url := srv.URL
	rule := &storage.AlertRule{WebhookURL: &url}

	if err := e.fireWebhook(context.Background(), rule, AlertPayload{}); err == nil {
		t.Error("expected error for 500 response")
	}
}

// --- fireSlack ---

func TestFireSlack_success(t *testing.T) {
	var body []byte
	var ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		ct = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client()}
	url := srv.URL
	rule := &storage.AlertRule{ID: "r-slack", ProjectIDs: []string{testProject.ID}, WebhookURL: &url}
	payload := AlertPayload{
		RuleID: "r-slack", RuleName: "slack test",
		Trigger: "new_or_regressed", FiredAt: time.Now(), Details: map[string]any{},
	}

	if err := e.fireSlack(context.Background(), rule, payload); err != nil {
		t.Fatalf("fireSlack: %v", err)
	}
	if ct != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}

	var msg struct {
		Text   string `json:"text"`
		Blocks []struct {
			Type string `json:"type"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if msg.Text == "" {
		t.Error("text fallback should not be empty")
	}
	if len(msg.Blocks) < 2 {
		t.Errorf("expected at least 2 blocks, got %d", len(msg.Blocks))
	}
	if msg.Blocks[0].Type != "section" {
		t.Errorf("first block type: got %q, want section", msg.Blocks[0].Type)
	}
	last := msg.Blocks[len(msg.Blocks)-1]
	if last.Type != "context" {
		t.Errorf("last block type: got %q, want context", last.Type)
	}
}

func TestFireSlack_withIssues(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client(), publicURL: "https://tindra.example.com"}
	url := srv.URL
	rule := &storage.AlertRule{WebhookURL: &url}
	payload := AlertPayload{
		RuleName: "issue alert",
		Trigger:  "new_issue",
		FiredAt:  time.Now(),
		Details:  map[string]any{"new_issue_count": 2},
		Issues: []*storage.Issue{
			{ID: "i1", Title: "TypeError: cannot read undefined"},
			{ID: "i2", Title: "ReferenceError: foo is not defined"},
		},
	}

	if err := e.fireSlack(context.Background(), rule, payload); err != nil {
		t.Fatalf("fireSlack: %v", err)
	}

	if !strings.Contains(string(body), "TypeError") {
		t.Error("expected issue title in slack payload")
	}
	if !strings.Contains(string(body), "ReferenceError") {
		t.Error("expected second issue title in slack payload")
	}
	if !strings.Contains(string(body), "https://tindra.example.com/issues/i1") {
		t.Error("expected issue link in slack payload")
	}
}

func TestFireSlack_nilURL(t *testing.T) {
	e := testEvaluator(nil)
	rule := &storage.AlertRule{WebhookURL: nil}
	if err := e.fireSlack(context.Background(), rule, AlertPayload{}); err == nil {
		t.Error("expected error for nil webhook_url")
	}
}

func TestFireSlack_emptyURL(t *testing.T) {
	e := testEvaluator(nil)
	empty := ""
	rule := &storage.AlertRule{WebhookURL: &empty}
	if err := e.fireSlack(context.Background(), rule, AlertPayload{}); err == nil {
		t.Error("expected error for empty webhook_url")
	}
}

func TestFireSlack_non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		w.WriteHeader(403)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client()}
	url := srv.URL
	rule := &storage.AlertRule{WebhookURL: &url}
	if err := e.fireSlack(context.Background(), rule, AlertPayload{FiredAt: time.Now()}); err == nil {
		t.Error("expected error for 403 response")
	}
}

func TestFireSlack_triggerLabels(t *testing.T) {
	tests := []struct {
		trigger string
		want    string
	}{
		{"new_issue", "New issue"},
		{"regressed", "Regression"},
		{"new_or_regressed", "New issue or regression"},
		{"event_count", "Event count"},
		{"unknown", "unknown"},
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
			if err := e.fireSlack(context.Background(), rule, payload); err != nil {
				t.Fatalf("fireSlack: %v", err)
			}
			if !strings.Contains(string(body), tt.want) {
				t.Errorf("expected %q in payload, got: %s", tt.want, body)
			}
		})
	}
}

func TestEvaluate_firesSlack(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE alert_rules CASCADE")

	fired := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		fired = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	url := srv.URL
	threshold, window := 1, 60
	storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "slack rule", Enabled: true,
		Trigger: "event_count", Threshold: &threshold, WindowMins: &window,
		Channel: "slack", WebhookURL: &url, CooldownMins: 60,
	})
	testPool.Exec(context.Background(), `INSERT INTO events (project_id, timestamp, payload) VALUES ($1, NOW(), '{"level":"error"}'::jsonb)`, testProject.ID)

	e := &Evaluator{pool: testPool, client: srv.Client()}
	e.evaluate(context.Background())

	if !fired {
		t.Error("expected Slack webhook to be called during evaluate")
	}
}

// --- fireDiscord ---

func TestFireDiscord_success(t *testing.T) {
	var body []byte
	var ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		ct = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client()}
	url := srv.URL
	rule := &storage.AlertRule{ID: "r-discord", ProjectIDs: []string{testProject.ID}, WebhookURL: &url}
	payload := AlertPayload{
		RuleID: "r-discord", RuleName: "discord test",
		Trigger: "new_or_regressed", FiredAt: time.Now(), Details: map[string]any{},
	}

	if err := e.fireDiscord(context.Background(), rule, payload); err != nil {
		t.Fatalf("fireDiscord: %v", err)
	}
	if ct != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}

	var msg struct {
		Embeds []struct {
			Title  string `json:"title"`
			Color  int    `json:"color"`
			Fields []struct {
				Name   string `json:"name"`
				Value  string `json:"value"`
				Inline bool   `json:"inline"`
			} `json:"fields"`
			Footer struct {
				Text string `json:"text"`
			} `json:"footer"`
		} `json:"embeds"`
	}
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(msg.Embeds) != 1 {
		t.Fatalf("expected 1 embed, got %d", len(msg.Embeds))
	}
	emb := msg.Embeds[0]
	if emb.Title == "" {
		t.Error("embed title should not be empty")
	}
	if emb.Color != 15548997 {
		t.Errorf("embed color: got %d, want 15548997", emb.Color)
	}
	if len(emb.Fields) < 2 {
		t.Errorf("expected at least 2 fields, got %d", len(emb.Fields))
	}
	if emb.Fields[0].Name != "Project" {
		t.Errorf("first field name: got %q, want Project", emb.Fields[0].Name)
	}
	if emb.Fields[1].Name != "Trigger" {
		t.Errorf("second field name: got %q, want Trigger", emb.Fields[1].Name)
	}
	if emb.Footer.Text == "" {
		t.Error("embed footer text should not be empty")
	}
}

func TestFireDiscord_withIssues(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client(), publicURL: "https://tindra.example.com"}
	url := srv.URL
	rule := &storage.AlertRule{WebhookURL: &url}
	payload := AlertPayload{
		RuleName: "issue alert",
		Trigger:  "new_issue",
		FiredAt:  time.Now(),
		Details:  map[string]any{"new_issue_count": 2},
		Issues: []*storage.Issue{
			{ID: "i1", Title: "TypeError: cannot read undefined"},
			{ID: "i2", Title: "ReferenceError: foo is not defined"},
		},
	}

	if err := e.fireDiscord(context.Background(), rule, payload); err != nil {
		t.Fatalf("fireDiscord: %v", err)
	}
	if !strings.Contains(string(body), "TypeError") {
		t.Error("expected first issue title in discord payload")
	}
	if !strings.Contains(string(body), "ReferenceError") {
		t.Error("expected second issue title in discord payload")
	}
	if !strings.Contains(string(body), "https://tindra.example.com/issues/i1") {
		t.Error("expected issue link in discord payload")
	}
}

func TestFireDiscord_nilURL(t *testing.T) {
	e := testEvaluator(nil)
	rule := &storage.AlertRule{WebhookURL: nil}
	if err := e.fireDiscord(context.Background(), rule, AlertPayload{}); err == nil {
		t.Error("expected error for nil webhook_url")
	}
}

func TestFireDiscord_emptyURL(t *testing.T) {
	e := testEvaluator(nil)
	empty := ""
	rule := &storage.AlertRule{WebhookURL: &empty}
	if err := e.fireDiscord(context.Background(), rule, AlertPayload{}); err == nil {
		t.Error("expected error for empty webhook_url")
	}
}

func TestFireDiscord_non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		w.WriteHeader(401)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client()}
	url := srv.URL
	rule := &storage.AlertRule{WebhookURL: &url}
	if err := e.fireDiscord(context.Background(), rule, AlertPayload{FiredAt: time.Now()}); err == nil {
		t.Error("expected error for 401 response")
	}
}

func TestFireDiscord_triggerLabels(t *testing.T) {
	tests := []struct {
		trigger string
		want    string
	}{
		{"new_issue", "New issue"},
		{"regressed", "Regression"},
		{"new_or_regressed", "New issue or regression"},
		{"event_count", "Event count"},
		{"unknown", "unknown"},
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
			if err := e.fireDiscord(context.Background(), rule, payload); err != nil {
				t.Fatalf("fireDiscord: %v", err)
			}
			if !strings.Contains(string(body), tt.want) {
				t.Errorf("expected %q in payload, got: %s", tt.want, body)
			}
		})
	}
}

func TestEvaluate_firesDiscord(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE alert_rules CASCADE")

	fired := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		fired = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	url := srv.URL
	threshold, window := 1, 60
	storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "discord rule", Enabled: true,
		Trigger: "event_count", Threshold: &threshold, WindowMins: &window,
		Channel: "discord", WebhookURL: &url, CooldownMins: 60,
	})
	testPool.Exec(context.Background(), `INSERT INTO events (project_id, timestamp, payload) VALUES ($1, NOW(), '{"level":"error"}'::jsonb)`, testProject.ID)

	e := &Evaluator{pool: testPool, client: srv.Client()}
	e.evaluate(context.Background())

	if !fired {
		t.Error("expected Discord webhook to be called during evaluate")
	}
}

// --- fireEmail ---

func TestFireEmail_nilSender(t *testing.T) {
	to := "user@example.com"
	rule := &storage.AlertRule{EmailTo: &to}
	err := testEvaluator(nil).fireEmail(context.Background(), rule, AlertPayload{FiredAt: time.Now()})
	if err == nil {
		t.Error("expected error when email sender is nil")
	}
}

func TestFireEmail_nilEmailTo(t *testing.T) {
	rule := &storage.AlertRule{EmailTo: nil}
	err := testEvaluator(&mockEmailSender{}).fireEmail(context.Background(), rule, AlertPayload{FiredAt: time.Now()})
	if err == nil {
		t.Error("expected error when email_to is nil")
	}
}

func TestFireEmail_emptyEmailTo(t *testing.T) {
	empty := ""
	rule := &storage.AlertRule{EmailTo: &empty}
	err := testEvaluator(&mockEmailSender{}).fireEmail(context.Background(), rule, AlertPayload{FiredAt: time.Now()})
	if err == nil {
		t.Error("expected error when email_to is empty string")
	}
}

func TestFireEmail_sends(t *testing.T) {
	mock := &mockEmailSender{}
	to := "alert@example.com"
	rule := &storage.AlertRule{Name: "email rule", EmailTo: &to}
	payload := AlertPayload{
		RuleName: "email rule", Trigger: "new_or_regressed",
		FiredAt: time.Now(), Details: map[string]any{},
	}

	if err := testEvaluator(mock).fireEmail(context.Background(), rule, payload); err != nil {
		t.Fatalf("fireEmail: %v", err)
	}
	if mock.lastMsg.To != to {
		t.Errorf("To: got %q, want %q", mock.lastMsg.To, to)
	}
	if mock.lastMsg.Subject == "" {
		t.Error("Subject should not be empty")
	}
	if mock.lastMsg.Text == "" {
		t.Error("Text body should not be empty")
	}
}

// --- evaluate end-to-end ---

func TestEvaluate_firesWebhook(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE alert_rules CASCADE")

	fired := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		fired = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	url := srv.URL
	threshold, window := 1, 60
	storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "fire me", Enabled: true,
		Trigger: "event_count", Threshold: &threshold, WindowMins: &window,
		Channel: "webhook", WebhookURL: &url, CooldownMins: 60,
	})
	testPool.Exec(context.Background(), `INSERT INTO events (project_id, timestamp, payload) VALUES ($1, NOW(), '{"level":"error"}'::jsonb)`, testProject.ID)

	e := &Evaluator{pool: testPool, client: srv.Client()}
	e.evaluate(context.Background())

	if !fired {
		t.Error("expected webhook to be called")
	}
}

func TestEvaluate_marksFiredAt(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE alert_rules CASCADE")

	url := "https://x.example.com/wh"
	threshold, window := 1, 60
	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "mark me", Enabled: true,
		Trigger: "event_count", Threshold: &threshold, WindowMins: &window,
		Channel: "webhook", WebhookURL: &url, CooldownMins: 60,
	})
	if created.LastFiredAt != nil {
		t.Fatal("last_fired_at should be nil before evaluate")
	}

	// Use a mock server so the delivery doesn't fail
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	*created.WebhookURL = srv.URL

	// Update rule in DB to point to the test server
	storage.UpdateAlertRule(context.Background(), testPool, created)
	testPool.Exec(context.Background(), `INSERT INTO events (project_id, timestamp, payload) VALUES ($1, NOW(), '{"level":"error"}'::jsonb)`, testProject.ID)

	e := &Evaluator{pool: testPool, client: srv.Client()}
	e.evaluate(context.Background())

	got, _ := storage.GetAlertRule(context.Background(), testPool, created.ID)
	if got.LastFiredAt == nil {
		t.Error("expected last_fired_at to be set after evaluate")
	}
}

func TestEvaluate_conditionNotMet(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE alert_rules CASCADE")
	testPool.Exec(context.Background(), "DELETE FROM events WHERE project_id = $1", testProject.ID)

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		callCount++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	url := srv.URL
	threshold := 1000
	window := 60
	storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "high threshold", Enabled: true,
		Trigger: "event_count", Channel: "webhook", WebhookURL: &url,
		Threshold: &threshold, WindowMins: &window, CooldownMins: 60,
	})

	e := &Evaluator{pool: testPool, client: srv.Client()}
	e.evaluate(context.Background())

	if callCount != 0 {
		t.Errorf("expected no webhook call when condition not met, got %d", callCount)
	}
}

func TestConditionMet_unknownTrigger(t *testing.T) {
	rule := &storage.AlertRule{
		ProjectIDs: []string{testProject.ID},
		Trigger:    "unknown_trigger",
	}
	met, details, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if met {
		t.Error("expected false for unknown trigger")
	}
	if details != nil {
		t.Errorf("expected nil details for unknown trigger, got %v", details)
	}
}

func TestEmailBody_withDetails(t *testing.T) {
	payload := AlertPayload{
		RuleName:  "spike alert",
		ProjectID: "proj-xyz",
		Trigger:   "event_count",
		FiredAt:   time.Now(),
		Details:   map[string]any{"event_count": 42, "threshold": 10},
	}
	html, _, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "spike alert") {
		t.Error("expected rule name in body")
	}
}

func TestEmailSubject(t *testing.T) {
	payload := AlertPayload{RuleName: "my rule"}
	got := buildAlertSubject(payload)
	if got != "[Tindra] my rule" {
		t.Errorf("subject: got %q", got)
	}
}

func TestConditionMet_newIssue_withLastFiredAt(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM issues WHERE project_id = $1", testProject.ID)

	rule, _ := storage.CreateAlertRule(context.Background(), testPool, webhookRule(testProject.ID, "https://x.example.com"))
	rule.Trigger = "new_issue"

	// Set LastFiredAt so conditionMet uses it as the "since" boundary instead of CreatedAt.
	lastFired := time.Now().Add(-1 * time.Hour)
	rule.LastFiredAt = &lastFired

	met, _, err := testEvaluator(nil).conditionMet(context.Background(), rule)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if met {
		t.Error("expected false when no new issues since last_fired_at")
	}
}

func TestEvaluate_firesEmail(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE alert_rules CASCADE")

	emailTo := "alert@example.com"
	threshold, window := 1, 60
	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "email fire", Enabled: true,
		Trigger: "event_count", Threshold: &threshold, WindowMins: &window,
		Channel: "email", EmailTo: &emailTo, CooldownMins: 60,
	})
	testPool.Exec(context.Background(), `INSERT INTO events (project_id, timestamp, payload) VALUES ($1, NOW(), '{"level":"error"}'::jsonb)`, testProject.ID)

	// Nil email sender → fireEmail returns error → covers deliveryErr != nil path in fire().
	e := testEvaluator(nil)
	e.evaluate(context.Background())

	// Rule should still be marked as fired even when delivery fails.
	got, _ := storage.GetAlertRule(context.Background(), testPool, created.ID)
	if got.LastFiredAt == nil {
		t.Error("expected last_fired_at to be set even when email delivery fails")
	}
}

func TestEvaluate_cooldownPreventsReFire(t *testing.T) {
	testPool.Exec(context.Background(), "TRUNCATE alert_rules CASCADE")

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		callCount++
		w.WriteHeader(200)
	}))
	defer srv.Close()

	url := srv.URL
	threshold, window := 1, 60
	created, _ := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "cooldown rule", Enabled: true,
		Trigger: "event_count", Threshold: &threshold, WindowMins: &window,
		Channel: "webhook", WebhookURL: &url, CooldownMins: 60,
	})
	testPool.Exec(context.Background(), `INSERT INTO events (project_id, timestamp, payload) VALUES ($1, NOW(), '{"level":"error"}'::jsonb)`, testProject.ID)

	e := &Evaluator{pool: testPool, client: srv.Client()}
	e.evaluate(context.Background())

	// Manually set last_fired_at to now so cooldown is active
	testPool.Exec(context.Background(),
		"UPDATE alert_rules SET last_fired_at = NOW() WHERE id = $1", created.ID)

	e.evaluate(context.Background())

	if callCount != 1 {
		t.Errorf("expected webhook called exactly once, got %d", callCount)
	}
}

// --- buildAlertSubject ---

func TestEmailSubject_withProject(t *testing.T) {
	got := buildAlertSubject(AlertPayload{RuleName: "my rule", ProjectName: "acme"})
	if got != "[Tindra] my rule - acme" {
		t.Errorf("got %q", got)
	}
}

func TestEmailSubject_projectIDFallback(t *testing.T) {
	got := buildAlertSubject(AlertPayload{RuleName: "my rule", ProjectID: "proj-123"})
	if got != "[Tindra] my rule - proj-123" {
		t.Errorf("got %q", got)
	}
}

func TestEmailSubject_newIssue_single(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "new_issue",
		ProjectName: "acme",
		Details:     map[string]any{"new_issue_count": 1},
	})
	if got != "[Tindra] 1 new issue - acme" {
		t.Errorf("got %q", got)
	}
}

func TestEmailSubject_newIssue_multiple(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "new_issue",
		ProjectName: "acme",
		Details:     map[string]any{"new_issue_count": 5},
	})
	if got != "[Tindra] 5 new issues - acme" {
		t.Errorf("got %q", got)
	}
}

func TestEmailSubject_regressed_single(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "regressed",
		ProjectName: "acme",
		Details:     map[string]any{"regressed_count": 1},
	})
	if got != "[Tindra] 1 regression - acme" {
		t.Errorf("got %q", got)
	}
}

func TestEmailSubject_regressed_multiple(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "regressed",
		ProjectName: "acme",
		Details:     map[string]any{"regressed_count": 3},
	})
	if got != "[Tindra] 3 regressions - acme" {
		t.Errorf("got %q", got)
	}
}

func TestEmailSubject_newOrRegressed_single(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "new_or_regressed",
		ProjectName: "acme",
		Details:     map[string]any{"new_issue_count": 1, "regressed_count": 0},
	})
	if got != "[Tindra] 1 issue - acme" {
		t.Errorf("got %q", got)
	}
}

func TestEmailSubject_newOrRegressed_multiple(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "new_or_regressed",
		ProjectName: "acme",
		Details:     map[string]any{"new_issue_count": 2, "regressed_count": 3},
	})
	if got != "[Tindra] 5 issues - acme" {
		t.Errorf("got %q", got)
	}
}

func TestEmailSubject_eventCount(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "event_count",
		ProjectName: "acme",
		Details:     map[string]any{"event_count": 42, "window_mins": 60},
	})
	if got != "[Tindra] 42 events in 60min - acme" {
		t.Errorf("got %q", got)
	}
}

func TestAlertSubject_autoResolved_single(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "issue_auto_resolved",
		ProjectName: "myapp",
		Details:     map[string]any{"resolved_count": 1},
	})
	if got != "[Tindra] 1 performance issue auto-resolved - myapp" {
		t.Errorf("got %q", got)
	}
}

func TestAlertSubject_autoResolved_multiple(t *testing.T) {
	got := buildAlertSubject(AlertPayload{
		Trigger:     "issue_auto_resolved",
		ProjectName: "myapp",
		Details:     map[string]any{"resolved_count": 5},
	})
	if got != "[Tindra] 5 performance issues auto-resolved - myapp" {
		t.Errorf("got %q", got)
	}
}

// --- NotifyAutoResolved ---

func TestNotifyAutoResolved_noIssues_noFire(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	e := &Evaluator{pool: testPool, client: srv.Client()}
	e.NotifyAutoResolved(context.Background(), nil)
	if called {
		t.Error("expected no webhook call for empty issue list")
	}
}

func TestNotifyAutoResolved_firesWebhook(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM alert_rules")

	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	url := srv.URL
	_, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "notify-test",
		Enabled: true, Trigger: "new_issue", Channel: "webhook",
		WebhookURL: &url, CooldownMins: 60,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	issues := []*storage.Issue{{ID: "00000000-0000-0000-0000-000000000001", ProjectID: testProject.ID, Title: "N+1 query"}}
	e := &Evaluator{pool: testPool, client: srv.Client()}
	e.NotifyAutoResolved(context.Background(), issues)

	if received == nil {
		t.Fatal("expected webhook to be called")
	}
	var payload AlertPayload
	if err := json.Unmarshal(received, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Trigger != "issue_auto_resolved" {
		t.Errorf("trigger: got %q, want issue_auto_resolved", payload.Trigger)
	}
	count, _ := payload.Details["resolved_count"].(float64)
	if count != 1 {
		t.Errorf("resolved_count: got %v, want 1", payload.Details["resolved_count"])
	}
}

func TestNotifyAutoResolved_skipsUnmatchedProject(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM alert_rules")

	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	}))
	defer srv.Close()

	url := srv.URL
	storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{testProject.ID}, Name: "scoped-rule",
		Enabled: true, Trigger: "new_issue", Channel: "webhook",
		WebhookURL: &url, CooldownMins: 60,
	})

	// Issue belongs to a different project — rule should not fire.
	issues := []*storage.Issue{{ID: "00000000-0000-0000-0000-000000000002", ProjectID: "00000000-0000-0000-0000-000000000099", Title: "N+1"}}
	e := &Evaluator{pool: testPool, client: srv.Client()}
	e.NotifyAutoResolved(context.Background(), issues)

	if called {
		t.Error("expected no webhook call when issue project doesn't match rule's project filter")
	}
}

func TestNotifyAutoResolved_globalRule_firesForAnyProject(t *testing.T) {
	testPool.Exec(context.Background(), "DELETE FROM alert_rules")

	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	url := srv.URL
	storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs: []string{}, Name: "global-rule",
		Enabled: true, Trigger: "new_issue", Channel: "webhook",
		WebhookURL: &url, CooldownMins: 60,
	})

	issues := []*storage.Issue{{ID: "00000000-0000-0000-0000-000000000003", ProjectID: "00000000-0000-0000-0000-000000000099", Title: "N+1"}}
	e := &Evaluator{pool: testPool, client: srv.Client()}
	e.NotifyAutoResolved(context.Background(), issues)

	if received == nil {
		t.Fatal("expected global rule to fire for any project's issues")
	}
}
