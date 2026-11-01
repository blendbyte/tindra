package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/migrations"
)

// Migration 0038 can run again after a failed attempt, drops the unused log
// attributes index, leaves the alert evaluator an index for regressions and
// tunes autovacuum on the large tables.
func TestMigration0038IsIdempotent(t *testing.T) {
	ctx := context.Background()
	sql, err := migrations.FS.ReadFile("0038_index_and_vacuum_upkeep.up.sql")
	require.NoError(t, err)
	for _, batch := range migrations.Batches(string(sql)) {
		_, err := testPool.Exec(ctx, batch)
		require.NoError(t, err)
	}

	var names []string
	rows, err := testPool.Query(ctx, `SELECT indexname FROM pg_indexes WHERE indexname IN ('issues_regressed_at', 'logs_attributes') ORDER BY 1`)
	require.NoError(t, err)
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		names = append(names, n)
	}
	rows.Close()
	require.Equal(t, []string{"issues_regressed_at"}, names)

	for _, table := range []string{"events", "event_tags", "transactions", "spans", "logs", "issues"} {
		var options []string
		require.NoError(t, testPool.QueryRow(ctx, `SELECT reloptions FROM pg_class WHERE relname = $1 AND relkind = 'r'`, table).Scan(&options))
		require.Contains(t, options, "autovacuum_vacuum_scale_factor=0.02", table)
	}

	plan := explainWithoutSeqScan(t, `SELECT id FROM issues WHERE regressed_at > $1`, []any{time.Now().Add(-time.Minute)})
	require.Contains(t, plan, "issues_regressed_at")
}
