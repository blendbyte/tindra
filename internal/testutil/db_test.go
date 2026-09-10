package testutil

import (
	"context"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestContainerFailureExitsUnsuccessfully(t *testing.T) {
	if os.Getenv("TINDRA_TEST_FAILURE_CHILD") == "1" {
		startPostgres = func(context.Context, string, ...testcontainers.ContainerCustomizer) (*tcpostgres.PostgresContainer, error) {
			return nil, errors.New("simulated unavailable postgres")
		}
		SetupDB(context.Background())
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestContainerFailureExitsUnsuccessfully$")
	cmd.Env = append(os.Environ(), "TINDRA_TEST_FAILURE_CHILD=1", "TINDRA_TEST_DSN=")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 1, exit.ExitCode())
	require.Contains(t, string(out), "postgres container is required")
}

func TestTemplateFingerprintIncludesNamesAndContents(t *testing.T) {
	a := fstest.MapFS{"0001.sql": {Data: []byte("SELECT 1;")}}
	first, err := templateName(a)
	require.NoError(t, err)
	again, err := templateName(a)
	require.NoError(t, err)
	require.Equal(t, first, again)
	for _, changed := range []fstest.MapFS{
		{"0001.sql": {Data: []byte("SELECT 2;")}},
		{"0002.sql": {Data: []byte("SELECT 1;")}},
		{"0001.sql": {Data: []byte("SELECT 1;")}, "0002.sql": {Data: []byte("SELECT 2;")}},
	} {
		name, err := templateName(changed)
		require.NoError(t, err)
		require.NotEqual(t, first, name)
		require.LessOrEqual(t, len(name), 63)
	}
	_, err = templateName(fstest.MapFS{})
	require.ErrorContains(t, err, "no migrations")
}

// failingMigrationFS models listing and read failures independently.
type failingMigrationFS struct {
	fs.FS
	listErr error
}

func (f failingMigrationFS) Glob(string) ([]string, error) {
	return []string{"0001.sql"}, f.listErr
}

func TestMigrationFilesystemErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		files fs.FS
		want  string
	}{
		{"empty", fstest.MapFS{}, "no migrations"},
		{"listing", failingMigrationFS{FS: fstest.MapFS{}, listErr: fs.ErrPermission}, "list migrations"},
		{"reading", failingMigrationFS{FS: fstest.MapFS{}}, "read migration 0001.sql"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := templateName(tc.files)
			require.ErrorContains(t, err, tc.want)
			_, err = ensureTemplateDB(t.Context(), "", tc.files)
			require.ErrorContains(t, err, tc.want)
			require.ErrorContains(t, applyMigrations(t.Context(), nil, tc.files), tc.want)
		})
	}
	files := fstest.MapFS{"0001.sql": {Data: []byte("SELECT 1;")}}
	_, err := ensureTemplateDB(t.Context(), "://invalid", files)
	require.ErrorContains(t, err, "connect admin for template")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = ensureTemplateDB(ctx, "postgres://localhost/postgres", files)
	require.ErrorContains(t, err, "acquire template connection")
}

