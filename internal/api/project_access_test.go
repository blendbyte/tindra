package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// userSession creates a user with the given project access and stored permissions,
// with a session cookie. testUser is the first user: it sees every project and holds
// every permission. A user who only sees their teams' projects cannot hold manage_users
// or manage_projects, so callers must not ask for that combination.
func userSession(t *testing.T, projectAccess string, perms storage.UserPermissions) (*storage.User, *http.Cookie) {
	t.Helper()
	ctx := context.Background()
	u, err := storage.CreateUser(ctx, testPool, "access-"+uuid.NewString()+"@example.com", "accesspass1234")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", u.ID) })
	_, err = storage.UpdateUserPermissions(ctx, testPool, u.ID, perms)
	require.NoError(t, err)
	if projectAccess != storage.AccessAll {
		u, err = storage.SetUserProjectAccess(ctx, testPool, u.ID, projectAccess)
		require.NoError(t, err)
	}
	sess, err := storage.CreateSession(ctx, testPool, u.ID)
	require.NoError(t, err)
	return u, &http.Cookie{Name: "tindra_session", Value: sess.Token}
}

// restrictedSession is a user who only sees their teams' projects, without permissions.
func restrictedSession(t *testing.T) (*storage.User, *http.Cookie) {
	t.Helper()
	return userSession(t, storage.AccessTeams, storage.UserPermissions{})
}

// restrictedWith is a restricted user holding permissions that work inside their teams
// (manage_issues, manage_alerts).
func restrictedWith(t *testing.T, perms storage.UserPermissions) (*storage.User, *http.Cookie) {
	t.Helper()
	return userSession(t, storage.AccessTeams, perms)
}

// fullAccessWith is a user who sees every project, holding the given permissions.
func fullAccessWith(t *testing.T, perms storage.UserPermissions) (*storage.User, *http.Cookie) {
	t.Helper()
	return userSession(t, storage.AccessAll, perms)
}

// newTeam creates a team with the given members, removed with the test.
func newTeam(t *testing.T, userIDs ...string) *storage.Team {
	t.Helper()
	ctx := context.Background()
	tm, err := storage.CreateTeam(ctx, testPool, "team-"+uuid.NewString())
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM teams WHERE id=$1", tm.ID) })
	if len(userIDs) > 0 {
		tm, err = storage.SetTeamMembers(ctx, testPool, tm.ID, userIDs)
		require.NoError(t, err)
	}
	return tm
}

func teamIDs(teams ...*storage.Team) []string {
	out := make([]string, len(teams))
	for i, tm := range teams {
		out[i] = tm.ID
	}
	return out
}

// projectInTeams creates a project attached to the teams, with one issue in it.
func projectInTeams(t *testing.T, teams ...*storage.Team) (*storage.Project, *storage.Issue) {
	t.Helper()
	ctx := context.Background()
	slug := "team-" + uuid.NewString()
	p, err := storage.CreateProjectForTeams(ctx, testPool, slug, slug, teamIDs(teams...))
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", p.ID) })
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "fp-"+slug, "Secret "+slug, "error", "error", "", "", time.Now())
	require.NoError(t, err)
	return p, issue
}

// restrictedProject creates a project in a new team whose members are the given users.
// With no users, only users who see every project can see it.
func restrictedProject(t *testing.T, userIDs ...string) (*storage.Project, *storage.Issue) {
	t.Helper()
	return projectInTeams(t, newTeam(t, userIDs...))
}

func accessGet(t *testing.T, cookie *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	return rec
}

func projectIDsIn(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var projects []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &projects))
	out := make([]string, len(projects))
	for i, p := range projects {
		out[i] = p.ID
	}
	return out
}

func TestProjectAccess_restrictedSeesOnlyTheirTeamsProjects(t *testing.T) {
	user, cookie := restrictedSession(t)
	mine, _ := restrictedProject(t, user.ID)
	someoneElses, _ := restrictedProject(t)

	for _, path := range []string{"/api/projects", "/api/projects/metadata"} {
		got := projectIDsIn(t, accessGet(t, cookie, path))
		require.Contains(t, got, mine.ID, path)
		require.NotContains(t, got, someoneElses.ID, path)
		require.NotContains(t, got, testProject.ID, "a project without a team is for users who see everything: "+path)
	}

	// Asking for another team's project by id must not widen the scope.
	got := projectIDsIn(t, accessGet(t, cookie, "/api/projects?project_id="+someoneElses.ID))
	require.NotContains(t, got, someoneElses.ID)
}

func TestProjectAccess_restrictedWithoutTeamSeesNothing(t *testing.T) {
	_, cookie := restrictedSession(t)
	_, hidden := restrictedProject(t)

	for _, path := range []string{"/api/projects", "/api/projects/metadata"} {
		require.Empty(t, projectIDsIn(t, accessGet(t, cookie, path)), path)
	}
	for _, path := range []string{"/api/issues", "/api/issues/overview", "/api/issues/attention"} {
		rec := accessGet(t, cookie, path)
		require.Equal(t, http.StatusOK, rec.Code, path)
		require.NotContains(t, rec.Body.String(), hidden.ID, path)
	}
}

