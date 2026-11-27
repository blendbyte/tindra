package storage_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

// accessUser creates a user with the given project access and no permissions. The
// permissions are cleared in the same statement, because CreateUser gives the very
// first user all of them and a restricted user cannot hold manage_users.
func accessUser(t *testing.T, projectAccess string) *storage.User {
	t.Helper()
	ctx := context.Background()
	u, err := storage.CreateUser(ctx, testPool, "access-"+uuid.NewString()+"@example.com", "accesspass1234")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", u.ID) })
	_, err = testPool.Exec(ctx, `UPDATE users SET perm_manage_users=false, perm_manage_projects=false,
		perm_manage_alerts=false, perm_manage_issues=false, project_access=$2 WHERE id=$1`, u.ID, projectAccess)
	require.NoError(t, err)
	u.ProjectAccess = projectAccess
	return u
}

func accessProject(t *testing.T) *storage.Project {
	t.Helper()
	slug := "access-" + uuid.NewString()
	p, err := storage.CreateProject(context.Background(), testPool, slug, slug)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM projects WHERE id=$1", p.ID) })
	return p
}

// teamProject creates a project attached to the teams.
func teamProject(t *testing.T, teams ...*storage.Team) *storage.Project {
	t.Helper()
	p := accessProject(t)
	ids := make([]string, len(teams))
	for i, tm := range teams {
		ids[i] = tm.ID
	}
	_, err := storage.SetProjectTeams(context.Background(), testPool, p.ID, ids)
	require.NoError(t, err)
	return p
}

func accessTeam(t *testing.T, members ...*storage.User) *storage.Team {
	t.Helper()
	ctx := context.Background()
	tm, err := storage.CreateTeam(ctx, testPool, "team-"+uuid.NewString())
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM teams WHERE id=$1", tm.ID) })
	if len(members) > 0 {
		ids := make([]string, len(members))
		for i, m := range members {
			ids[i] = m.ID
		}
		tm, err = storage.SetTeamMembers(ctx, testPool, tm.ID, ids)
		require.NoError(t, err)
	}
	return tm
}

func accessFor(t *testing.T, u *storage.User) storage.ProjectAccess {
	t.Helper()
	access, err := storage.GetProjectAccess(context.Background(), testPool, u.ID, u.ProjectAccess)
	require.NoError(t, err)
	return access
}

func ids(teams ...*storage.Team) []string {
	out := make([]string, len(teams))
	for i, tm := range teams {
		out[i] = tm.ID
	}
	return out
}

func TestGetProjectAccess_allSeesEverything(t *testing.T) {
	teamProject(t, accessTeam(t))
	accessProject(t)
	require.True(t, accessFor(t, accessUser(t, storage.AccessAll)).All)
}

func TestGetProjectAccess_teamsSeesOnlyTheirTeamsProjects(t *testing.T) {
	user := accessUser(t, storage.AccessTeams)
	mine := teamProject(t, accessTeam(t, user))
	someoneElses := teamProject(t, accessTeam(t))
	unassigned := accessProject(t)

	access := accessFor(t, user)
	require.False(t, access.All)
	require.True(t, access.Allows(mine.ID))
	require.False(t, access.Allows(someoneElses.ID))
	require.False(t, access.Allows(unassigned.ID), "a project without a team is for users who see everything")
}

func TestGetProjectAccess_teamsWithoutTeamSeesNothing(t *testing.T) {
	user := accessUser(t, storage.AccessTeams)
	teamProject(t, accessTeam(t))

	access := accessFor(t, user)
	require.False(t, access.All)
	require.NotNil(t, access.IDs, "an empty list must not read as no filter")
	require.Empty(t, access.IDs)
}

func TestGetProjectAccess_projectInSeveralTeams(t *testing.T) {
	backend := accessUser(t, storage.AccessTeams)
	contractor := accessUser(t, storage.AccessTeams)
	outsider := accessUser(t, storage.AccessTeams)
	backendTeam := accessTeam(t, backend)
	contractorTeam := accessTeam(t, contractor)
	shared := teamProject(t, backendTeam, contractorTeam)

	require.True(t, accessFor(t, backend).Allows(shared.ID))
	require.True(t, accessFor(t, contractor).Allows(shared.ID))
	require.False(t, accessFor(t, outsider).Allows(shared.ID))
}

