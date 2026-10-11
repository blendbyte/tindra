package storage_test

import (
	"context"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/migrations"
)

func TestMonitorSlugFromName(t *testing.T) {
	for name, want := range map[string]string{
		"Nightly Backup":        "nightly-backup",
		"  sync: users -> CRM ": "sync-users-crm",
		"!!!":                   "monitor",
		"ÄÖÜ report":            "report",
	} {
		got := storage.MonitorSlugFromName(name)
		require.Equal(t, want, got, name)
		require.True(t, storage.ValidMonitorSlug(got), got)
	}
	require.False(t, storage.ValidMonitorSlug("-leading-dash"))
	require.False(t, storage.ValidMonitorSlug("Upper"))
}

func TestCreateCronMonitor_slugs(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "slug-proj", "Slug Project")
	require.NoError(t, err)
	create := func(name, slug string) (*storage.CronMonitor, error) {
		return storage.CreateCronMonitor(ctx, testPool, &storage.CronMonitor{
			ProjectID: p.ID, Name: name, Slug: slug, Schedule: "0 * * * *", GracePeriodSecs: 60,
		})
	}

	first, err := create("Nightly Backup", "")
	require.NoError(t, err)
	require.Equal(t, "nightly-backup", first.Slug)
	second, err := create("Nightly backup", "")
	require.NoError(t, err)
	require.Equal(t, "nightly-backup-2", second.Slug, "auto-generated slugs get a suffix on collision")

	_, err = create("Other", "nightly-backup")
	require.ErrorIs(t, err, storage.ErrMonitorSlugTaken, "an explicit slug that is taken is an error")

	found, err := storage.GetCronMonitorBySlug(ctx, testPool, p.ID, "nightly-backup-2")
	require.NoError(t, err)
	require.Equal(t, second.ID, found.ID)
	missing, err := storage.GetCronMonitorBySlug(ctx, testPool, p.ID, "nope")
	require.NoError(t, err)
	require.Nil(t, missing)

	first.Slug = "nightly-backup-2"
	_, err = storage.UpdateCronMonitor(ctx, testPool, first)
	require.ErrorIs(t, err, storage.ErrMonitorSlugTaken)
}

// The backfill in migration 0028 is replayed on pre-slug rows inside a
// transaction that is rolled back afterwards.
func TestCronMonitorSlugBackfillMigration(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "slug-backfill", "Slug Backfill")
	require.NoError(t, err)
	migration, err := fs.ReadFile(migrations.FS, "0028_cron_monitor_slugs.up.sql")
	require.NoError(t, err)

	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err = tx.Exec(ctx, `DROP INDEX cron_monitors_project_slug; ALTER TABLE cron_monitors DROP COLUMN slug`)
	require.NoError(t, err)
	for _, name := range []string{"Nightly Backup", "nightly backup!", "???"} {
		_, err = tx.Exec(ctx, `INSERT INTO cron_monitors (project_id, name, schedule) VALUES ($1, $2, '0 * * * *')`, p.ID, name)
		require.NoError(t, err)
	}
	_, err = tx.Exec(ctx, string(migration))
	require.NoError(t, err)

	rows, err := tx.Query(ctx, `SELECT name, slug FROM cron_monitors WHERE project_id = $1 ORDER BY created_at, id`, p.ID)
	require.NoError(t, err)
	slugs := map[string]string{}
	for rows.Next() {
		var name, slug string
		require.NoError(t, rows.Scan(&name, &slug))
		slugs[name] = slug
	}
	require.NoError(t, rows.Err())
	require.Equal(t, "monitor", slugs["???"])
	require.Contains(t, []string{slugs["Nightly Backup"], slugs["nightly backup!"]}, "nightly-backup")
	require.NotEqual(t, slugs["Nightly Backup"], slugs["nightly backup!"], "duplicates get a suffix")
	for _, slug := range slugs {
		require.True(t, storage.ValidMonitorSlug(slug), slug)
	}
}
