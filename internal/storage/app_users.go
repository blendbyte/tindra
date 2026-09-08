package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AppUser is an end-user of the instrumented app, keyed by identity
// (COALESCE(id, username, email)) per project.
type AppUser struct {
	Identity  string    `json:"identity"`
	UserID    *string   `json:"user_id"`
	Username  *string   `json:"username"`
	Email     *string   `json:"email"`
	Name      *string   `json:"name"`
	LastSeen  time.Time `json:"last_seen"`
	ProjectID string    `json:"project_id"`
}

// ListAppUsers returns recently seen app users, optionally filtered by a
// case-insensitive substring on identity / username / email / name.
func ListAppUsers(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, search string, limit int) ([]*AppUser, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if projectIDs == nil {
		projectIDs = []string{}
	}

	args := []any{projectIDs}
	where := `(CARDINALITY($1::uuid[]) = 0 OR project_id = ANY($1::uuid[]))`
	if search != "" {
		args = append(args, likeContains(search))
		n := len(args)
		where += fmt.Sprintf(` AND (
			identity ILIKE $%d ESCAPE E'\\'
			OR COALESCE(username, '') ILIKE $%d ESCAPE E'\\'
			OR COALESCE(email, '') ILIKE $%d ESCAPE E'\\'
			OR COALESCE(name, '') ILIKE $%d ESCAPE E'\\'
		)`, n, n, n, n)
	}

	args = append(args, limit)
	var q string
	if search == "" {
		q = fmt.Sprintf(`
			SELECT identity, user_id, username, email, name, last_seen, project_id::text
			FROM app_users
			WHERE %s
			ORDER BY last_seen DESC
			LIMIT $%d
		`, where, len(args))
	} else {
		q = fmt.Sprintf(`
			SELECT identity, user_id, username, email, name, last_seen, project_id::text
			FROM (
				SELECT DISTINCT ON (identity)
					identity, user_id, username, email, name, last_seen, project_id
				FROM app_users
				WHERE %s
				ORDER BY identity, last_seen DESC
			) u
			ORDER BY last_seen DESC
			LIMIT $%d
		`, where, len(args))
	}

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list app users: %w", err)
	}
	defer rows.Close()

	var out []*AppUser
	for rows.Next() {
		var u AppUser
		if err := rows.Scan(&u.Identity, &u.UserID, &u.Username, &u.Email, &u.Name, &u.LastSeen, &u.ProjectID); err != nil {
			return nil, fmt.Errorf("scan app user: %w", err)
		}
		out = append(out, &u)
	}
	return out, rows.Err()
}

// GetAppUser returns the most recently seen row for an identity across the
// given projects. Nil if none.
func GetAppUser(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, identity string) (*AppUser, error) {
	if identity == "" {
		return nil, nil
	}
	if projectIDs == nil {
		projectIDs = []string{}
	}
	var u AppUser
	err := pool.QueryRow(ctx, `
		SELECT identity, user_id, username, email, name, last_seen, project_id::text
		FROM app_users
		WHERE identity = $1
		  AND (CARDINALITY($2::uuid[]) = 0 OR project_id = ANY($2::uuid[]))
		ORDER BY last_seen DESC
		LIMIT 1
	`, identity, projectIDs).Scan(&u.Identity, &u.UserID, &u.Username, &u.Email, &u.Name, &u.LastSeen, &u.ProjectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get app user: %w", err)
	}
	return &u, nil
}