func TestGetProjectAccess_userInSeveralTeamsSeesEachOnce(t *testing.T) {
	user := accessUser(t, storage.AccessTeams)
	a := accessTeam(t, user)
	b := accessTeam(t, user)
	both := teamProject(t, a, b)
	onlyB := teamProject(t, b)

	require.ElementsMatch(t, []string{both.ID, onlyB.ID}, accessFor(t, user).IDs, "a project reachable twice is listed once")
}

func TestGetProjectAccess_leavingATeamRevokesAccess(t *testing.T) {
	ctx := context.Background()
	user := accessUser(t, storage.AccessTeams)
	tm := accessTeam(t, user)
	p := teamProject(t, tm)
	require.True(t, accessFor(t, user).Allows(p.ID))

	_, err := storage.SetTeamMembers(ctx, testPool, tm.ID, nil)
	require.NoError(t, err)
	require.False(t, accessFor(t, user).Allows(p.ID))
}

func TestGetProjectAccess_deletingATeamDetachesItsProjects(t *testing.T) {
	ctx := context.Background()
	user := accessUser(t, storage.AccessTeams)
	tm := accessTeam(t, user)
	p := teamProject(t, tm)

	ok, err := storage.DeleteTeam(ctx, testPool, tm.ID)
	require.NoError(t, err)
	require.True(t, ok)

	teams, err := storage.GetProjectTeams(ctx, testPool, p.ID)
	require.NoError(t, err)
	require.NotNil(t, teams, "the project itself survives")
	require.Empty(t, teams.TeamIDs)
	require.False(t, accessFor(t, user).Allows(p.ID))
}

func TestProjectAccess_Allows(t *testing.T) {
	require.True(t, storage.ProjectAccess{All: true}.Allows("anything"))
	require.False(t, storage.ProjectAccess{}.Allows("anything"), "empty IDs means no access, not all")
	require.True(t, storage.ProjectAccess{IDs: []string{"a", "b"}}.Allows("b"))
	require.False(t, storage.ProjectAccess{IDs: []string{"a", "b"}}.Allows("c"))
}

func TestSetProjectTeams(t *testing.T) {
	ctx := context.Background()
	first := accessUser(t, storage.AccessTeams)
	second := accessUser(t, storage.AccessTeams)
	a := accessTeam(t, first)
	b := accessTeam(t, second)
	p := accessProject(t)

	got, err := storage.SetProjectTeams(ctx, testPool, p.ID, ids(a, b, a))
	require.NoError(t, err)
	require.ElementsMatch(t, ids(a, b), got.TeamIDs, "duplicates are dropped")
	require.True(t, accessFor(t, first).Allows(p.ID))
	require.True(t, accessFor(t, second).Allows(p.ID))

	_, err = storage.SetProjectTeams(ctx, testPool, p.ID, ids(b))
	require.NoError(t, err)
	require.False(t, accessFor(t, first).Allows(p.ID), "members of a removed team lose access")
	require.True(t, accessFor(t, second).Allows(p.ID))

	got, err = storage.SetProjectTeams(ctx, testPool, p.ID, nil)
	require.NoError(t, err)
	require.Empty(t, got.TeamIDs, "no team detaches the project from all of them")
}

func TestSetProjectTeams_errors(t *testing.T) {
	ctx := context.Background()
	tm := accessTeam(t)

	missing, err := storage.SetProjectTeams(ctx, testPool, uuid.NewString(), ids(tm))
	require.NoError(t, err)
	require.Nil(t, missing, "unknown project")

	p := teamProject(t, tm)
	_, err = storage.SetProjectTeams(ctx, testPool, p.ID, []string{tm.ID, uuid.NewString()})
	require.ErrorIs(t, err, storage.ErrUnknownReference)

	got, err := storage.GetProjectTeams(ctx, testPool, p.ID)
	require.NoError(t, err)
	require.Equal(t, ids(tm), got.TeamIDs, "a refused change must leave the teams alone")

	got, err = storage.GetProjectTeams(ctx, testPool, uuid.NewString())
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestCreateProjectForTeams_isAttachedFromTheStart(t *testing.T) {
	ctx := context.Background()
	member := accessUser(t, storage.AccessTeams)
	outsider := accessUser(t, storage.AccessTeams)
	a := accessTeam(t, member)
	b := accessTeam(t)

	slug := "for-teams-" + uuid.NewString()
	p, err := storage.CreateProjectForTeams(ctx, testPool, slug, slug, []string{a.ID, b.ID, a.ID})
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = testPool.Exec(ctx, "DELETE FROM projects WHERE id=$1", p.ID) })

	teams, err := storage.GetProjectTeams(ctx, testPool, p.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, ids(a, b), teams.TeamIDs)
	require.True(t, accessFor(t, member).Allows(p.ID))
	require.False(t, accessFor(t, outsider).Allows(p.ID))
}

