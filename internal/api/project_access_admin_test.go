package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v), rec.Body.String())
	return v
}

// accessTestProject creates a project without a team, removed with the test.
func accessTestProject(t *testing.T) *storage.Project {
	t.Helper()
	slug := "loose-" + uuid.NewString()
	p, err := storage.CreateProject(context.Background(), testPool, slug, slug)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", p.ID) })
	return p
}

func newTeamViaAPI(t *testing.T, name string) storage.Team {
	t.Helper()
	rec := accessSend(t, authCookie(), http.MethodPost, "/api/teams", fmt.Sprintf(`{"name":%q}`, name))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	tm := decodeBody[storage.Team](t, rec)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM teams WHERE id=$1", tm.ID) })
	return tm
}

func visibleTo(t *testing.T, cookie *http.Cookie, projectID string) bool {
	t.Helper()
	for _, id := range projectIDsIn(t, accessGet(t, cookie, "/api/projects")) {
		if id == projectID {
			return true
		}
	}
	return false
}

func TestTeamsAPI_permissions(t *testing.T) {
	_, restricted := restrictedWith(t, storage.UserPermissions{ManageIssues: true, ManageAlerts: true})
	_, projectAdmin := fullAccessWith(t, storage.UserPermissions{ManageProjects: true})
	_, userAdmin := fullAccessWith(t, storage.UserPermissions{ManageUsers: true})

	// Listing is for the users who edit users or project teams.
	require.Equal(t, http.StatusForbidden, accessGet(t, restricted, "/api/teams").Code)
	require.Equal(t, http.StatusOK, accessGet(t, projectAdmin, "/api/teams").Code)
	require.Equal(t, http.StatusOK, accessGet(t, userAdmin, "/api/teams").Code)

	// Changing teams needs manage_users.
	body := func() string { return `{"name":"nope-` + uuid.NewString() + `"}` }
	require.Equal(t, http.StatusForbidden, accessSend(t, restricted, http.MethodPost, "/api/teams", body()).Code)
	require.Equal(t, http.StatusForbidden, accessSend(t, projectAdmin, http.MethodPost, "/api/teams", body()).Code)
	rec := accessSend(t, userAdmin, http.MethodPost, "/api/teams", body())
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	created := decodeBody[storage.Team](t, rec)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM teams WHERE id=$1", created.ID) })

	// A bearer token is never a user.
	req := httptest.NewRequest(http.MethodGet, "/api/teams", nil)
	req.Header.Set("Authorization", "Bearer "+bearerToken(t, testProject.ID))
	res := httptest.NewRecorder()
	globalHandler().ServeHTTP(res, req)
	require.Equal(t, http.StatusForbidden, res.Code)
}

func TestTeamsAPI_lifecycle(t *testing.T) {
	user, _ := restrictedSession(t)
	name := "ops-" + uuid.NewString()
	tm := newTeamViaAPI(t, name)
	require.Equal(t, name, tm.Name)
	require.Empty(t, tm.MemberIDs)

	dup := accessSend(t, authCookie(), http.MethodPost, "/api/teams", fmt.Sprintf(`{"name":%q}`, name))
	require.Equal(t, http.StatusConflict, dup.Code)

	for _, bad := range []string{`{"name":""}`, `{"name":"   "}`, `not json`} {
		require.Equal(t, http.StatusBadRequest, accessSend(t, authCookie(), http.MethodPost, "/api/teams", bad).Code, bad)
	}

	renamed := name + "-renamed"
	rec := accessSend(t, authCookie(), http.MethodPatch, "/api/teams/"+tm.ID, fmt.Sprintf(`{"name":%q}`, renamed))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, renamed, decodeBody[storage.Team](t, rec).Name)

	rec = accessSend(t, authCookie(), http.MethodPut, "/api/teams/"+tm.ID+"/members", fmt.Sprintf(`{"user_ids":[%q]}`, user.ID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{user.ID}, decodeBody[storage.Team](t, rec).MemberIDs)

	require.Contains(t, accessGet(t, authCookie(), "/api/teams").Body.String(), tm.ID)

	require.Equal(t, http.StatusBadRequest,
		accessSend(t, authCookie(), http.MethodPut, "/api/teams/"+tm.ID+"/members", fmt.Sprintf(`{"user_ids":[%q]}`, uuid.NewString())).Code, "unknown user")
	require.Equal(t, http.StatusBadRequest,
		accessSend(t, authCookie(), http.MethodPut, "/api/teams/"+tm.ID+"/members", `{"user_ids":["not-a-uuid"]}`).Code, "malformed id")

	require.Equal(t, http.StatusNoContent, accessSend(t, authCookie(), http.MethodDelete, "/api/teams/"+tm.ID, "").Code)
	require.Equal(t, http.StatusNotFound, accessSend(t, authCookie(), http.MethodDelete, "/api/teams/"+tm.ID, "").Code)
	require.Equal(t, http.StatusNotFound, accessSend(t, authCookie(), http.MethodPatch, "/api/teams/"+uuid.NewString(), `{"name":"x"}`).Code)
	require.Equal(t, http.StatusNotFound, accessSend(t, authCookie(), http.MethodPut, "/api/teams/"+uuid.NewString()+"/members", `{"user_ids":[]}`).Code)
	require.Equal(t, http.StatusNotFound, accessSend(t, authCookie(), http.MethodDelete, "/api/teams/not-a-uuid", "").Code)
}

