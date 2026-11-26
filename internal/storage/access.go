package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrTeamNameTaken is returned when a team name is already in use.
var ErrTeamNameTaken = errors.New("team name already taken")

// ErrUnknownReference is returned when a team membership, a project's teams or an
// invitation's teams point at a user, team or project that does not exist.
var ErrUnknownReference = errors.New("unknown user, team or project")

// ProjectAccess is the set of projects a user may see.
//
// All means no restriction applies, in which case IDs is unused. Otherwise IDs
// lists the visible projects and may be empty: callers must not treat an empty
// IDs as "no filter", which is what an empty project list means almost
// everywhere else in this package.
type ProjectAccess struct {
	All bool
	IDs []string
}

// Allows reports whether the project is visible.
func (a ProjectAccess) Allows(projectID string) bool {
	if a.All {
		return true
	}
	for _, id := range a.IDs {
		if id == projectID {
			return true
		}
	}
	return false
}

// GetProjectAccess returns the projects a user may see. A user with AccessAll sees
// every project. One with AccessTeams sees the projects of the teams they belong
// to, and none when they belong to no team.
func GetProjectAccess(ctx context.Context, pool *pgxpool.Pool, userID, projectAccess string) (ProjectAccess, error) {
	if projectAccess != AccessTeams {
		return ProjectAccess{All: true}, nil
	}
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT pt.project_id FROM project_teams pt
		JOIN team_members m ON m.team_id = pt.team_id
		WHERE m.user_id = $1
	`, userID)
	if err != nil {
		return ProjectAccess{}, fmt.Errorf("query accessible projects: %w", err)
	}
	defer rows.Close()
	access := ProjectAccess{IDs: []string{}}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return ProjectAccess{}, fmt.Errorf("scan accessible project: %w", err)
		}
		access.IDs = append(access.IDs, id)
	}
	return access, rows.Err()
}

// Team is a named set of users that projects can be attached to.
type Team struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MemberIDs []string  `json:"member_ids"`
	CreatedAt time.Time `json:"created_at"`
}

const teamSelect = `
	SELECT t.id, t.name, t.created_at,
	       COALESCE(ARRAY(SELECT m.user_id FROM team_members m WHERE m.team_id = t.id ORDER BY m.user_id), '{}')
	FROM teams t`

func scanTeam(row pgx.Row) (*Team, error) {
	var t Team
	if err := row.Scan(&t.ID, &t.Name, &t.CreatedAt, &t.MemberIDs); err != nil {
		return nil, err
	}
	return &t, nil
}

func isUniqueViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23505"
}

func isForeignKeyViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == "23503"
}

// ListTeams returns every team with its member ids, ordered by name.
func ListTeams(ctx context.Context, pool *pgxpool.Pool) ([]*Team, error) {
	rows, err := pool.Query(ctx, teamSelect+` ORDER BY t.name ASC`)
	if err != nil {
		return nil, fmt.Errorf("query teams: %w", err)
	}
	defer rows.Close()
	teams := []*Team{}
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, fmt.Errorf("scan team: %w", err)
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// GetTeam returns one team, or nil when it does not exist.
func GetTeam(ctx context.Context, pool *pgxpool.Pool, id string) (*Team, error) {
	t, err := scanTeam(pool.QueryRow(ctx, teamSelect+` WHERE t.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get team: %w", err)
	}
	return t, nil
}

// CreateTeam adds an empty team. It returns ErrTeamNameTaken for a duplicate name.
func CreateTeam(ctx context.Context, pool *pgxpool.Pool, name string) (*Team, error) {
	var id string
	err := pool.QueryRow(ctx, `INSERT INTO teams (name) VALUES ($1) RETURNING id`, name).Scan(&id)
	if isUniqueViolation(err) {
		return nil, ErrTeamNameTaken
	}
	if err != nil {
		return nil, fmt.Errorf("insert team: %w", err)
	}
	return GetTeam(ctx, pool, id)
}