func TestProjectAccess_otherTeamsRoutesAnswerNotFound(t *testing.T) {
	_, cookie := restrictedSession(t)
	other, issue := restrictedProject(t)

	for _, path := range []string{
		"/api/projects/" + other.Slug + "/issues",
		"/api/projects/" + other.Slug + "/issues/" + issue.ID,
		"/api/projects/" + other.Slug + "/transactions",
		"/api/issues/" + issue.ID,
		"/api/issues/" + issue.ID + "/history",
	} {
		require.Equal(t, http.StatusNotFound, accessGet(t, cookie, path).Code, path)
	}
}

func TestProjectAccess_otherTeamsIssuesAbsentFromGlobalLists(t *testing.T) {
	user, cookie := restrictedSession(t)
	restrictedProject(t, user.ID)
	other, issue := restrictedProject(t)

	for _, path := range []string{
		"/api/issues",
		"/api/issues?project_id=" + other.ID,
		"/api/issues/overview",
		"/api/issues/attention",
	} {
		rec := accessGet(t, cookie, path)
		require.Equal(t, http.StatusOK, rec.Code, path)
		require.NotContains(t, rec.Body.String(), issue.ID, "another team's issue leaked from "+path)
	}
}

func TestProjectAccess_teamMemberSeesTheTeamsProject(t *testing.T) {
	user, cookie := restrictedSession(t)
	mine, issue := restrictedProject(t, user.ID)

	require.Contains(t, projectIDsIn(t, accessGet(t, cookie, "/api/projects")), mine.ID)
	require.Equal(t, http.StatusOK, accessGet(t, cookie, "/api/projects/"+mine.Slug+"/issues").Code)
	require.Equal(t, http.StatusOK, accessGet(t, cookie, "/api/issues/"+issue.ID).Code)
}

func TestProjectAccess_joiningAndLeavingATeamTakesEffectAtOnce(t *testing.T) {
	ctx := context.Background()
	user, cookie := restrictedSession(t)
	tm := newTeam(t)
	p, _ := projectInTeams(t, tm)
	require.NotContains(t, projectIDsIn(t, accessGet(t, cookie, "/api/projects")), p.ID)

	_, err := storage.SetTeamMembers(ctx, testPool, tm.ID, []string{user.ID})
	require.NoError(t, err)
	require.Contains(t, projectIDsIn(t, accessGet(t, cookie, "/api/projects")), p.ID)

	_, err = storage.SetTeamMembers(ctx, testPool, tm.ID, nil)
	require.NoError(t, err)
	require.NotContains(t, projectIDsIn(t, accessGet(t, cookie, "/api/projects")), p.ID)
}

func TestProjectAccess_aProjectInSeveralTeamsIsSeenByEachTeam(t *testing.T) {
	backend, backendCookie := restrictedSession(t)
	contractor, contractorCookie := restrictedSession(t)
	_, outsiderCookie := restrictedSession(t)
	shared, issue := projectInTeams(t, newTeam(t, backend.ID), newTeam(t, contractor.ID))

	for _, c := range []*http.Cookie{backendCookie, contractorCookie} {
		require.Contains(t, projectIDsIn(t, accessGet(t, c, "/api/projects")), shared.ID)
		require.Equal(t, http.StatusOK, accessGet(t, c, "/api/issues/"+issue.ID).Code)
	}
	require.NotContains(t, projectIDsIn(t, accessGet(t, outsiderCookie, "/api/projects")), shared.ID)
	require.Equal(t, http.StatusNotFound, accessGet(t, outsiderCookie, "/api/issues/"+issue.ID).Code)
}

func TestProjectAccess_fullAccessSeesEverything(t *testing.T) {
	hidden, issue := restrictedProject(t)
	_, plain := fullAccessWith(t, storage.UserPermissions{})

	for _, cookie := range []*http.Cookie{authCookie(), plain} {
		got := projectIDsIn(t, accessGet(t, cookie, "/api/projects"))
		require.Contains(t, got, hidden.ID)
		require.Contains(t, got, testProject.ID, "a project without a team")
		require.Equal(t, http.StatusOK, accessGet(t, cookie, "/api/projects/"+hidden.Slug+"/issues").Code)
		require.Equal(t, http.StatusOK, accessGet(t, cookie, "/api/issues/"+issue.ID).Code)
	}
}

func TestProjectAccess_bearerTokenKeepsItsOwnScope(t *testing.T) {
	secret, _ := restrictedProject(t)
	token := bearerToken(t, secret.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	require.Equal(t, []string{secret.ID}, projectIDsIn(t, rec))
}