func TestTeamsAPI_renameToTakenNameConflicts(t *testing.T) {
	a := newTeamViaAPI(t, "a-"+uuid.NewString())
	b := newTeamViaAPI(t, "b-"+uuid.NewString())
	rec := accessSend(t, authCookie(), http.MethodPatch, "/api/teams/"+b.ID, fmt.Sprintf(`{"name":%q}`, a.Name))
	require.Equal(t, http.StatusConflict, rec.Code)
}

func TestProjectTeamsAPI_permissions(t *testing.T) {
	_, restricted := restrictedSession(t)
	_, userAdmin := fullAccessWith(t, storage.UserPermissions{ManageUsers: true})
	_, projectAdmin := fullAccessWith(t, storage.UserPermissions{ManageProjects: true})
	p := accessTestProject(t)
	tm := newTeam(t)
	path := "/api/projects/" + p.ID + "/teams"

	for _, c := range []*http.Cookie{restricted, userAdmin} {
		require.Equal(t, http.StatusForbidden, accessGet(t, c, path).Code)
		require.Equal(t, http.StatusForbidden, accessSend(t, c, http.MethodPut, path, fmt.Sprintf(`{"team_ids":[%q]}`, tm.ID)).Code)
	}
	got, err := storage.GetProjectTeams(context.Background(), testPool, p.ID)
	require.NoError(t, err)
	require.Empty(t, got.TeamIDs, "a refused request must change nothing")

	require.Equal(t, http.StatusOK, accessGet(t, projectAdmin, path).Code)
	require.Equal(t, http.StatusOK, accessSend(t, projectAdmin, http.MethodPut, path, fmt.Sprintf(`{"team_ids":[%q]}`, tm.ID)).Code)
}

func TestProjectTeamsAPI_errors(t *testing.T) {
	p := accessTestProject(t)
	tm := newTeam(t)
	put := func(id, body string) int {
		return accessSend(t, authCookie(), http.MethodPut, "/api/projects/"+id+"/teams", body).Code
	}
	require.Equal(t, http.StatusNotFound, put(uuid.NewString(), fmt.Sprintf(`{"team_ids":[%q]}`, tm.ID)), "unknown project")
	require.Equal(t, http.StatusNotFound, put("not-a-uuid", `{"team_ids":[]}`), "malformed project id")
	require.Equal(t, http.StatusBadRequest, put(p.ID, `{"team_ids":["nope"]}`))
	require.Equal(t, http.StatusBadRequest, put(p.ID, `{"team_ids":"x"}`), "not a list")
	require.Equal(t, http.StatusBadRequest, put(p.ID, fmt.Sprintf(`{"team_ids":[%q,%q]}`, tm.ID, uuid.NewString())), "one unknown team")
	require.Equal(t, http.StatusNotFound, accessGet(t, authCookie(), "/api/projects/"+uuid.NewString()+"/teams").Code)

	got, err := storage.GetProjectTeams(context.Background(), testPool, p.ID)
	require.NoError(t, err)
	require.Empty(t, got.TeamIDs, "a refused request must change nothing")
}

