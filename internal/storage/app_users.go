package storage

import (
	"context"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

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
	args := []any{}
	where := "TRUE"
	if len(projectIDs) > 0 {
		args = append(args, projectIDs)
		where = "project_id = ANY($1::uuid[])"
	}
	if search != "" {
		args = append(args, likeContains(search))
		n := len(args)
		if utf8.RuneCountInString(search) < 3 {
			// Without a complete trigram, walk the recent-user index until
			// enough literal matches are found instead of scanning the GIN.
			args[len(args)-1] = search
			where += fmt.Sprintf(` AND (
				strpos(lower(identity), lower($%[1]d)) > 0
				OR strpos(lower(username), lower($%[1]d)) > 0
				OR strpos(lower(email), lower($%[1]d)) > 0
				OR strpos(lower(name), lower($%[1]d)) > 0
			)`, n)
		} else {
			where += fmt.Sprintf(` AND (
			identity ILIKE $%d ESCAPE E'\\'
			OR username ILIKE $%d ESCAPE E'\\'
			OR email ILIKE $%d ESCAPE E'\\'
			OR name ILIKE $%d ESCAPE E'\\'
		)`, n, n, n, n)
		}
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
	} else if len(projectIDs) == 1 {
		// Identity is unique within one project; avoid sorting every match to
		// deduplicate rows that cannot be duplicates.
		q = fmt.Sprintf(`SELECT identity, user_id, username, email, name, last_seen, project_id::text
			FROM app_users WHERE %s ORDER BY last_seen DESC LIMIT $%d`, where, len(args))
	} else if utf8.RuneCountInString(search) < 3 {
		// Short substrings cannot use trigrams. Find at most limit recent
		// matches per project before deduplicating. Any omitted row already
		// has limit distinct, newer identities ahead of it in its own project.
		projectWhere := "TRUE"
		if len(projectIDs) > 0 {
			projectWhere = "p.id = ANY($1::uuid[])"
		}
		q = fmt.Sprintf(`
			SELECT identity, user_id, username, email, name, last_seen, project_id::text
			FROM (
				SELECT DISTINCT ON (u.identity) u.* FROM projects p
				CROSS JOIN LATERAL (
					SELECT identity, user_id, username, email, name, last_seen, project_id
					FROM app_users WHERE project_id = p.id AND %s
					ORDER BY project_id, last_seen DESC LIMIT $%d
				) u WHERE %s
				ORDER BY u.identity, u.last_seen DESC
			) matches ORDER BY last_seen DESC LIMIT $%d
		`, where, len(args), projectWhere, len(args))
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

	rows, err := pool.Query(ctx, q, userQueryArgs(search, args)...)
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
	args := []any{identity}
	projectWhere := ""
	if len(projectIDs) > 0 {
		args = append(args, projectIDs)
		projectWhere = " AND project_id = ANY($2::uuid[])"
	}
	var u AppUser
	err := pool.QueryRow(ctx, `
		SELECT identity, user_id, username, email, name, last_seen, project_id::text
		FROM app_users
		WHERE app_user_identity_hash(identity) = app_user_identity_hash($1::text) AND identity = $1
		  `+projectWhere+`
		ORDER BY last_seen DESC
		LIMIT 1
	`, args...).Scan(&u.Identity, &u.UserID, &u.Username, &u.Email, &u.Name, &u.LastSeen, &u.ProjectID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get app user: %w", err)
	}
	return &u, nil
}