func TestTemplatesAreVersionedReusableAndRecoverable(t *testing.T) {
	t.Setenv("TINDRA_TEST_DSN", "")
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	admin, dsn, cleanup := SetupDBWithDSN(ctx)
	defer cleanup()
	files := fstest.MapFS{"0001.sql": {Data: []byte("CREATE TABLE revision_one (id integer);\n-- tindra:next-batch\nCREATE INDEX CONCURRENTLY revision_one_idx ON revision_one(id);")}}
	name, err := templateName(files)
	require.NoError(t, err)
	// Simulate a process interrupted after creating its database.
	_, err = admin.Exec(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	var initialOID uint32
	require.NoError(t, admin.QueryRow(ctx, "SELECT oid FROM pg_database WHERE datname=$1", name).Scan(&initialOID))
	actual, err := ensureTemplateDB(ctx, dsn, files)
	require.NoError(t, err)
	require.Equal(t, name, actual)
	var builtOID uint32
	require.NoError(t, admin.QueryRow(ctx, "SELECT oid FROM pg_database WHERE datname=$1", name).Scan(&builtOID))
	require.NotEqual(t, initialOID, builtOID, "partial templates must be rebuilt")

	// Several callers must reuse the same completed template without rebuilding.
	errors := make(chan error, 4)
	for range 4 {
		go func() { _, err := ensureTemplateDB(ctx, dsn, files); errors <- err }()
	}
	for range 4 {
		require.NoError(t, <-errors)
	}
	var reusedOID uint32
	require.NoError(t, admin.QueryRow(ctx, "SELECT oid FROM pg_database WHERE datname=$1", name).Scan(&reusedOID))
	require.Equal(t, builtOID, reusedOID)

	changed := fstest.MapFS{"0001.sql": {Data: []byte("CREATE TABLE revision_two (id integer);")}}
	// First-time callers must serialize creation as well as reuse.
	second, err := templateName(changed)
	require.NoError(t, err)
	for range 4 {
		go func() { _, err := ensureTemplateDB(ctx, dsn, changed); errors <- err }()
	}
	for range 4 {
		require.NoError(t, <-errors)
	}
	require.NotEqual(t, name, second)
	for template, table := range map[string]string{name: "revision_one", second: "revision_two"} {
		cfg := admin.Config()
		cfg.ConnConfig.Database = template
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		require.NoError(t, err)
		var exists bool
		err = pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists)
		pool.Close()
		require.NoError(t, err)
		require.True(t, exists)
	}

	broken := fstest.MapFS{"0001.sql": {Data: []byte("INVALID SQL")}}
	brokenName, err := templateName(broken)
	require.NoError(t, err)
	for range 2 {
		_, err := ensureTemplateDB(ctx, dsn, broken)
		require.ErrorContains(t, err, "apply migration")
		var ready bool
		require.NoError(t, admin.QueryRow(ctx, `SELECT COALESCE(shobj_description(oid,'pg_database')=$2,false) FROM pg_database WHERE datname=$1`, brokenName, templateReady).Scan(&ready))
		require.False(t, ready, "failed migrations must never publish a reusable template")
	}

	t.Run("lock timeout", func(t *testing.T) {
		conn, err := admin.Acquire(ctx)
		require.NoError(t, err)
		defer conn.Release()
		_, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", templateLockKey)
		require.NoError(t, err)
		defer func() {
			_, err := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", templateLockKey)
			require.NoError(t, err)
		}()
		_, err = ensureTemplateDB(ctx, dsn+"&statement_timeout=100", files)
		require.ErrorContains(t, err, "lock template")
	})

	t.Run("create denied", func(t *testing.T) {
		_, err := admin.Exec(ctx, "CREATE ROLE template_reader LOGIN PASSWORD 'test'")
		require.NoError(t, err)
		restricted, err := url.Parse(dsn)
		require.NoError(t, err)
		restricted.User = url.UserPassword("template_reader", "test")
		fresh := fstest.MapFS{"denied.sql": {Data: []byte("SELECT 1;")}}
		_, err = ensureTemplateDB(ctx, restricted.String(), fresh)
		require.ErrorContains(t, err, "create template")
	})

	t.Run("active incomplete template", func(t *testing.T) {
		cfg := admin.Config()
		cfg.ConnConfig.Database = brokenName
		active, err := pgxpool.NewWithConfig(ctx, cfg)
		require.NoError(t, err)
		defer active.Close()
		require.NoError(t, active.Ping(ctx))
		_, err = ensureTemplateDB(ctx, dsn+"&statement_timeout=100", broken)
		require.ErrorContains(t, err, "drop incomplete template")
		require.NoError(t, active.Ping(ctx), "an active database must not be forcibly dropped")
	})

	// Exercise the public DSN path and confirm cloned databases have the current schema.
	t.Setenv("TINDRA_TEST_DSN", dsn)
	for range 2 {
		pool, cleanup := SetupDB(ctx)
		var exists bool
		err := pool.QueryRow(ctx, "SELECT to_regclass('cron_checkins_completed_retention') IS NOT NULL").Scan(&exists)
		dbName := pool.Config().ConnConfig.Database
		cleanup()
		require.NoError(t, err)
		require.True(t, exists)
		require.NoError(t, admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", dbName).Scan(&exists))
		require.False(t, exists, "cleanup must remove only the test clone")
	}
}