func TestProjectTeamsAPI_takesEffectImmediately(t *testing.T) {
	member, memberCookie := restrictedSession(t)
	contractor, contractorCookie := restrictedSession(t)
	_, outsiderCookie := restrictedSession(t)
	p := accessTestProject(t)
	backend := newTeamViaAPI(t, "backend-"+uuid.NewString())
	external := newTeamViaAPI(t, "external-"+uuid.NewString())
	for tm, u := range map[string]*storage.User{backend.ID: member, external.ID: contractor} {
		require.Equal(t, http.StatusOK,
			accessSend(t, authCookie(), http.MethodPut, "/api/teams/"+tm+"/members", fmt.Sprintf(`{"user_ids":[%q]}`, u.ID)).Code)
	}
	require.False(t, visibleTo(t, memberCookie, p.ID), "a project without a team is for users who see everything")

	rec := accessSend(t, authCookie(), http.MethodPut, "/api/projects/"+p.ID+"/teams", fmt.Sprintf(`{"team_ids":[%q,%q]}`, backend.ID, external.ID))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.ElementsMatch(t, []string{backend.ID, external.ID}, decodeBody[storage.ProjectTeams](t, rec).TeamIDs)
	require.True(t, visibleTo(t, memberCookie, p.ID), "the backend team sees it")
	require.True(t, visibleTo(t, contractorCookie, p.ID), "so does the contractor team")
	require.False(t, visibleTo(t, outsiderCookie, p.ID))
	require.True(t, visibleTo(t, authCookie(), p.ID), "full access always does")

	rec = accessSend(t, authCookie(), http.MethodPut, "/api/projects/"+p.ID+"/teams", fmt.Sprintf(`{"team_ids":[%q]}`, backend.ID))
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, visibleTo(t, memberCookie, p.ID))
	require.False(t, visibleTo(t, contractorCookie, p.ID), "moving the project out of a team takes it away from that team")

	read := decodeBody[storage.ProjectTeams](t, accessGet(t, authCookie(), "/api/projects/"+p.ID+"/teams"))
	require.Equal(t, []string{backend.ID}, read.TeamIDs)

	rec = accessSend(t, authCookie(), http.MethodPut, "/api/projects/"+p.ID+"/teams", `{"team_ids":[]}`)
	require.Equal(t, http.StatusOK, rec.Code)
	require.False(t, visibleTo(t, memberCookie, p.ID), "detached from every team")
}

func TestProjectTeamsAPI_listCoversEveryProject(t *testing.T) {
	loose := accessTestProject(t)
	a := newTeam(t)
	b := newTeam(t)
	grouped, _ := projectInTeams(t, a, b)

	rec := accessGet(t, authCookie(), "/api/projects/teams")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	byProject := map[string]storage.ProjectTeamEntry{}
	for _, e := range decodeBody[[]storage.ProjectTeamEntry](t, rec) {
		byProject[e.ProjectID] = e
	}
	require.Empty(t, byProject[loose.ID].TeamIDs)
	require.ElementsMatch(t, teamIDs(a, b), byProject[grouped.ID].TeamIDs)
}

func TestProjectTeamsAPI_listNeedsManageProjects(t *testing.T) {
	_, restricted := restrictedSession(t)
	_, userAdmin := fullAccessWith(t, storage.UserPermissions{ManageUsers: true})
	for _, c := range []*http.Cookie{restricted, userAdmin} {
		require.Equal(t, http.StatusForbidden, accessGet(t, c, "/api/projects/teams").Code)
	}
}

func createProjectViaAPI(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := accessSend(t, authCookie(), http.MethodPost, "/api/projects", body)
	if rec.Code == http.StatusCreated {
		p := decodeBody[storage.Project](t, rec)
		t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", p.ID) })
	}
	return rec
}

func TestCreateProject_withTeamsIsAttachedFromTheStart(t *testing.T) {
	member, memberCookie := restrictedSession(t)
	other, otherCookie := restrictedSession(t)
	_, outsiderCookie := restrictedSession(t)
	a := newTeam(t, member.ID)
	b := newTeam(t, other.ID)

	slug := "created-" + uuid.NewString()
	rec := createProjectViaAPI(t, fmt.Sprintf(`{"name":"Created","slug":%q,"team_ids":[%q,%q]}`, slug, a.ID, b.ID))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	p := decodeBody[storage.Project](t, rec)

	got := decodeBody[storage.ProjectTeams](t, accessGet(t, authCookie(), "/api/projects/"+p.ID+"/teams"))
	require.ElementsMatch(t, teamIDs(a, b), got.TeamIDs)
	require.True(t, visibleTo(t, memberCookie, p.ID))
	require.True(t, visibleTo(t, otherCookie, p.ID))
	require.False(t, visibleTo(t, outsiderCookie, p.ID))
	require.True(t, visibleTo(t, authCookie(), p.ID), "full access sees it")
}

