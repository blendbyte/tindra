package alerts

import (
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

// --- alertLevelColor ---

func TestAlertLevelColor(t *testing.T) {
	cases := []struct {
		level string
		want  string
	}{
		{"fatal", "#ef4444"},
		{"error", "#ef4444"},
		{"warning", "#f59e0b"},
		{"info", "#3b82f6"},
		{"debug", "#9ca3af"},
		{"", "#9ca3af"},
		{"unknown", "#9ca3af"},
	}
	for _, c := range cases {
		t.Run(c.level, func(t *testing.T) {
			got := alertLevelColor(c.level)
			if got != c.want {
				t.Errorf("alertLevelColor(%q) = %q, want %q", c.level, got, c.want)
			}
		})
	}
}

// --- alertTriggerLine ---

func TestAlertTriggerLine_testAlert(t *testing.T) {
	p := AlertPayload{Details: map[string]any{"test": true}}
	got := alertTriggerLine(p)
	if !strings.Contains(got, "test alert") {
		t.Errorf("expected 'test alert' in trigger line, got %q", got)
	}
}

func TestAlertTriggerLine_newIssue_single(t *testing.T) {
	p := AlertPayload{Trigger: "new_issue", Details: map[string]any{"new_issue_count": 1}}
	got := alertTriggerLine(p)
	if got != "1 new issue was created since the last alert." {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_newIssue_multiple(t *testing.T) {
	p := AlertPayload{Trigger: "new_issue", Details: map[string]any{"new_issue_count": 5}}
	got := alertTriggerLine(p)
	if !strings.Contains(got, "5 new issues") {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_regressed_single(t *testing.T) {
	p := AlertPayload{Trigger: "regressed", Details: map[string]any{"regressed_count": 1}}
	got := alertTriggerLine(p)
	if got != "1 previously resolved issue regressed since the last alert." {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_regressed_multiple(t *testing.T) {
	p := AlertPayload{Trigger: "regressed", Details: map[string]any{"regressed_count": 3}}
	got := alertTriggerLine(p)
	if !strings.Contains(got, "3 previously resolved issues") {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_newOrRegressed_bothPresent(t *testing.T) {
	p := AlertPayload{Trigger: "new_or_regressed", Details: map[string]any{
		"new_issue_count": 2, "regressed_count": 3,
	}}
	got := alertTriggerLine(p)
	if !strings.Contains(got, "2 new issue(s)") || !strings.Contains(got, "3 regression(s)") {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_newOrRegressed_newOnly_single(t *testing.T) {
	p := AlertPayload{Trigger: "new_or_regressed", Details: map[string]any{
		"new_issue_count": 1, "regressed_count": 0,
	}}
	got := alertTriggerLine(p)
	if got != "1 new issue was created since the last alert." {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_newOrRegressed_newOnly_multiple(t *testing.T) {
	p := AlertPayload{Trigger: "new_or_regressed", Details: map[string]any{
		"new_issue_count": 4, "regressed_count": 0,
	}}
	got := alertTriggerLine(p)
	if !strings.Contains(got, "4 new issues") {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_newOrRegressed_regressedOnly_single(t *testing.T) {
	p := AlertPayload{Trigger: "new_or_regressed", Details: map[string]any{
		"new_issue_count": 0, "regressed_count": 1,
	}}
	got := alertTriggerLine(p)
	if got != "1 previously resolved issue regressed since the last alert." {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_newOrRegressed_regressedOnly_multiple(t *testing.T) {
	p := AlertPayload{Trigger: "new_or_regressed", Details: map[string]any{
		"new_issue_count": 0, "regressed_count": 7,
	}}
	got := alertTriggerLine(p)
	if !strings.Contains(got, "7 previously resolved issues") {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_eventCount(t *testing.T) {
	p := AlertPayload{Trigger: "event_count", Details: map[string]any{
		"event_count": 42, "threshold": 10, "window_mins": 60,
	}}
	got := alertTriggerLine(p)
	if !strings.Contains(got, "42 events") || !strings.Contains(got, "60 minutes") {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_autoResolved_single(t *testing.T) {
	p := AlertPayload{Trigger: "issue_auto_resolved", Details: map[string]any{"resolved_count": 1}}
	got := alertTriggerLine(p)
	if got != "1 issue was automatically resolved." {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_autoResolved_multiple(t *testing.T) {
	p := AlertPayload{Trigger: "issue_auto_resolved", Details: map[string]any{"resolved_count": 3}}
	got := alertTriggerLine(p)
	if got != "3 issues were automatically resolved." {
		t.Errorf("got %q", got)
	}
}

func TestAlertTriggerLine_default(t *testing.T) {
	p := AlertPayload{Trigger: "unknown", RuleName: "my rule"}
	got := alertTriggerLine(p)
	if !strings.Contains(got, "my rule") {
		t.Errorf("got %q", got)
	}
}

// --- RenderPasswordResetEmail ---

func TestRenderPasswordResetEmail_success(t *testing.T) {
	html, text, err := RenderPasswordResetEmail("https://example.com/reset/abc123", "https://example.com")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if html == "" {
		t.Error("expected non-empty HTML")
	}
	if text == "" {
		t.Error("expected non-empty text")
	}
}

func TestRenderPasswordResetEmail_containsURL(t *testing.T) {
	resetURL := "https://example.com/reset/abc123"
	html, text, err := RenderPasswordResetEmail(resetURL, "https://example.com")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, resetURL) {
		t.Errorf("HTML does not contain reset URL")
	}
	if !strings.Contains(text, resetURL) {
		t.Errorf("text does not contain reset URL")
	}
}

func TestRenderPasswordResetEmail_emptyPublicURL(t *testing.T) {
	_, _, err := RenderPasswordResetEmail("https://example.com/reset/token", "")
	if err != nil {
		t.Fatalf("render with empty publicURL: %v", err)
	}
}

// --- RenderInviteEmail ---

func TestRenderInviteEmail_success(t *testing.T) {
	html, text, err := RenderInviteEmail("https://example.com/invite/xyz", "https://example.com")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if html == "" {
		t.Error("expected non-empty HTML")
	}
	if text == "" {
		t.Error("expected non-empty text")
	}
}

func TestRenderInviteEmail_containsURL(t *testing.T) {
	inviteURL := "https://example.com/invite/xyz"
	html, text, err := RenderInviteEmail(inviteURL, "https://example.com")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, inviteURL) {
		t.Errorf("HTML does not contain invite URL")
	}
	if !strings.Contains(text, inviteURL) {
		t.Errorf("text does not contain invite URL")
	}
}

// --- RenderAlertEmail (extended coverage) ---

func TestRenderAlertEmail_newIssue(t *testing.T) {
	payload := AlertPayload{
		RuleName: "issue alert",
		Trigger:  "new_issue",
		FiredAt:  time.Now(),
		Details:  map[string]any{"new_issue_count": 3},
	}
	html, _, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "3 new issues") {
		t.Error("expected issue count in HTML")
	}
}

func TestRenderAlertEmail_withPublicURL(t *testing.T) {
	payload := AlertPayload{RuleName: "url rule", Trigger: "new_or_regressed", FiredAt: time.Now()}
	html, text, err := RenderAlertEmail(payload, "https://tindra.example.com")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "https://tindra.example.com/issues") {
		t.Error("expected view URL in HTML")
	}
	if !strings.Contains(text, "https://tindra.example.com/issues") {
		t.Error("expected view URL in text")
	}
}

func TestRenderAlertEmail_testPayload(t *testing.T) {
	payload := AlertPayload{
		RuleName: "test rule",
		Trigger:  "new_or_regressed",
		FiredAt:  time.Now(),
		Details:  map[string]any{"test": true},
	}
	html, _, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "No action required") {
		t.Error("expected test alert message in HTML")
	}
}

func TestRenderAlertEmail_moreCount_newIssue(t *testing.T) {
	// 5 issues reported but only 0 issue cards (no Issues slice) → moreCount = 5.
	payload := AlertPayload{
		RuleName: "many issues",
		Trigger:  "new_issue",
		FiredAt:  time.Now(),
		Details:  map[string]any{"new_issue_count": 5},
		Issues:   nil, // no cards
	}
	html, _, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "5 more new issue") {
		t.Errorf("expected 'more' count in HTML; got snippet: %s",
			html[max(0, strings.Index(html, "more")-20):min(len(html), strings.Index(html, "more")+40)])
	}
}

func TestRenderAlertEmail_regressed(t *testing.T) {
	payload := AlertPayload{
		RuleName: "regression alert",
		Trigger:  "regressed",
		FiredAt:  time.Now(),
		Details:  map[string]any{"regressed_count": 2},
	}
	_, _, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render regressed: %v", err)
	}
}

func TestRenderAlertEmail_newOrRegressed(t *testing.T) {
	payload := AlertPayload{
		RuleName: "combo alert",
		Trigger:  "new_or_regressed",
		FiredAt:  time.Now(),
		Details:  map[string]any{"new_issue_count": 1, "regressed_count": 2},
	}
	_, _, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render new_or_regressed: %v", err)
	}
}

// --- uptime alerts ---

func TestAlertTriggerLine_uptimeDown(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{
		{1, "1 uptime monitor is down."},
		{3, "3 uptime monitors are down."},
	}
	for _, c := range cases {
		got := alertTriggerLine(AlertPayload{Trigger: "uptime_down", Details: map[string]any{"down_count": c.count}})
		if got != c.want {
			t.Errorf("count=%d: got %q, want %q", c.count, got, c.want)
		}
	}
}

func TestAlertTriggerLine_uptimeRecovered(t *testing.T) {
	cases := []struct {
		count int
		want  string
	}{
		{1, "1 uptime monitor has recovered."},
		{2, "2 uptime monitors have recovered."},
	}
	for _, c := range cases {
		got := alertTriggerLine(AlertPayload{Trigger: "uptime_recovered", Details: map[string]any{"recovered_count": c.count}})
		if got != c.want {
			t.Errorf("count=%d: got %q, want %q", c.count, got, c.want)
		}
	}
}

func TestRenderAlertEmail_uptimeDown(t *testing.T) {
	code := 503
	errMsg := ""
	wentDown := time.Now().Add(-47 * time.Minute)
	payload := AlertPayload{
		RuleName: "uptime alert",
		Trigger:  "uptime_down",
		FiredAt:  time.Now(),
		Details:  map[string]any{"down_count": 1},
		UptimeMonitors: []*storage.UptimeMonitor{
			{
				Name:           "Homepage",
				URL:            "https://example.com",
				State:          "down",
				LastStatusCode: &code,
				LastError:      &errMsg,
				WentDownAt:     &wentDown,
			},
		},
	}
	html, text, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Homepage", "https://example.com", "HTTP 503", "Down"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
	if !strings.Contains(html, "Down since") {
		t.Error("HTML missing 'Down since'")
	}
}

func TestRenderAlertEmail_uptimeDown_networkError(t *testing.T) {
	errMsg := "timeout"
	payload := AlertPayload{
		RuleName: "uptime alert",
		Trigger:  "uptime_down",
		FiredAt:  time.Now(),
		Details:  map[string]any{"down_count": 1},
		UptimeMonitors: []*storage.UptimeMonitor{
			{
				Name:      "API",
				URL:       "https://api.example.com",
				State:     "down",
				LastError: &errMsg,
			},
		},
	}
	html, text, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"API", "https://api.example.com", "timeout"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
}

func TestRenderAlertEmail_uptimeRecovered(t *testing.T) {
	wentDown := time.Now().Add(-2*time.Hour - 15*time.Minute)
	lastOk := time.Now()
	payload := AlertPayload{
		RuleName: "uptime alert",
		Trigger:  "uptime_recovered",
		FiredAt:  time.Now(),
		Details:  map[string]any{"recovered_count": 1},
		UptimeMonitors: []*storage.UptimeMonitor{
			{
				Name:       "Homepage",
				URL:        "https://example.com",
				State:      "up",
				WentDownAt: &wentDown,
				LastOkAt:   &lastOk,
			},
		},
	}
	html, text, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Homepage", "https://example.com", "Recovered", "Was down for"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
	if !strings.Contains(text, "Was down for") {
		t.Error("text missing 'Was down for'")
	}
}

func TestRenderAlertEmail_uptimeDown_viewURL(t *testing.T) {
	payload := AlertPayload{
		RuleName: "uptime alert",
		Trigger:  "uptime_down",
		FiredAt:  time.Now(),
		Details:  map[string]any{"down_count": 1},
	}
	html, text, err := RenderAlertEmail(payload, "https://tindra.example.com")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "https://tindra.example.com/monitors") {
		t.Error("HTML missing monitors view URL")
	}
	if !strings.Contains(text, "https://tindra.example.com/monitors") {
		t.Error("text missing monitors view URL")
	}
}

func TestRenderAlertEmail_uptimeDown_historyAndDetails(t *testing.T) {
	code := 503
	ms := 234
	history := []storage.UptimeCheckDot{
		{Status: "up"}, {Status: "up"}, {Status: "down"}, {Status: "down"},
	}
	payload := AlertPayload{
		RuleName: "uptime alert",
		Trigger:  "uptime_down",
		FiredAt:  time.Now(),
		Details:  map[string]any{"down_count": 1},
		UptimeMonitors: []*storage.UptimeMonitor{{
			Name:           "API",
			URL:            "https://api.example.com",
			State:          "down",
			ExpectedCodes:  "200-299",
			LastStatusCode: &code,
			LastResponseMs: &ms,
			RecentChecks:   history,
		}},
	}
	html, text, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// expected/received detail
	if !strings.Contains(html, "expected 200-299") {
		t.Error("HTML missing expected codes")
	}
	if !strings.Contains(html, "got HTTP 503") {
		t.Error("HTML missing received status")
	}
	if !strings.Contains(html, "234ms") {
		t.Error("HTML missing response time")
	}
	// history dots rendered as colored spans
	if !strings.Contains(html, "#22c55e") {
		t.Error("HTML missing green dot color")
	}
	if !strings.Contains(html, "#ef4444") {
		t.Error("HTML missing red dot color")
	}
	// plain text history
	if !strings.Contains(text, "++--") {
		t.Errorf("text missing history string, got snippet: %q", text[max(0, strings.Index(text, "History")-5):])
	}
	if !strings.Contains(text, "Expected: 200-299") {
		t.Error("text missing expected codes")
	}
	if !strings.Contains(text, "Got: HTTP 503") {
		t.Error("text missing received status")
	}
}

func TestFormatDowntime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "less than a minute"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h 30m"},
		{2*time.Hour + 3*time.Minute, "2h 3m"},
		{-time.Minute, "less than a minute"},
	}
	for _, c := range cases {
		got := formatDowntime(c.d)
		if got != c.want {
			t.Errorf("formatDowntime(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// makeTestIssue returns a *storage.Issue with all alert-enrichment fields populated.
func makeTestIssue(id, title, level, env string, ts time.Time) *storage.Issue {
	e := env
	return &storage.Issue{
		ID:              id,
		Title:           title,
		Level:           level,
		Environment:     &e,
		FirstSeen:       ts,
		AlertMessage:    "Something went wrong fetching data",
		AlertReqURL:     "https://api.example.com/v1/orders/99",
		AlertReqMethod:  "POST",
		AlertOccurredAt: &ts,
		TopFrames:       []string{"handleOrder  app/orders.go:88", "routeRequest  app/router.go:14"},
	}
}

func TestRenderAlertEmail_issueCard_html(t *testing.T) {
	ts := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC)
	iss := makeTestIssue("iss-001", "TypeError: Cannot read properties of undefined", "error", "production", ts)
	payload := AlertPayload{
		RuleName: "Prod errors",
		Trigger:  "new_issue",
		FiredAt:  ts,
		Details:  map[string]any{"new_issue_count": 1},
		Issues:   []*storage.Issue{iss},
	}
	html, _, err := RenderAlertEmail(payload, "https://tindra.example.com")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"TypeError: Cannot read properties of undefined",
		"27 May 2026, 14:30:00 UTC",
		"POST",
		"https://api.example.com/v1/orders/99",
		"Something went wrong fetching data",
		"handleOrder",
		"https://tindra.example.com/issues/iss-001",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

func TestRenderAlertEmail_issueCard_text(t *testing.T) {
	ts := time.Date(2026, 5, 27, 14, 30, 0, 0, time.UTC)
	iss := makeTestIssue("iss-002", "RuntimeException: DB connection refused", "error", "production", ts)
	payload := AlertPayload{
		RuleName: "Prod errors",
		Trigger:  "new_issue",
		FiredAt:  ts,
		Details:  map[string]any{"new_issue_count": 1},
		Issues:   []*storage.Issue{iss},
	}
	_, text, err := RenderAlertEmail(payload, "https://tindra.example.com")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"RuntimeException: DB connection refused",
		"27 May 2026, 14:30:00 UTC",
		"POST https://api.example.com/v1/orders/99",
		"Something went wrong fetching data",
		"handleOrder",
		"https://tindra.example.com/issues/iss-002",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q", want)
		}
	}
}

// TestRenderAlertEmail_issueCard_noOptionalFields verifies that missing request/message
// fields don't produce empty labels or broken markup.
func TestRenderAlertEmail_issueCard_noOptionalFields(t *testing.T) {
	ts := time.Date(2026, 5, 27, 9, 0, 0, 0, time.UTC)
	env := "staging"
	iss := &storage.Issue{
		ID:          "iss-003",
		Title:       "NullPointerException",
		Level:       "error",
		Environment: &env,
		FirstSeen:   ts,
		// no AlertMessage, AlertReqURL, AlertReqMethod, AlertOccurredAt, TopFrames
	}
	payload := AlertPayload{
		RuleName: "staging alert",
		Trigger:  "new_issue",
		FiredAt:  ts,
		Details:  map[string]any{"new_issue_count": 1},
		Issues:   []*storage.Issue{iss},
	}
	html, text, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "NullPointerException") {
		t.Error("HTML missing issue title")
	}
	if !strings.Contains(text, "NullPointerException") {
		t.Error("text missing issue title")
	}
	// Empty optional fields must not leave orphaned labels in either output.
	for _, bad := range []string{"POST ", "GET ", "occurred_at", "undefined"} {
		if strings.Contains(text, bad) {
			t.Errorf("text contains unexpected fragment %q", bad)
		}
	}
}

func TestRenderAlertEmail_projectName_fromPayload(t *testing.T) {
	ts := time.Now().UTC()
	iss := makeTestIssue("iss-p1", "SomeError", "error", "production", ts)
	iss.ProjectID = "proj-aaa"
	payload := AlertPayload{
		RuleName:    "Errors",
		Trigger:     "new_issue",
		FiredAt:     ts,
		Details:     map[string]any{"new_issue_count": 1},
		ProjectName: "MyApp",
		Issues:      []*storage.Issue{iss},
	}
	html, text, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, "MyApp") {
		t.Error("HTML missing project name from payload")
	}
	// text meta line is uppercased: "ERROR · PRODUCTION · MYAPP"
	if !strings.Contains(text, "MYAPP") {
		t.Error("text missing project name from payload")
	}
}

func TestRenderAlertEmail_projectName_perIssueOverridesPayload(t *testing.T) {
	ts := time.Now().UTC()
	iss1 := makeTestIssue("iss-p1", "ErrorAlpha", "error", "production", ts)
	iss1.ProjectID = "proj-aaa"
	iss2 := makeTestIssue("iss-p2", "ErrorBeta", "error", "production", ts)
	iss2.ProjectID = "proj-bbb"
	payload := AlertPayload{
		RuleName: "All errors",
		Trigger:  "new_issue",
		FiredAt:  ts,
		Details:  map[string]any{"new_issue_count": 2},
		ProjectNames: map[string]string{
			"proj-aaa": "Frontend",
			"proj-bbb": "Backend",
		},
		Issues: []*storage.Issue{iss1, iss2},
	}
	html, text, err := RenderAlertEmail(payload, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Frontend", "Backend"} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing project name %q", want)
		}
	}
	// text meta line is uppercased: "ERROR · PRODUCTION · FRONTEND"
	for _, want := range []string{"FRONTEND", "BACKEND"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing project name %q", want)
		}
	}
}

func TestRenderAlertEmail_cronMissed(t *testing.T) {
	now := time.Now().UTC()
	nextAt := now.Add(time.Hour)
	lastOk := now.Add(-2 * time.Hour)
	payload := AlertPayload{
		RuleName:  "Cron Monitor Alert",
		Trigger:   "cron_missed",
		FiredAt:   now,
		ProjectID: "proj-cron",
		Details:   map[string]any{"missed_count": 2},
		Monitors: []*storage.CronMonitor{
			{
				Name:           "nightly-job",
				Schedule:       "@daily",
				State:          "missed",
				NextExpectedAt: &nextAt,
				LastOkAt:       &lastOk,
			},
		},
	}

	html, text, err := RenderAlertEmail(payload, "https://app.example.com")
	if err != nil {
		t.Fatalf("RenderAlertEmail cron_missed: %v", err)
	}
	if !strings.Contains(html, "monitors") {
		t.Error("HTML should contain monitors link")
	}
	if !strings.Contains(text, "Cron Monitor Alert") {
		t.Error("text should contain rule name")
	}
	if !strings.Contains(html, "nightly-job") {
		t.Error("HTML should contain monitor name")
	}
}

func TestRenderAlertEmail_cronError(t *testing.T) {
	now := time.Now().UTC()
	lastCheckin := now.Add(-time.Hour)
	checkinStatus := "error"
	payload := AlertPayload{
		RuleName:  "Cron Error Alert",
		Trigger:   "cron_error",
		FiredAt:   now,
		ProjectID: "proj-cron-err",
		Details:   map[string]any{"error_count": 1},
		Monitors: []*storage.CronMonitor{
			{
				Name:              "daily-report",
				Schedule:          "0 8 * * *",
				State:             "error",
				LastCheckinAt:     &lastCheckin,
				LastCheckinStatus: &checkinStatus,
			},
		},
	}

	html, text, err := RenderAlertEmail(payload, "https://app.example.com")
	if err != nil {
		t.Fatalf("RenderAlertEmail cron_error: %v", err)
	}
	if !strings.Contains(html, "monitors") {
		t.Error("HTML should contain monitors link")
	}
	if !strings.Contains(text, "Cron Error Alert") {
		t.Error("text should contain rule name")
	}
	if !strings.Contains(html, "daily-report") {
		t.Error("HTML should contain monitor name")
	}
}