// RenameTeam renames a team, or returns nil when it does not exist.
func RenameTeam(ctx context.Context, pool *pgxpool.Pool, id, name string) (*Team, error) {
	tag, err := pool.Exec(ctx, `UPDATE teams SET name = $2 WHERE id = $1`, id, name)
	if isUniqueViolation(err) {
		return nil, ErrTeamNameTaken
	}
	if err != nil {
		return nil, fmt.Errorf("rename team: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, nil
	}
	return GetTeam(ctx, pool, id)
}

// DeleteTeam removes a team. Its members and project links cascade.
func DeleteTeam(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete team: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetTeamMembers replaces a team's members. It returns nil when the team does not
// exist and ErrUnknownReference when a user id does not exist.
func SetTeamMembers(ctx context.Context, pool *pgxpool.Pool, id string, userIDs []string) (*Team, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM teams WHERE id = $1 FOR UPDATE)`, id).Scan(&exists); err != nil {
		return nil, fmt.Errorf("lock team: %w", err)
	}
	if !exists {
		return nil, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM team_members WHERE team_id = $1`, id); err != nil {
		return nil, fmt.Errorf("clear members: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO team_members (team_id, user_id)
		SELECT $1, u FROM unnest($2::uuid[]) AS u
		ON CONFLICT DO NOTHING
	`, id, uniqueIDs(userIDs)); err != nil {
		if isForeignKeyViolation(err) {
			return nil, ErrUnknownReference
		}
		return nil, fmt.Errorf("insert members: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return GetTeam(ctx, pool, id)
}

// ProjectTeams lists the teams a project belongs to. Members of those teams see the
// project, and so does every user with access to all projects.
type ProjectTeams struct {
	TeamIDs []string `json:"team_ids"`
}

// GetProjectTeams returns a project's teams, or nil when the project does not exist.
func GetProjectTeams(ctx context.Context, pool *pgxpool.Pool, projectID string) (*ProjectTeams, error) {
	var t ProjectTeams
	err := pool.QueryRow(ctx, `
		SELECT COALESCE(ARRAY(SELECT team_id FROM project_teams WHERE project_id = p.id ORDER BY team_id), '{}')
		FROM projects p WHERE p.id = $1
	`, projectID).Scan(&t.TeamIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get project teams: %w", err)
	}
	return &t, nil
}

// ProjectTeamEntry is one project's teams together with its id.
type ProjectTeamEntry struct {
	ProjectID string `json:"project_id"`
	ProjectTeams
}

// ListProjectTeams returns the teams of every project in one query, for screens that
// show or filter projects by team.
func ListProjectTeams(ctx context.Context, pool *pgxpool.Pool) ([]*ProjectTeamEntry, error) {
	rows, err := pool.Query(ctx, `
		SELECT p.id, COALESCE(ARRAY(SELECT team_id FROM project_teams WHERE project_id = p.id ORDER BY team_id), '{}')
		FROM projects p ORDER BY p.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query project teams: %w", err)
	}
	defer rows.Close()
	entries := []*ProjectTeamEntry{}
	for rows.Next() {
		var e ProjectTeamEntry
		if err := rows.Scan(&e.ProjectID, &e.TeamIDs); err != nil {
			return nil, fmt.Errorf("scan project teams: %w", err)
		}
		entries = append(entries, &e)
	}
	return entries, rows.Err()
}

// SetProjectTeams replaces the teams a project belongs to. It returns nil when the
// project does not exist and ErrUnknownReference when a team does not exist.
func SetProjectTeams(ctx context.Context, pool *pgxpool.Pool, projectID string, teamIDs []string) (*ProjectTeams, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1 FOR UPDATE)`, projectID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("lock project: %w", err)
	}
	if !exists {
		return nil, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM project_teams WHERE project_id = $1`, projectID); err != nil {
		return nil, fmt.Errorf("clear project teams: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO project_teams (project_id, team_id)
		SELECT $1, t FROM unnest($2::uuid[]) AS t
	`, projectID, uniqueIDs(teamIDs)); err != nil {
		if isForeignKeyViolation(err) {
			return nil, ErrUnknownReference
		}
		return nil, fmt.Errorf("insert project teams: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return GetProjectTeams(ctx, pool, projectID)
}

// uniqueIDs drops duplicates, keeping first occurrences. It always returns a
// non-nil slice so the value binds as an empty array rather than NULL.
func uniqueIDs(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; !dup {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}