func TestCreateProject_withoutTeamsIsForFullAccessUsers(t *testing.T) {
	_, restrictedCookie := restrictedSession(t)

	rec := createProjectViaAPI(t, fmt.Sprintf(`{"name":"Loose","slug":%q}`, "loose-"+uuid.NewString()))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	loose := decodeBody[storage.Project](t, rec)
	require.Empty(t, decodeBody[storage.ProjectTeams](t, accessGet(t, authCookie(), "/api/projects/"+loose.ID+"/teams")).TeamIDs)
	require.False(t, visibleTo(t, restrictedCookie, loose.ID))
	require.True(t, visibleTo(t, authCookie(), loose.ID))

	rec = createProjectViaAPI(t, fmt.Sprintf(`{"name":"Empty","slug":%q,"team_ids":[]}`, "empty-"+uuid.NewString()))
	require.Equal(t, http.StatusCreated, rec.Code, "an empty list means no team")
}

func TestCreateProject_badTeamsCreateNothing(t *testing.T) {
	for name, teams := range map[string]string{
		"unknown team":   fmt.Sprintf(`[%q]`, uuid.NewString()),
		"malformed id":   `["not-a-uuid"]`,
		"not a list":     `"nope"`,
		"one unknown id": fmt.Sprintf(`[%q,%q]`, newTeam(t).ID, uuid.NewString()),
	} {
		slug := "bad-" + uuid.NewString()
		rec := createProjectViaAPI(t, fmt.Sprintf(`{"name":"Bad","slug":%q,"team_ids":%s}`, slug, teams))
		require.Equal(t, http.StatusBadRequest, rec.Code, name+": "+rec.Body.String())

		var n int
		require.NoError(t, testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM projects WHERE slug=$1`, slug).Scan(&n))
		require.Zero(t, n, name+": nothing may be created")
	}
}

func TestUserAccessAPI_restrictsAndOpensAtOnce(t *testing.T) {
	user, cookie := fullAccessWith(t, storage.UserPermissions{})
	mine, _ := restrictedProject(t, user.ID)
	other, _ := restrictedProject(t)
	require.True(t, visibleTo(t, cookie, other.ID), "full access by default")

	rec := accessSend(t, authCookie(), http.MethodPut, "/api/users/"+user.ID+"/access", `{"project_access":"teams"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, storage.AccessTeams, decodeBody[storage.User](t, rec).ProjectAccess)
	require.True(t, visibleTo(t, cookie, mine.ID))
	require.False(t, visibleTo(t, cookie, other.ID), "restricted at once")

	rec = accessSend(t, authCookie(), http.MethodPut, "/api/users/"+user.ID+"/access", `{"project_access":"all"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, visibleTo(t, cookie, other.ID), "opened again")
}

func TestUserAccessAPI_errors(t *testing.T) {
	user, _ := fullAccessWith(t, storage.UserPermissions{})
	put := func(id, body string) int {
		return accessSend(t, authCookie(), http.MethodPut, "/api/users/"+id+"/access", body).Code
	}
	require.Equal(t, http.StatusBadRequest, put(user.ID, `{"project_access":"everything"}`))
	require.Equal(t, http.StatusBadRequest, put(user.ID, `{"project_access":""}`))
	require.Equal(t, http.StatusBadRequest, put(user.ID, `not json`))
	require.Equal(t, http.StatusNotFound, put(uuid.NewString(), `{"project_access":"teams"}`))
	require.Equal(t, http.StatusNotFound, put("not-a-uuid", `{"project_access":"teams"}`))
}

func TestUserAccessAPI_needsManageUsers(t *testing.T) {
	target, _ := fullAccessWith(t, storage.UserPermissions{})
	_, restricted := restrictedSession(t)
	_, projectAdmin := fullAccessWith(t, storage.UserPermissions{ManageProjects: true})
	for _, c := range []*http.Cookie{restricted, projectAdmin} {
		require.Equal(t, http.StatusForbidden, accessSend(t, c, http.MethodPut, "/api/users/"+target.ID+"/access", `{"project_access":"teams"}`).Code)
	}
	got, err := storage.GetUserByID(context.Background(), testPool, target.ID)
	require.NoError(t, err)
	require.Equal(t, storage.AccessAll, got.ProjectAccess)
}

// manage_users and manage_projects administer the whole instance, so a stored permission
// must never silently do nothing for a user who only sees their teams' projects.
func TestUserAccessAPI_refusesPermissionsThatRestrictedUsersCannotHold(t *testing.T) {
	admin, _ := fullAccessWith(t, storage.UserPermissions{ManageProjects: true})
	rec := accessSend(t, authCookie(), http.MethodPut, "/api/users/"+admin.ID+"/access", `{"project_access":"teams"}`)
	require.Equal(t, http.StatusConflict, rec.Code, "a user holding manage_projects cannot be restricted: "+rec.Body.String())
	got, err := storage.GetUserByID(context.Background(), testPool, admin.ID)
	require.NoError(t, err)
	require.Equal(t, storage.AccessAll, got.ProjectAccess)

	restricted, _ := restrictedSession(t)
	for _, perms := range []string{`{"manage_users":true}`, `{"manage_projects":true}`} {
		rec := accessSend(t, authCookie(), http.MethodPut, "/api/users/"+restricted.ID+"/permissions", perms)
		require.Equal(t, http.StatusConflict, rec.Code, perms+": "+rec.Body.String())
	}
	rec = accessSend(t, authCookie(), http.MethodPut, "/api/users/"+restricted.ID+"/permissions", `{"manage_issues":true,"manage_alerts":true}`)
	require.Equal(t, http.StatusOK, rec.Code, "manage_issues and manage_alerts work inside a user's teams: "+rec.Body.String())
}

func TestMe_reportsTheProjectAccess(t *testing.T) {
	_, restrictedCookie := restrictedSession(t)
	require.Equal(t, storage.AccessTeams, decodeBody[storage.User](t, accessGet(t, restrictedCookie, "/api/me")).ProjectAccess)
	require.Equal(t, storage.AccessAll, decodeBody[storage.User](t, accessGet(t, authCookie(), "/api/me")).ProjectAccess)

	users := decodeBody[[]storage.User](t, accessGet(t, authCookie(), "/api/users"))
	for _, u := range users {
		require.Contains(t, []string{storage.AccessAll, storage.AccessTeams}, u.ProjectAccess, u.Email)
	}
}

func TestRestrictedUsersCannotAdministerTheInstance(t *testing.T) {
	_, cookie := restrictedWith(t, storage.UserPermissions{ManageIssues: true, ManageAlerts: true})
	slug := "nope-" + uuid.NewString()
	rec := accessSend(t, cookie, http.MethodPost, "/api/projects", fmt.Sprintf(`{"name":"Nope","slug":%q}`, slug))
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Equal(t, http.StatusForbidden, accessGet(t, cookie, "/api/projects/teams").Code)

	var n int
	require.NoError(t, testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM projects WHERE slug=$1`, slug).Scan(&n))
	require.Zero(t, n)
}

// inviteToken pulls the token out of an invitation response's invite_url.
func inviteToken(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var res struct {
		InviteURL string `json:"invite_url"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res), rec.Body.String())
	const marker = "/invite/"
	i := strings.LastIndex(res.InviteURL, marker)
	require.GreaterOrEqual(t, i, 0, res.InviteURL)
	return res.InviteURL[i+len(marker):]
}

// acceptInviteViaAPI accepts an invitation and returns the new user's session cookie.
func acceptInviteViaAPI(t *testing.T, token string) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/invite/"+token+"/accept", strings.NewReader(`{"password":"invitepass1234"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	for _, c := range rec.Result().Cookies() {
		if c.Name == "tindra_session" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

func cleanupInvitee(t *testing.T, email string) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), "DELETE FROM users WHERE email=$1", email)
		_, _ = testPool.Exec(context.Background(), "DELETE FROM user_invites WHERE email=$1", email)
	})
}

