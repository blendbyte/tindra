package api_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func accessSend(t *testing.T, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	return rec
}

func TestProjectAccess_commentsOnAnotherTeamsIssue(t *testing.T) {
	ctx := context.Background()
	user, cookie := restrictedWith(t, storage.UserPermissions{ManageIssues: true})
	_, hidden := restrictedProject(t)
	base := "/api/issues/" + hidden.ID + "/comments"

	require.Equal(t, http.StatusNotFound, accessGet(t, cookie, base).Code)
	require.Equal(t, http.StatusNotFound, accessSend(t, cookie, http.MethodPost, base, `{"body":"hi"}`).Code)

	// A comment written while the user was in the team cannot be edited or deleted
	// after they leave it, even though they authored it.
	tm := newTeam(t, user.ID)
	_, issue := projectInTeams(t, tm)
	c, err := storage.CreateComment(ctx, testPool, issue.ID, user.ID, "mine")
	require.NoError(t, err)
	_, err = storage.SetTeamMembers(ctx, testPool, tm.ID, nil)
	require.NoError(t, err)

	require.Equal(t, http.StatusNotFound, accessSend(t, cookie, http.MethodPut, "/api/comments/"+c.ID, `{"body":"edited"}`).Code)
	require.Equal(t, http.StatusNotFound, accessSend(t, cookie, http.MethodDelete, "/api/comments/"+c.ID, "").Code)
	got, err := storage.GetComment(ctx, testPool, c.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "comment must survive")
	require.Equal(t, "mine", got.Body)
}

func TestProjectAccess_releasesOfAnotherTeamsProject(t *testing.T) {
	ctx := context.Background()
	_, cookie := restrictedSession(t)
	hidden, _ := restrictedProject(t)
	var releaseID string
	require.NoError(t, testPool.QueryRow(ctx,
		`INSERT INTO releases (project_id, version) VALUES ($1, 'v-access') RETURNING id`, hidden.ID).Scan(&releaseID))

	for _, suffix := range []string{"", "/issues", "/transactions"} {
		path := "/api/releases/" + releaseID + suffix
		require.Equal(t, http.StatusNotFound, accessGet(t, cookie, path).Code, path)
		require.Equal(t, http.StatusOK, accessGet(t, authCookie(), path).Code, "full access: "+path)
	}
}

