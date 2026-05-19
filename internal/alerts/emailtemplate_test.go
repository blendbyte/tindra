package alerts

import (
	"strings"
	"testing"
	"time"
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
