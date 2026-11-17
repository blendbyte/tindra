package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/migrations"
)

// Migration 0041 turns each rule's single environment filter into a list.
// It runs inside a rolled-back transaction on a table put back into its 0040
// shape, so the shared test database keeps the migrated schema.
func TestMigration0041ConvertsEnvironmentFilter(t *testing.T) {
	ctx := context.Background()
	sql, err := migrations.FS.ReadFile("0041_alert_filter_environments.up.sql")
	require.NoError(t, err)

	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		ALTER TABLE alert_rules DROP COLUMN filter_environments;
		ALTER TABLE alert_rules ADD COLUMN filter_environment TEXT;
		INSERT INTO alert_rules (name, trigger, channel, cooldown_mins, filter_environment) VALUES
			('m41-prod', 'new_issue', 'email', 60, 'production'),
			('m41-blank', 'new_issue', 'email', 60, ''),
			('m41-none', 'new_issue', 'email', 60, NULL)`)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, string(sql))
	require.NoError(t, err)

	got := map[string][]string{}
	rows, err := tx.Query(ctx, `SELECT name, filter_environments FROM alert_rules WHERE name LIKE 'm41-%'`)
	require.NoError(t, err)
	for rows.Next() {
		var name string
		var envs []string
		require.NoError(t, rows.Scan(&name, &envs))
		got[name] = envs
	}
	require.NoError(t, rows.Err())
	require.Equal(t, map[string][]string{
		"m41-prod":  {"production"},
		"m41-blank": {},
		"m41-none":  {},
	}, got)

	var oldColumn bool
	require.NoError(t, tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'alert_rules' AND column_name = 'filter_environment')`).Scan(&oldColumn))
	require.False(t, oldColumn, "filter_environment should be dropped")
}