func TestInviteAPI_withTeamsRestrictsAndAddsThePersonOnAcceptance(t *testing.T) {
	a := newTeam(t)
	b := newTeam(t)
	projectA, _ := projectInTeams(t, a)
	projectB, _ := projectInTeams(t, b)
	elsewhere, _ := restrictedProject(t)
	email := "invitee-" + uuid.NewString() + "@example.com"
	cleanupInvitee(t, email)

	rec := accessSend(t, authCookie(), http.MethodPost, "/api/invites", fmt.Sprintf(
		`{"email":%q,"name":"Invitee","project_access":"teams","team_ids":[%q,%q]}`, email, a.ID, b.ID))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	token := inviteToken(t, rec)

	var found bool
	for _, inv := range decodeBody[[]storage.Invite](t, accessGet(t, authCookie(), "/api/invites")) {
		if inv.Email == email {
			found = true
			require.Equal(t, storage.AccessTeams, inv.ProjectAccess)
			require.ElementsMatch(t, teamIDs(a, b), inv.TeamIDs)
		}
	}
	require.True(t, found, "the pending invitation lists its access and teams")

	session := acceptInviteViaAPI(t, token)
	got := projectIDsIn(t, accessGet(t, session, "/api/projects"))
	require.ElementsMatch(t, []string{projectA.ID, projectB.ID}, got, "the new user sees exactly their teams' projects from the start")
	require.NotContains(t, got, elsewhere.ID)
	require.Equal(t, storage.AccessTeams, decodeBody[storage.User](t, accessGet(t, session, "/api/me")).ProjectAccess)
}

