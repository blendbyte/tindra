package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func shortIDPayload() AlertPayload {
	return AlertPayload{
		RuleName: "errors",
		Trigger:  "new_issue",
		FiredAt:  time.Now(),
		Details:  map[string]any{"new_issue_count": 2},
		Issues: []*storage.Issue{
			{ID: "i1", ShortID: "BACKEND-42", Title: "TypeError: boom", Level: "error"},
			{ID: "i2", Title: "No short ID", Level: "error"},
		},
	}
}

// Each chat channel leads an issue line with its short ID and links to the
// page under it; an issue without one keeps neither.
func TestChatAlerts_leadWithShortID(t *testing.T) {
	slack := rawJSON(t, captureChat(t, func(e *Evaluator, rule *storage.AlertRule) error {
		_, err := e.fireSlack(context.Background(), rule, shortIDPayload())
		return err
	}))
	require.Contains(t, slack, "• `BACKEND-42` <https://tindra.example.com/issues/BACKEND-42|TypeError: boom>")
	require.Contains(t, slack, "• <https://tindra.example.com/issues/i2|No short ID>")

	discord := rawJSON(t, captureChat(t, func(e *Evaluator, rule *storage.AlertRule) error {
		_, err := e.fireDiscord(context.Background(), rule, shortIDPayload())
		return err
	}))
	require.Contains(t, discord, "- `BACKEND-42` [TypeError: boom](https://tindra.example.com/issues/BACKEND-42)")
	require.Contains(t, discord, "- [No short ID](https://tindra.example.com/issues/i2)")

	teams := rawJSON(t, captureChat(t, func(e *Evaluator, rule *storage.AlertRule) error {
		_, err := e.fireTeams(context.Background(), rule, shortIDPayload())
		return err
	}))
	require.Contains(t, teams, "`BACKEND-42` [TypeError: boom](https://tindra.example.com/issues/BACKEND-42)")
}

func TestPushAndEmail_showShortID(t *testing.T) {
	n := buildPushNotification(shortIDPayload())
	lines := strings.Split(n.Body, "\n")
	require.Equal(t, "BACKEND-42 TypeError: boom", lines[0])
	require.Equal(t, "No short ID", lines[1])
	require.Equal(t, "/issues/BACKEND-42", buildPushNotification(AlertPayload{Trigger: "new_issue", Issues: shortIDPayload().Issues[:1]}).URL)

	html, text, err := RenderAlertEmail(shortIDPayload(), "https://tindra.example.com")
	require.NoError(t, err)
	require.Contains(t, html, "&middot; BACKEND-42")
	require.Contains(t, text, "ERROR · BACKEND-42")
	require.Contains(t, html, `href="https://tindra.example.com/issues/BACKEND-42"`)
	require.Contains(t, text, "https://tindra.example.com/issues/i2")
}
