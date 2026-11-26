package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// mcpOK calls a tool and fails the test when it answers with a tool error, so
// that a NotContains assertion on the text cannot pass on an error message.
func mcpOK(t *testing.T, tool string, args map[string]any, cookie *http.Cookie) string {
	t.Helper()
	result := toolCall(t, mcpHandler(), tool, args, cookie)
	require.NotEqual(t, true, result["isError"], toolText(t, result))
	return toolText(t, result)
}

// mcpRefused calls a tool and returns the error text it answered with.
func mcpRefused(t *testing.T, tool string, args map[string]any, cookie *http.Cookie) string {
	t.Helper()
	result := toolCall(t, mcpHandler(), tool, args, cookie)
	require.Equal(t, true, result["isError"], "expected a tool error, got: "+toolText(t, result))
	return toolText(t, result)
}

func TestProjectAccess_mcpReadsHideOtherTeamsProjects(t *testing.T) {
	_, cookie := restrictedSession(t)
	hidden, issue := restrictedProject(t)

	for _, args := range []map[string]any{{}, {"project_id": hidden.ID}} {
		require.NotContains(t, mcpOK(t, "list_issues", args, cookie), issue.ID, "list_issues %v", args)
		require.NotContains(t, mcpOK(t, "list_releases", args, cookie), hidden.ID, "list_releases %v", args)
	}
	require.Contains(t, mcpOK(t, "list_issues", map[string]any{"project_id": hidden.ID}, authCookie()), issue.ID,
		"full access sees the project")

	require.Contains(t, mcpRefused(t, "get_issue", map[string]any{"id": issue.ID}, cookie), "issue not found")
	require.Contains(t, mcpRefused(t, "list_issue_events", map[string]any{"issue_id": issue.ID}, cookie), "issue not found")
}

func TestProjectAccess_mcpTeamMemberReadsTheTeamsProject(t *testing.T) {
	user, cookie := restrictedSession(t)
	mine, issue := restrictedProject(t, user.ID)

	require.Contains(t, mcpOK(t, "list_issues", map[string]any{"project_id": mine.ID}, cookie), issue.ID)
	require.Contains(t, mcpOK(t, "get_issue", map[string]any{"id": issue.ID}, cookie), issue.ID)
}

func TestProjectAccess_mcpWritesCannotReachOtherTeamsIssues(t *testing.T) {
	ctx := context.Background()
	_, cookie := restrictedWith(t, storage.UserPermissions{ManageIssues: true})
	_, hidden := restrictedProject(t)

	require.Contains(t, mcpRefused(t, "update_issue", map[string]any{"id": hidden.ID, "status": "resolved"}, cookie), "issue not found")
	require.Contains(t, mcpOK(t, "bulk_update_issues", map[string]any{"ids": []any{hidden.ID}, "status": "resolved"}, cookie), `"updated":0`)

	got, err := storage.GetIssue(ctx, testPool, hidden.ID)
	require.NoError(t, err)
	require.NotEqual(t, "resolved", got.Status, "another team's issue must be untouched")
}

func TestProjectAccess_mcpAlerts(t *testing.T) {
	user, cookie := restrictedWith(t, storage.UserPermissions{ManageAlerts: true})
	mine, _ := restrictedProject(t, user.ID)
	hidden, _ := restrictedProject(t)
	visibleRule := alertRuleFor(t, "visible", mine.ID)
	secretRule := alertRuleFor(t, "secret", hidden.ID)
	globalRule := alertRuleFor(t, "global")

	listed := mcpOK(t, "list_alerts", map[string]any{}, cookie)
	require.Contains(t, listed, visibleRule.ID)
	require.NotContains(t, listed, secretRule.ID)
	require.NotContains(t, listed, globalRule.ID)

	create := func(args map[string]any) map[string]any {
		base := map[string]any{"name": "mcp-n", "trigger": "new_issue", "channel": "email", "email_to": "mcp@example.com"}
		for k, v := range args {
			base[k] = v
		}
		return toolCall(t, mcpHandler(), "create_alert_rule", base, cookie)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM alert_rules WHERE name = 'mcp-n' AND email_to = 'mcp@example.com'`)
	})

	// Without a project the rule would cover every project, other teams' included.
	for name, args := range map[string]map[string]any{
		"no project":             nil,
		"another team's project": {"project_id": hidden.ID},
	} {
		res := create(args)
		require.Equal(t, true, res["isError"], name)
		require.Contains(t, toolText(t, res), "project_ids must list projects you have access to", name)
	}
	ok := create(map[string]any{"project_id": mine.ID})
	require.NotEqual(t, true, ok["isError"], toolText(t, ok))
}
