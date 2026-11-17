package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/migrations"
)

// Migration 0042 numbers existing issues per project in first-seen order and
// carries on from there. It runs inside a rolled-back transaction on tables
// put back into their 0041 shape, so the shared test database keeps the
// migrated schema.
func TestMigration0042NumbersExistingIssues(t *testing.T) {
	ctx := context.Background()
	sql, err := migrations.FS.ReadFile("0042_issue_short_ids.up.sql")
	require.NoError(t, err)

	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		DROP TABLE issue_short_aliases;
		DROP TRIGGER issues_short_number ON issues;
		DROP FUNCTION issues_assign_short_number();
		DROP TABLE issue_counters;
		ALTER TABLE issues DROP COLUMN short_number;
		DROP TRIGGER projects_issue_prefix ON projects;
		DROP FUNCTION projects_assign_issue_prefix();
		ALTER TABLE projects DROP COLUMN issue_prefix;
		DROP FUNCTION claim_issue_prefix(TEXT);
		DROP TABLE issue_prefixes;`)
	require.NoError(t, err)

	var projectID, twinID string
	require.NoError(t, tx.QueryRow(ctx, `
		INSERT INTO projects (slug, name, public_key, created_at) VALUES ('m42-app', 'M42', 'm42-key', now() - interval '1 day') RETURNING id`).Scan(&projectID))
	// A CLI-made slug that cleans up to the same prefix, created later.
	require.NoError(t, tx.QueryRow(ctx, `
		INSERT INTO projects (slug, name, public_key) VALUES ('m42_app', 'M42 twin', 'm42-twin-key') RETURNING id`).Scan(&twinID))
	_, err = tx.Exec(ctx, `
		INSERT INTO issues (project_id, fingerprint, title, level, kind, first_seen, last_seen) VALUES
			($1, 'm42-newest', 'newest', 'error', 'error', now(), now()),
			($1, 'm42-oldest', 'oldest', 'error', 'error', now() - interval '2 days', now()),
			($1, 'm42-middle', 'middle', 'error', 'error', now() - interval '1 day', now())`, projectID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, string(sql))
	require.NoError(t, err)

	var prefix, twinPrefix string
	require.NoError(t, tx.QueryRow(ctx, `SELECT issue_prefix FROM projects WHERE id = $1`, projectID).Scan(&prefix))
	require.Equal(t, "M42-APP", prefix)
	require.NoError(t, tx.QueryRow(ctx, `SELECT issue_prefix FROM projects WHERE id = $1`, twinID).Scan(&twinPrefix))
	require.Equal(t, "M42-APP2", twinPrefix)

	got := map[string]int64{}
	rows, err := tx.Query(ctx, `SELECT title, short_number FROM issues WHERE project_id = $1`, projectID)
	require.NoError(t, err)
	for rows.Next() {
		var title string
		var n int64
		require.NoError(t, rows.Scan(&title, &n))
		got[title] = n
	}
	require.NoError(t, rows.Err())
	require.Equal(t, map[string]int64{"oldest": 1, "middle": 2, "newest": 3}, got)

	var next int64
	require.NoError(t, tx.QueryRow(ctx, `
		INSERT INTO issues (project_id, fingerprint, title, level, kind, first_seen, last_seen)
		VALUES ($1, 'm42-after', 'after', 'error', 'error', now(), now()) RETURNING short_number`, projectID).Scan(&next))
	require.Equal(t, int64(4), next)
}