func TestCreateProjectForTeams_unknownTeamLeavesNoProject(t *testing.T) {
	ctx := context.Background()
	slug := "for-teams-" + uuid.NewString()

	_, err := storage.CreateProjectForTeams(ctx, testPool, slug, slug, []string{uuid.NewString()})
	require.ErrorIs(t, err, storage.ErrUnknownReference)

	var n int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT COUNT(*) FROM projects WHERE slug=$1`, slug).Scan(&n))
	require.Zero(t, n, "a failed creation must not leave an unattached project behind")
}

func TestListProjectTeams_coversEveryProject(t *testing.T) {
	ctx := context.Background()
	a := accessTeam(t)
	b := accessTeam(t)
	grouped := teamProject(t, a, b)
	loose := accessProject(t)

	entries, err := storage.ListProjectTeams(ctx, testPool)
	require.NoError(t, err)
	byProject := map[string]*storage.ProjectTeamEntry{}
	for _, e := range entries {
		byProject[e.ProjectID] = e
	}
	require.ElementsMatch(t, ids(a, b), byProject[grouped.ID].TeamIDs)
	require.Empty(t, byProject[loose.ID].TeamIDs)
	require.NotNil(t, byProject[loose.ID].TeamIDs, "an empty list serialises as [] not null")
}

func TestTeams_crud(t *testing.T) {
	ctx := context.Background()
	tm := accessTeam(t)
	require.Empty(t, tm.MemberIDs)

	_, err := storage.CreateTeam(ctx, testPool, tm.Name)
	require.ErrorIs(t, err, storage.ErrTeamNameTaken)

	renamed, err := storage.RenameTeam(ctx, testPool, tm.ID, tm.Name+"-renamed")
	require.NoError(t, err)
	require.Equal(t, tm.Name+"-renamed", renamed.Name)

	other := accessTeam(t)
	_, err = storage.RenameTeam(ctx, testPool, other.ID, renamed.Name)
	require.ErrorIs(t, err, storage.ErrTeamNameTaken)

	missing, err := storage.RenameTeam(ctx, testPool, uuid.NewString(), "x")
	require.NoError(t, err)
	require.Nil(t, missing)

	teams, err := storage.ListTeams(ctx, testPool)
	require.NoError(t, err)
	var found bool
	for _, listed := range teams {
		found = found || listed.ID == tm.ID
	}
	require.True(t, found)

	ok, err := storage.DeleteTeam(ctx, testPool, uuid.NewString())
	require.NoError(t, err)
	require.False(t, ok)
}

func TestSetTeamMembers_errors(t *testing.T) {
	ctx := context.Background()

	missing, err := storage.SetTeamMembers(ctx, testPool, uuid.NewString(), nil)
	require.NoError(t, err)
	require.Nil(t, missing, "unknown team")

	tm := accessTeam(t)
	_, err = storage.SetTeamMembers(ctx, testPool, tm.ID, []string{uuid.NewString()})
	require.ErrorIs(t, err, storage.ErrUnknownReference)

	u := accessUser(t, storage.AccessTeams)
	got, err := storage.SetTeamMembers(ctx, testPool, tm.ID, []string{u.ID, u.ID})
	require.NoError(t, err)
	require.Equal(t, []string{u.ID}, got.MemberIDs)
}

func TestListProjects_filter(t *testing.T) {
	ctx := context.Background()
	a := accessProject(t)
	accessProject(t)

	got, err := storage.ListProjects(ctx, testPool, a.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, a.ID, got[0].ID)

	all, err := storage.ListProjects(ctx, testPool)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(all), 2, "no filter returns every project")
}

func TestCreateUser_seesEveryProjectByDefault(t *testing.T) {
	truncateUsers(t)
	ctx := context.Background()
	first, err := storage.CreateUser(ctx, testPool, "first@example.com", "password12345")
	require.NoError(t, err)
	second, err := storage.CreateUser(ctx, testPool, "second@example.com", "password12345")
	require.NoError(t, err)
	admin, err := storage.CreateAdminUser(ctx, testPool, "admin@example.com", "Admin", "password12345")
	require.NoError(t, err)

	for _, u := range []*storage.User{first, second, admin} {
		require.Equal(t, storage.AccessAll, u.ProjectAccess, u.Email)
	}
	users, err := storage.ListUsers(ctx, testPool)
	require.NoError(t, err)
	for _, u := range users {
		require.Equal(t, storage.AccessAll, u.ProjectAccess)
	}
}

func TestSetUserProjectAccess(t *testing.T) {
	ctx := context.Background()
	u := accessUser(t, storage.AccessAll)

	restricted, err := storage.SetUserProjectAccess(ctx, testPool, u.ID, storage.AccessTeams)
	require.NoError(t, err)
	require.Equal(t, storage.AccessTeams, restricted.ProjectAccess)
	require.False(t, accessFor(t, restricted).All)

	back, err := storage.SetUserProjectAccess(ctx, testPool, u.ID, storage.AccessAll)
	require.NoError(t, err)
	require.Equal(t, storage.AccessAll, back.ProjectAccess)
	require.True(t, accessFor(t, back).All)

	_, err = storage.SetUserProjectAccess(ctx, testPool, u.ID, "everything")
	require.ErrorIs(t, err, storage.ErrInvalidAccess)
	missing, err := storage.SetUserProjectAccess(ctx, testPool, uuid.NewString(), storage.AccessAll)
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestRestrictedUsersCannotHoldAdministrationPermissions(t *testing.T) {
	ctx := context.Background()

	// A user who holds manage_users or manage_projects cannot be restricted.
	for name, perms := range map[string]storage.UserPermissions{
		"manage_users":    {ManageUsers: true},
		"manage_projects": {ManageProjects: true},
	} {
		u := accessUser(t, storage.AccessAll)
		// Another user keeps the instance administrable while this one is edited.
		_, err := storage.UpdateUserPermissions(ctx, testPool, u.ID, perms)
		require.NoError(t, err, name)
		_, err = storage.SetUserProjectAccess(ctx, testPool, u.ID, storage.AccessTeams)
		require.ErrorIs(t, err, storage.ErrRestrictedUserPermissions, name)
		got, err := storage.GetUserByID(ctx, testPool, u.ID)
		require.NoError(t, err)
		require.Equal(t, storage.AccessAll, got.ProjectAccess, name+": a refused change must leave the access alone")
	}

	// A restricted user cannot be given them either.
	restricted := accessUser(t, storage.AccessTeams)
	for name, perms := range map[string]storage.UserPermissions{
		"manage_users":    {ManageUsers: true},
		"manage_projects": {ManageProjects: true},
	} {
		_, err := storage.UpdateUserPermissions(ctx, testPool, restricted.ID, perms)
		require.ErrorIs(t, err, storage.ErrRestrictedUserPermissions, name)
	}

	// manage_issues and manage_alerts work inside a user's teams, so they stay allowed.
	got, err := storage.UpdateUserPermissions(ctx, testPool, restricted.ID, storage.UserPermissions{ManageIssues: true, ManageAlerts: true})
	require.NoError(t, err)
	require.True(t, got.Permissions.ManageIssues)
	require.True(t, got.Permissions.ManageAlerts)
}

func TestInviteForTeams_restrictedAcceptanceJoinsTheTeams(t *testing.T) {
	ctx := context.Background()
	accessUser(t, storage.AccessAll) // the invited user must not be the very first one
	a := accessTeam(t)
	b := accessTeam(t)
	projectA := teamProject(t, a)
	projectB := teamProject(t, b)
	email := inviteEmail()

	token, err := storage.CreateInviteForTeams(ctx, testPool, "", email, "Invited", storage.AccessTeams, []string{a.ID, b.ID, a.ID})
	require.NoError(t, err)
	inv, err := storage.GetInvite(ctx, testPool, token)
	require.NoError(t, err)
	require.Equal(t, storage.AccessTeams, inv.ProjectAccess)
	require.ElementsMatch(t, ids(a, b), inv.TeamIDs)

	pending, err := storage.ListPendingInvites(ctx, testPool)
	require.NoError(t, err)
	var listed *storage.Invite
	for _, p := range pending {
		if p.Email == email {
			listed = p
		}
	}
	require.NotNil(t, listed, "the invitation is pending")
	require.Equal(t, storage.AccessTeams, listed.ProjectAccess)
	require.ElementsMatch(t, ids(a, b), listed.TeamIDs)

	user := acceptInvite(t, token)
	require.Equal(t, storage.AccessTeams, user.ProjectAccess)
	require.Equal(t, []string{user.ID}, membersOf(t, a))
	require.Equal(t, []string{user.ID}, membersOf(t, b))
	access := accessFor(t, user)
	require.True(t, access.Allows(projectA.ID))
	require.True(t, access.Allows(projectB.ID), "they see every invited team's projects from the start")
}

func TestInviteForTeams_allAccessIsTheDefault(t *testing.T) {
	ctx := context.Background()
	accessUser(t, storage.AccessAll)
	token, err := storage.CreateInvite(ctx, testPool, "", inviteEmail(), "Invited")
	require.NoError(t, err)
	inv, err := storage.GetInvite(ctx, testPool, token)
	require.NoError(t, err)
	require.Equal(t, storage.AccessAll, inv.ProjectAccess)
	require.Empty(t, inv.TeamIDs)

	user := acceptInvite(t, token)
	require.Equal(t, storage.AccessAll, user.ProjectAccess)
	require.True(t, accessFor(t, user).All)
}

func TestInviteForTeams_allAccessCanStillJoinTeams(t *testing.T) {
	ctx := context.Background()
	accessUser(t, storage.AccessAll)
	tm := accessTeam(t)
	token, err := storage.CreateInviteForTeams(ctx, testPool, "", inviteEmail(), "Invited", storage.AccessAll, ids(tm))
	require.NoError(t, err)

	user := acceptInvite(t, token)
	require.True(t, accessFor(t, user).All)
	require.Equal(t, []string{user.ID}, membersOf(t, tm))
}

func TestInviteForTeams_validation(t *testing.T) {
	ctx := context.Background()
	email := inviteEmail()

	_, err := storage.CreateInviteForTeams(ctx, testPool, "", email, "x", storage.AccessTeams, nil)
	require.ErrorIs(t, err, storage.ErrInviteNeedsTeam, "restricted access with no team would land on an empty instance")
	_, err = storage.CreateInviteForTeams(ctx, testPool, "", email, "x", "everything", nil)
	require.ErrorIs(t, err, storage.ErrInvalidAccess)
	_, err = storage.CreateInviteForTeams(ctx, testPool, "", email, "x", storage.AccessTeams, []string{uuid.NewString()})
	require.ErrorIs(t, err, storage.ErrUnknownReference)

	pending, err := storage.HasPendingInvite(ctx, testPool, email)
	require.NoError(t, err)
	require.False(t, pending, "a refused invitation must not be created")
}

func TestInviteForTeams_deletedTeamStillLetsThePersonIn(t *testing.T) {
	ctx := context.Background()
	accessUser(t, storage.AccessAll)
	keep := accessTeam(t)
	gone := accessTeam(t)
	token, err := storage.CreateInviteForTeams(ctx, testPool, "", inviteEmail(), "Invited", storage.AccessTeams, ids(keep, gone))
	require.NoError(t, err)

	ok, err := storage.DeleteTeam(ctx, testPool, gone.ID)
	require.NoError(t, err)
	require.True(t, ok)
	inv, err := storage.GetInvite(ctx, testPool, token)
	require.NoError(t, err)
	require.Equal(t, ids(keep), inv.TeamIDs, "the deleted team is dropped from the invitation")

	user := acceptInvite(t, token)
	require.Equal(t, []string{user.ID}, membersOf(t, keep))
}

func inviteEmail() string { return "invited-" + uuid.NewString() + "@example.com" }

// acceptInvite accepts an invitation and removes the new account afterwards.
func acceptInvite(t *testing.T, token string) *storage.User {
	t.Helper()
	user, _, err := storage.AcceptInviteWithSession(context.Background(), testPool, token, "invitepass1234", "", 0)
	require.NoError(t, err)
	require.NotNil(t, user)
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", user.ID) })
	return user
}

func membersOf(t *testing.T, tm *storage.Team) []string {
	t.Helper()
	got, err := storage.GetTeam(context.Background(), testPool, tm.ID)
	require.NoError(t, err)
	require.NotNil(t, got)
	return got.MemberIDs
}

func recipientRule(projectIDs []string, recipients ...string) *storage.AlertRule {
	return &storage.AlertRule{
		Name: "access-" + uuid.NewString(), Enabled: true, Trigger: "new_issue", Channel: "push",
		ProjectIDs: projectIDs, RecipientIDs: recipients, CooldownMins: 60,
	}
}

// A recipient must see every project of the rule, and a rule for all projects can
// only notify users who see all of them. The same holds when only the projects of an
// existing rule change.
func TestAlertRuleRecipients_mustSeeEveryProjectOfTheRule(t *testing.T) {
	ctx := context.Background()
	member := accessUser(t, storage.AccessTeams)
	everything := accessUser(t, storage.AccessAll)
	mine := teamProject(t, accessTeam(t, member))
	other := accessProject(t)

	_, err := storage.CreateAlertRule(ctx, testPool, recipientRule([]string{mine.ID}, member.ID, everything.ID))
	require.NoError(t, err)

	for name, projects := range map[string][]string{
		"another project":         {other.ID},
		"one visible, one not":    {mine.ID, other.ID},
		"a rule for all projects": nil,
	} {
		_, err := storage.CreateAlertRule(ctx, testPool, recipientRule(projects, member.ID))
		require.ErrorIs(t, err, storage.ErrRecipientCannotSeeRule, name)
	}

	rule, err := storage.CreateAlertRule(ctx, testPool, recipientRule([]string{mine.ID}, member.ID))
	require.NoError(t, err)
	rule.ProjectIDs = []string{mine.ID, other.ID}
	_, err = storage.UpdateAlertRule(ctx, testPool, rule)
	require.ErrorIs(t, err, storage.ErrRecipientCannotSeeRule, "widening the projects re-checks the recipients")
}

// A recipient who lost access after the rule was saved stops receiving its pushes,
// even if nothing purged them from the rule.
func TestListPushSubscriptionsForRule_skipsRecipientsWhoLostAccess(t *testing.T) {
	ctx := context.Background()
	member := accessUser(t, storage.AccessTeams)
	everything := accessUser(t, storage.AccessAll)
	team := accessTeam(t, member)
	p := teamProject(t, team)
	for _, u := range []*storage.User{member, everything} {
		_, err := storage.UpsertPushSubscription(ctx, testPool, &storage.PushSubscription{
			UserID: u.ID, Endpoint: "https://push.example.com/" + uuid.NewString(), P256dh: "p", Auth: "a",
		}, storage.PushUpsertOptions{})
		require.NoError(t, err)
	}
	rule, err := storage.CreateAlertRule(ctx, testPool, recipientRule([]string{p.ID}, member.ID, everything.ID))
	require.NoError(t, err)

	subs, err := storage.ListPushSubscriptionsForRule(ctx, testPool, rule.ID, nil)
	require.NoError(t, err)
	require.Len(t, subs, 2)

	// Straight to the table: SetTeamMembers would also purge the recipient, and this
	// checks the filter applied when pushes go out, which covers any other way
	// access can change.
	_, err = testPool.Exec(ctx, `DELETE FROM team_members WHERE team_id = $1`, team.ID)
	require.NoError(t, err)
	subs, err = storage.ListPushSubscriptionsForRule(ctx, testPool, rule.ID, nil)
	require.NoError(t, err)
	require.Len(t, subs, 1)
	require.Equal(t, everything.ID, subs[0].UserID)
}

func ruleRecipients(t *testing.T, id string) []string {
	t.Helper()
	r, err := storage.GetAlertRule(context.Background(), testPool, id)
	require.NoError(t, err)
	require.NotNil(t, r)
	return r.RecipientIDs
}

// Taking access away from a recipient, by any of the four ways, removes them from
// the rules they could no longer see. Someone who still sees every project stays.
func TestAlertRuleRecipients_areDroppedWhenAccessIsTakenAway(t *testing.T) {
	ctx := context.Background()
	everything := accessUser(t, storage.AccessAll)

	cases := map[string]func(t *testing.T, member *storage.User, team *storage.Team, p *storage.Project){
		"the member leaves the team": func(t *testing.T, _ *storage.User, team *storage.Team, _ *storage.Project) {
			_, err := storage.SetTeamMembers(ctx, testPool, team.ID, nil)
			require.NoError(t, err)
		},
		"the project leaves the team": func(t *testing.T, _ *storage.User, _ *storage.Team, p *storage.Project) {
			_, err := storage.SetProjectTeams(ctx, testPool, p.ID, nil)
			require.NoError(t, err)
		},
		"the team is deleted": func(t *testing.T, _ *storage.User, team *storage.Team, _ *storage.Project) {
			deleted, err := storage.DeleteTeam(ctx, testPool, team.ID)
			require.NoError(t, err)
			require.True(t, deleted)
		},
	}
	for name, takeAway := range cases {
		t.Run(name, func(t *testing.T) {
			member := accessUser(t, storage.AccessTeams)
			team := accessTeam(t, member)
			p := teamProject(t, team)
			rule, err := storage.CreateAlertRule(ctx, testPool, recipientRule([]string{p.ID}, member.ID, everything.ID))
			require.NoError(t, err)

			takeAway(t, member, team, p)

			require.Equal(t, []string{everything.ID}, ruleRecipients(t, rule.ID))
		})
	}
}

// A rule whose only recipient loses access is paused, like one whose only recipient
// is deleted, so the rule list shows it needs a recipient.
func TestAlertRuleRecipients_ruleWithoutRecipientsIsPaused(t *testing.T) {
	ctx := context.Background()
	member := accessUser(t, storage.AccessTeams)
	team := accessTeam(t, member)
	p := teamProject(t, team)
	rule, err := storage.CreateAlertRule(ctx, testPool, recipientRule([]string{p.ID}, member.ID))
	require.NoError(t, err)

	_, err = storage.SetTeamMembers(ctx, testPool, team.ID, nil)
	require.NoError(t, err)

	got, err := storage.GetAlertRule(ctx, testPool, rule.ID)
	require.NoError(t, err)
	require.Empty(t, got.RecipientIDs)
	require.False(t, got.Enabled)
}

// Limiting a user to their teams drops them from the rules that cover all projects,
// and from the ones that cover a project none of their teams has.
func TestAlertRuleRecipients_areDroppedWhenAUserIsLimitedToTheirTeams(t *testing.T) {
	ctx := context.Background()
	user := accessUser(t, storage.AccessAll)
	other := accessUser(t, storage.AccessAll)
	team := accessTeam(t, user)
	mine := teamProject(t, team)
	elsewhere := accessProject(t)

	global, err := storage.CreateAlertRule(ctx, testPool, recipientRule(nil, user.ID, other.ID))
	require.NoError(t, err)
	covered, err := storage.CreateAlertRule(ctx, testPool, recipientRule([]string{mine.ID}, user.ID, other.ID))
	require.NoError(t, err)
	uncovered, err := storage.CreateAlertRule(ctx, testPool, recipientRule([]string{mine.ID, elsewhere.ID}, user.ID, other.ID))
	require.NoError(t, err)

	_, err = storage.SetUserProjectAccess(ctx, testPool, user.ID, storage.AccessTeams)
	require.NoError(t, err)

	require.Equal(t, []string{other.ID}, ruleRecipients(t, global.ID))
	require.ElementsMatch(t, []string{user.ID, other.ID}, ruleRecipients(t, covered.ID))
	require.Equal(t, []string{other.ID}, ruleRecipients(t, uncovered.ID))
}

// Each way of taking access away can be previewed first: the preview names who comes
// off which rule and which push rule is paused, and leaves everything as it was. The
// real change then does exactly what the preview said.
func TestPreviewAccessChange_reportsAlertImpactWithoutChangingAnything(t *testing.T) {
	ctx := context.Background()
	everything := accessUser(t, storage.AccessAll)

	type scenario struct {
		preview func(member *storage.User, team *storage.Team, p *storage.Project) (*storage.AlertImpact, error)
		apply   func(member *storage.User, team *storage.Team, p *storage.Project)
	}
	cases := map[string]scenario{
		"the member leaves the team": {
			preview: func(_ *storage.User, team *storage.Team, _ *storage.Project) (*storage.AlertImpact, error) {
				return storage.PreviewTeamMembers(ctx, testPool, team.ID, nil)
			},
			apply: func(_ *storage.User, team *storage.Team, _ *storage.Project) {
				_, err := storage.SetTeamMembers(ctx, testPool, team.ID, nil)
				require.NoError(t, err)
			},
		},
		"the project leaves the team": {
			preview: func(_ *storage.User, _ *storage.Team, p *storage.Project) (*storage.AlertImpact, error) {
				return storage.PreviewProjectTeams(ctx, testPool, p.ID, nil)
			},
			apply: func(_ *storage.User, _ *storage.Team, p *storage.Project) {
				_, err := storage.SetProjectTeams(ctx, testPool, p.ID, nil)
				require.NoError(t, err)
			},
		},
		"the team is deleted": {
			preview: func(_ *storage.User, team *storage.Team, _ *storage.Project) (*storage.AlertImpact, error) {
				return storage.PreviewDeleteTeam(ctx, testPool, team.ID)
			},
			apply: func(_ *storage.User, team *storage.Team, _ *storage.Project) {
				_, err := storage.DeleteTeam(ctx, testPool, team.ID)
				require.NoError(t, err)
			},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			member := accessUser(t, storage.AccessTeams)
			team := accessTeam(t, member)
			p := teamProject(t, team)
			shared, err := storage.CreateAlertRule(ctx, testPool, recipientRule([]string{p.ID}, member.ID, everything.ID))
			require.NoError(t, err)
			alone, err := storage.CreateAlertRule(ctx, testPool, recipientRule([]string{p.ID}, member.ID))
			require.NoError(t, err)

			impact, err := c.preview(member, team, p)
			require.NoError(t, err)
			require.NotNil(t, impact)
			require.ElementsMatch(t, []storage.RecipientRemoval{
				{UserID: member.ID, UserName: member.Name, RuleID: shared.ID, RuleName: shared.Name},
				{UserID: member.ID, UserName: member.Name, RuleID: alone.ID, RuleName: alone.Name},
			}, impact.RemovedRecipients)
			require.Equal(t, []storage.PausedRule{{ID: alone.ID, Name: alone.Name}}, impact.PausedRules)

			// Nothing changed.
			require.ElementsMatch(t, []string{member.ID, everything.ID}, ruleRecipients(t, shared.ID))
			require.Equal(t, []string{member.ID}, ruleRecipients(t, alone.ID))
			got, err := storage.GetAlertRule(ctx, testPool, alone.ID)
			require.NoError(t, err)
			require.True(t, got.Enabled)

			c.apply(member, team, p)

			require.Equal(t, []string{everything.ID}, ruleRecipients(t, shared.ID))
			require.Empty(t, ruleRecipients(t, alone.ID))
			got, err = storage.GetAlertRule(ctx, testPool, alone.ID)
			require.NoError(t, err)
			require.False(t, got.Enabled)
		})
	}
}

// A change that touches no alert rule previews as empty lists, not null.
func TestPreviewAccessChange_nothingAffectedIsEmpty(t *testing.T) {
	ctx := context.Background()
	member := accessUser(t, storage.AccessTeams)
	team := accessTeam(t, member)

	impact, err := storage.PreviewTeamMembers(ctx, testPool, team.ID, []string{member.ID})
	require.NoError(t, err)
	require.NotNil(t, impact)
	require.NotNil(t, impact.RemovedRecipients)
	require.Empty(t, impact.RemovedRecipients)
	require.NotNil(t, impact.PausedRules)
	require.Empty(t, impact.PausedRules)
}

// Limiting a user to their teams previews like the change itself, including its refusals.
func TestPreviewUserProjectAccess(t *testing.T) {
	ctx := context.Background()
	user := accessUser(t, storage.AccessAll)
	rule, err := storage.CreateAlertRule(ctx, testPool, recipientRule(nil, user.ID))
	require.NoError(t, err)

	impact, err := storage.PreviewUserProjectAccess(ctx, testPool, user.ID, storage.AccessTeams)
	require.NoError(t, err)
	require.Equal(t, []storage.RecipientRemoval{{UserID: user.ID, UserName: user.Name, RuleID: rule.ID, RuleName: rule.Name}}, impact.RemovedRecipients)
	require.Equal(t, []storage.PausedRule{{ID: rule.ID, Name: rule.Name}}, impact.PausedRules)
	require.Equal(t, []string{user.ID}, ruleRecipients(t, rule.ID))
	got, err := storage.GetUserByID(ctx, testPool, user.ID)
	require.NoError(t, err)
	require.Equal(t, storage.AccessAll, got.ProjectAccess)

	_, err = storage.PreviewUserProjectAccess(ctx, testPool, user.ID, "everything")
	require.ErrorIs(t, err, storage.ErrInvalidAccess)

	admin := accessUser(t, storage.AccessAll)
	_, err = storage.UpdateUserPermissions(ctx, testPool, admin.ID, storage.UserPermissions{ManageUsers: true})
	require.NoError(t, err)
	_, err = storage.PreviewUserProjectAccess(ctx, testPool, admin.ID, storage.AccessTeams)
	require.ErrorIs(t, err, storage.ErrRestrictedUserPermissions)
}

// Previews of something that does not exist answer nil, like the changes do.
func TestPreviewAccessChange_unknownTargetIsNil(t *testing.T) {
	ctx := context.Background()
	missing := uuid.NewString()

	impact, err := storage.PreviewTeamMembers(ctx, testPool, missing, nil)
	require.NoError(t, err)
	require.Nil(t, impact)
	impact, err = storage.PreviewProjectTeams(ctx, testPool, missing, nil)
	require.NoError(t, err)
	require.Nil(t, impact)
	impact, err = storage.PreviewDeleteTeam(ctx, testPool, missing)
	require.NoError(t, err)
	require.Nil(t, impact)
	impact, err = storage.PreviewUserProjectAccess(ctx, testPool, missing, storage.AccessTeams)
	require.NoError(t, err)
	require.Nil(t, impact)
}
