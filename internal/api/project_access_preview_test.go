package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// pushRuleFor creates a push rule on the project that only the given user is notified by.
func pushRuleFor(t *testing.T, projectID string, userID string) *storage.AlertRule {
	t.Helper()
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		Name: "preview-" + uuid.NewString(), Enabled: true, Trigger: "new_issue", Channel: "push",
		ProjectIDs: []string{projectID}, RecipientIDs: []string{userID}, CooldownMins: 60,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM alert_rules WHERE id=$1", rule.ID) })
	return rule
}

// With ?dry_run=true, the four ways of taking access away answer with the alert rules
// they would affect and change nothing; without it the same request goes through.
func TestAccessChangesAPI_dryRunReportsAlertImpactAndChangesNothing(t *testing.T) {
	ctx := context.Background()
	member, _ := restrictedWith(t, storage.UserPermissions{ManageAlerts: true})
	team := newTeam(t, member.ID)
	project, _ := projectInTeams(t, team)
	rule := pushRuleFor(t, project.ID, member.ID)

	cases := map[string]struct{ method, path, body string }{
		"team members":  {http.MethodPut, "/api/teams/" + team.ID + "/members", `{"user_ids":[]}`},
		"project teams": {http.MethodPut, "/api/projects/" + project.ID + "/teams", `{"team_ids":[]}`},
		"team deletion": {http.MethodDelete, "/api/teams/" + team.ID, ``},
	}

	for name, c := range cases {
		rec := accessSend(t, authCookie(), c.method, c.path+"?dry_run=true", c.body)
		require.Equal(t, http.StatusOK, rec.Code, name+": "+rec.Body.String())
		impact := decodeBody[storage.AlertImpact](t, rec)
		require.Equal(t, []storage.RecipientRemoval{{
			UserID: member.ID, UserName: member.Name, RuleID: rule.ID, RuleName: rule.Name,
		}}, impact.RemovedRecipients, name)
		require.Equal(t, []storage.PausedRule{{ID: rule.ID, Name: rule.Name}}, impact.PausedRules, name)

		got, err := storage.GetAlertRule(ctx, testPool, rule.ID)
		require.NoError(t, err)
		require.Equal(t, []string{member.ID}, got.RecipientIDs, name+": nothing may change")
		require.True(t, got.Enabled, name)
		tm, err := storage.GetTeam(ctx, testPool, team.ID)
		require.NoError(t, err)
		require.NotNil(t, tm, name+": the team is still there")
		require.Equal(t, []string{member.ID}, tm.MemberIDs, name)
	}

	rec := accessSend(t, authCookie(), http.MethodPut, "/api/teams/"+team.ID+"/members", `{"user_ids":[]}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got, err := storage.GetAlertRule(ctx, testPool, rule.ID)
	require.NoError(t, err)
	require.Empty(t, got.RecipientIDs, "without dry_run the change is applied")
	require.False(t, got.Enabled)
}

func TestAccessChangesAPI_dryRunOfUserAccess(t *testing.T) {
	ctx := context.Background()
	user, _ := fullAccessWith(t, storage.UserPermissions{})
	project, _ := restrictedProject(t)
	rule := pushRuleFor(t, project.ID, user.ID)
	path := "/api/users/" + user.ID + "/access?dry_run=true"

	rec := accessSend(t, authCookie(), http.MethodPut, path, `{"project_access":"teams"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	impact := decodeBody[storage.AlertImpact](t, rec)
	require.Len(t, impact.RemovedRecipients, 1)
	require.Equal(t, rule.ID, impact.RemovedRecipients[0].RuleID)
	require.Len(t, impact.PausedRules, 1)
	got, err := storage.GetUserByID(ctx, testPool, user.ID)
	require.NoError(t, err)
	require.Equal(t, storage.AccessAll, got.ProjectAccess)

	// The refusals are the real ones.
	require.Equal(t, http.StatusBadRequest, accessSend(t, authCookie(), http.MethodPut, path, `{"project_access":"nobody"}`).Code)
	admin, _ := fullAccessWith(t, storage.UserPermissions{ManageUsers: true})
	require.Equal(t, http.StatusConflict,
		accessSend(t, authCookie(), http.MethodPut, "/api/users/"+admin.ID+"/access?dry_run=true", `{"project_access":"teams"}`).Code)
	require.Equal(t, http.StatusNotFound,
		accessSend(t, authCookie(), http.MethodPut, "/api/users/"+uuid.NewString()+"/access?dry_run=true", `{"project_access":"teams"}`).Code)
}

func TestAccessChangesAPI_dryRunErrors(t *testing.T) {
	team := newTeam(t)
	project, _ := projectInTeams(t, team)
	missing := uuid.NewString()

	for name, c := range map[string]struct {
		method, path, body string
		want               int
	}{
		"unknown team":         {http.MethodPut, "/api/teams/" + missing + "/members", `{"user_ids":[]}`, http.StatusNotFound},
		"unknown member":       {http.MethodPut, "/api/teams/" + team.ID + "/members", fmt.Sprintf(`{"user_ids":[%q]}`, missing), http.StatusBadRequest},
		"malformed members":    {http.MethodPut, "/api/teams/" + team.ID + "/members", `{"user_ids":["x"]}`, http.StatusBadRequest},
		"unknown project":      {http.MethodPut, "/api/projects/" + missing + "/teams", `{"team_ids":[]}`, http.StatusNotFound},
		"unknown project team": {http.MethodPut, "/api/projects/" + project.ID + "/teams", fmt.Sprintf(`{"team_ids":[%q]}`, missing), http.StatusBadRequest},
		"delete unknown team":  {http.MethodDelete, "/api/teams/" + missing, ``, http.StatusNotFound},
	} {
		rec := accessSend(t, authCookie(), c.method, c.path+"?dry_run=true", c.body)
		require.Equal(t, c.want, rec.Code, name+": "+rec.Body.String())
	}
}

// A preview needs the same permission as the change it previews.
func TestAccessChangesAPI_dryRunNeedsThePermissionOfTheChange(t *testing.T) {
	team := newTeam(t)
	project, _ := projectInTeams(t, team)
	_, projectAdmin := fullAccessWith(t, storage.UserPermissions{ManageProjects: true})
	_, alertAdmin := fullAccessWith(t, storage.UserPermissions{ManageAlerts: true})

	require.Equal(t, http.StatusForbidden,
		accessSend(t, alertAdmin, http.MethodPut, "/api/projects/"+project.ID+"/teams?dry_run=true", `{"team_ids":[]}`).Code)
	require.Equal(t, http.StatusForbidden,
		accessSend(t, projectAdmin, http.MethodPut, "/api/teams/"+team.ID+"/members?dry_run=true", `{"user_ids":[]}`).Code)
	require.Equal(t, http.StatusOK,
		accessSend(t, projectAdmin, http.MethodPut, "/api/projects/"+project.ID+"/teams?dry_run=true", `{"team_ids":[]}`).Code)
}