func TestProjectAccess_issueUpdatesOnAnotherTeamsProject(t *testing.T) {
	ctx := context.Background()
	_, cookie := restrictedWith(t, storage.UserPermissions{ManageIssues: true})
	_, hidden := restrictedProject(t)

	rec := accessSend(t, cookie, http.MethodPatch, "/api/issues/"+hidden.ID, `{"status":"resolved"}`)
	require.Equal(t, http.StatusNotFound, rec.Code)

	rec = accessSend(t, cookie, http.MethodPatch, "/api/issues/bulk", fmt.Sprintf(`{"ids":[%q],"status":"resolved"}`, hidden.ID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"updated":0`)

	got, err := storage.GetIssue(ctx, testPool, hidden.ID)
	require.NoError(t, err)
	require.NotEqual(t, "resolved", got.Status, "another team's issue must be untouched")
}

func TestProjectAccess_tokenListHidesOtherTeamsProjects(t *testing.T) {
	ctx := context.Background()
	user, cookie := restrictedSession(t)
	mine, _ := restrictedProject(t, user.ID)
	hidden, _ := restrictedProject(t)
	_, _, err := storage.CreateAPIToken(ctx, testPool, hidden.ID, "hidden-token", false)
	require.NoError(t, err)
	_, _, err = storage.CreateAPIToken(ctx, testPool, mine.ID, "mine-token", false)
	require.NoError(t, err)

	rec := accessGet(t, cookie, "/api/tokens")
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), hidden.ID)
	require.Contains(t, rec.Body.String(), mine.ID)
	require.Contains(t, accessGet(t, authCookie(), "/api/tokens").Body.String(), hidden.ID)
}

func alertRuleFor(t *testing.T, name string, projectIDs ...string) *storage.AlertRule {
	t.Helper()
	email := "alerts@example.com"
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		Name: name + "-" + uuid.NewString(), Enabled: true, Trigger: "new_issue", Channel: "email",
		EmailTo: &email, CooldownMins: 60, ProjectIDs: projectIDs,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM alert_rules WHERE id=$1", rule.ID) })
	return rule
}

func TestProjectAccess_alertRuleVisibility(t *testing.T) {
	user, cookie := restrictedWith(t, storage.UserPermissions{ManageAlerts: true})
	mine, _ := restrictedProject(t, user.ID)
	hidden, _ := restrictedProject(t)
	visibleRule := alertRuleFor(t, "visible", mine.ID)
	secretRule := alertRuleFor(t, "secret", hidden.ID)
	globalRule := alertRuleFor(t, "global")
	mixedRule := alertRuleFor(t, "mixed", mine.ID, hidden.ID)

	listRules := func(c *http.Cookie) string {
		rec := accessGet(t, c, "/api/alert-rules")
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		return rec.Body.String()
	}
	body := listRules(cookie)
	require.Contains(t, body, visibleRule.ID)
	for _, hiddenRule := range []*storage.AlertRule{secretRule, globalRule, mixedRule} {
		require.NotContains(t, body, hiddenRule.ID, hiddenRule.Name)
	}

	all := listRules(authCookie())
	for _, rule := range []*storage.AlertRule{secretRule, globalRule, visibleRule, mixedRule} {
		require.Contains(t, all, rule.ID, "full access sees every rule")
	}

	for _, rule := range []*storage.AlertRule{secretRule, globalRule, mixedRule} {
		base := "/api/alert-rules/" + rule.ID
		require.Equal(t, http.StatusNotFound, accessGet(t, cookie, base).Code, base)
		require.Equal(t, http.StatusNotFound, accessGet(t, cookie, base+"/firings").Code, base+"/firings")
		require.Equal(t, http.StatusNotFound, accessSend(t, cookie, http.MethodPatch, base, `{"enabled":false}`).Code, "PATCH "+base)
		require.Equal(t, http.StatusNotFound, accessSend(t, cookie, http.MethodPost, base+"/test", "").Code, "test "+base)
		require.Equal(t, http.StatusNotFound, accessSend(t, cookie, http.MethodDelete, base, "").Code, "DELETE "+base)
	}
	got, err := storage.GetAlertRule(context.Background(), testPool, secretRule.ID)
	require.NoError(t, err)
	require.NotNil(t, got, "a hidden rule must survive a refused delete")
}

func TestProjectAccess_alertRuleWritesStayInsideAccess(t *testing.T) {
	user, cookie := restrictedWith(t, storage.UserPermissions{ManageAlerts: true})
	mine, _ := restrictedProject(t, user.ID)
	hidden, _ := restrictedProject(t)
	visibleRule := alertRuleFor(t, "visible", mine.ID)

	create := func(projectIDs string) *httptest.ResponseRecorder {
		return accessSend(t, cookie, http.MethodPost, "/api/alert-rules", fmt.Sprintf(
			`{"name":"n","trigger":"new_issue","channel":"email","email_to":"a@example.com","project_ids":%s}`, projectIDs))
	}
	require.Equal(t, http.StatusBadRequest, create(fmt.Sprintf(`[%q]`, hidden.ID)).Code, "another team's project")
	require.Equal(t, http.StatusBadRequest, create(`[]`).Code, "global rule")
	require.Equal(t, http.StatusBadRequest, create(fmt.Sprintf(`[%q,%q]`, mine.ID, hidden.ID)).Code, "mixed")

	ok := create(fmt.Sprintf(`[%q]`, mine.ID))
	require.Equal(t, http.StatusCreated, ok.Code, ok.Body.String())
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM alert_rules WHERE name = 'n' AND email_to = 'a@example.com'`)
	})

	rec := accessSend(t, cookie, http.MethodPatch, "/api/alert-rules/"+visibleRule.ID, fmt.Sprintf(`{"project_ids":[%q]}`, hidden.ID))
	require.Equal(t, http.StatusBadRequest, rec.Code, "moving a rule onto another team's project")
	rec = accessSend(t, cookie, http.MethodPatch, "/api/alert-rules/"+visibleRule.ID, `{"project_ids":[]}`)
	require.Equal(t, http.StatusBadRequest, rec.Code, "turning a rule global")
	require.False(t, strings.Contains(rec.Body.String(), hidden.ID), "the refusal must not echo hidden ids")
}
