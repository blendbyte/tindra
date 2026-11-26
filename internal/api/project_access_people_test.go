package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func userIDsIn(t *testing.T, path string, cookie *http.Cookie) []string {
	t.Helper()
	rec := accessGet(t, cookie, path)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var users []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &users))
	ids := make([]string, len(users))
	for i, u := range users {
		ids[i] = u.ID
	}
	return ids
}

// A restricted user only gets their teammates and the users who see every project,
// with no more than an id, a name and an email.
func TestUsersList_restrictedCallerOnlySeesTeammatesAndFullAccessUsers(t *testing.T) {
	me, cookie := restrictedSession(t)
	mate, _ := restrictedSession(t)
	stranger, _ := restrictedSession(t)
	everything, _ := fullAccessWith(t, storage.UserPermissions{})
	newTeam(t, me.ID, mate.ID)
	newTeam(t, stranger.ID)

	ids := userIDsIn(t, "/api/users", cookie)
	require.Contains(t, ids, me.ID)
	require.Contains(t, ids, mate.ID)
	require.Contains(t, ids, everything.ID)
	require.NotContains(t, ids, stranger.ID)

	var raw []map[string]any
	require.NoError(t, json.Unmarshal(accessGet(t, cookie, "/api/users").Body.Bytes(), &raw))
	for _, u := range raw {
		require.ElementsMatch(t, []string{"id", "name", "email"}, keysOf(u))
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// project_id narrows the list to the users who see all of those projects.
func TestUsersList_projectFilterKeepsUsersWhoSeeEveryProject(t *testing.T) {
	_, admin := fullAccessWith(t, storage.UserPermissions{})
	both, _ := restrictedSession(t)
	onlyFirst, _ := restrictedSession(t)
	everything, _ := fullAccessWith(t, storage.UserPermissions{})
	first, _ := projectInTeams(t, newTeam(t, both.ID, onlyFirst.ID))
	second, _ := projectInTeams(t, newTeam(t, both.ID))

	one := userIDsIn(t, "/api/users?project_id="+first.ID, admin)
	require.Contains(t, one, onlyFirst.ID)
	require.Contains(t, one, both.ID)
	require.Contains(t, one, everything.ID)

	two := userIDsIn(t, "/api/users?project_id="+first.ID+"&project_id="+second.ID, admin)
	require.Contains(t, two, both.ID)
	require.Contains(t, two, everything.ID)
	require.NotContains(t, two, onlyFirst.ID)

	require.Equal(t, http.StatusBadRequest, accessGet(t, admin, "/api/users?project_id=nope").Code)
}

// A restricted user cannot probe who sees a project they cannot see.
func TestUsersList_projectFilterRefusesAProjectTheCallerCannotSee(t *testing.T) {
	_, cookie := restrictedSession(t)
	hidden, _ := restrictedProject(t)
	require.Equal(t, http.StatusNotFound, accessGet(t, cookie, "/api/users?project_id="+url.QueryEscape(hidden.ID)).Code)
}

// An issue cannot be assigned to a user who has no access to its project.
func TestAssignIssue_refusesAssigneeWithoutAccess(t *testing.T) {
	_, admin := fullAccessWith(t, storage.UserPermissions{ManageIssues: true})
	member, _ := restrictedSession(t)
	outsider, _ := restrictedSession(t)
	everything, _ := fullAccessWith(t, storage.UserPermissions{})
	_, issue := restrictedProject(t, member.ID)
	patch := func(id string) *http.Response {
		rec := accessSend(t, admin, http.MethodPatch, "/api/issues/"+issue.ID, fmt.Sprintf(`{"assignee_id":%q}`, id))
		return rec.Result()
	}

	require.Equal(t, http.StatusBadRequest, patch(outsider.ID).StatusCode)
	require.Equal(t, http.StatusOK, patch(member.ID).StatusCode)
	require.Equal(t, http.StatusOK, patch(everything.ID).StatusCode)

	require.Contains(t, mcpRefused(t, "update_issue", map[string]any{"id": issue.ID, "assignee_id": outsider.ID}, admin), "cannot see")
	mcpOK(t, "update_issue", map[string]any{"id": issue.ID, "assignee_id": member.ID}, admin)
}

func TestUsersList_accessAllKeepsOnlyFullAccessUsers(t *testing.T) {
	_, admin := fullAccessWith(t, storage.UserPermissions{})
	restricted, _ := restrictedSession(t)
	everything, _ := fullAccessWith(t, storage.UserPermissions{})

	ids := userIDsIn(t, "/api/users?access=all", admin)
	require.Contains(t, ids, everything.ID)
	require.NotContains(t, ids, restricted.ID)
}