func TestInviteAPI_allAccessIsTheDefault(t *testing.T) {
	hidden, _ := restrictedProject(t)
	email := "invitee-" + uuid.NewString() + "@example.com"
	cleanupInvitee(t, email)

	rec := accessSend(t, authCookie(), http.MethodPost, "/api/invites", fmt.Sprintf(`{"email":%q}`, email))
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	session := acceptInviteViaAPI(t, inviteToken(t, rec))
	require.Contains(t, projectIDsIn(t, accessGet(t, session, "/api/projects")), hidden.ID, "existing API clients keep inviting people who see everything")
	require.Equal(t, storage.AccessAll, decodeBody[storage.User](t, accessGet(t, session, "/api/me")).ProjectAccess)
}

func TestInviteAPI_badInputsCreateNoInvitation(t *testing.T) {
	tm := newTeam(t)
	for name, body := range map[string]string{
		"teams access without a team": `"project_access":"teams"`,
		"teams access, empty list":    `"project_access":"teams","team_ids":[]`,
		"unknown access":              `"project_access":"everything"`,
		"unknown team":                fmt.Sprintf(`"project_access":"teams","team_ids":[%q]`, uuid.NewString()),
		"malformed team id":           `"project_access":"teams","team_ids":["not-a-uuid"]`,
		"team ids not a list":         `"project_access":"teams","team_ids":"x"`,
		"one unknown team among two":  fmt.Sprintf(`"project_access":"teams","team_ids":[%q,%q]`, tm.ID, uuid.NewString()),
	} {
		email := "bad-" + uuid.NewString() + "@example.com"
		rec := accessSend(t, authCookie(), http.MethodPost, "/api/invites", fmt.Sprintf(`{"email":%q,%s}`, email, body))
		require.Equal(t, http.StatusBadRequest, rec.Code, name+": "+rec.Body.String())
		pending, err := storage.HasPendingInvite(context.Background(), testPool, email)
		require.NoError(t, err)
		require.False(t, pending, name+": nothing may be created")
	}
}

func TestInviteAPI_needsManageUsers(t *testing.T) {
	tm := newTeam(t)
	_, restricted := restrictedSession(t)
	_, projectAdmin := fullAccessWith(t, storage.UserPermissions{ManageProjects: true})
	for _, c := range []*http.Cookie{restricted, projectAdmin} {
		email := "nope-" + uuid.NewString() + "@example.com"
		rec := accessSend(t, c, http.MethodPost, "/api/invites", fmt.Sprintf(`{"email":%q,"project_access":"teams","team_ids":[%q]}`, email, tm.ID))
		require.Equal(t, http.StatusForbidden, rec.Code)
	}
}
