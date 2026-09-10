package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestSetupStorageFailuresAreReturned(t *testing.T) {
	ctx := t.Context()
	p, err := storage.CreateProject(ctx, testPool, "setup-storage-"+uuid.NewString(), "Setup")
	require.NoError(t, err)
	for _, stage := range []string{"begin", "SELECT id FROM projects", "DELETE FROM project_setup_checks", "INSERT INTO project_setup_checks", "commit"} {
		t.Run(stage, func(t *testing.T) {
			trace := &cancelGroupingQuery{match: stage}
			cfg := testPool.Config()
			cfg.ConnConfig.Tracer = trace
			failing, err := pgxpool.NewWithConfig(ctx, cfg)
			require.NoError(t, err)
			defer failing.Close()
			_, err = storage.StartSetupCheck(ctx, failing, p.ID)
			require.Error(t, err)
			require.True(t, trace.hit.Load())
			var count int
			require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM project_setup_checks WHERE project_id=$1`, p.ID).Scan(&count))
			require.Zero(t, count)
		})
	}
	closed, err := pgxpool.NewWithConfig(ctx, testPool.Config())
	require.NoError(t, err)
	closed.Close()
	_, err = storage.ListSetupReceipts(ctx, closed, p.ID)
	require.Error(t, err)
	_, err = storage.ListSetupObservations(ctx, closed, p.ID)
	require.Error(t, err)
	_, err = storage.GetSetupCheck(ctx, closed, p.ID, uuid.NewString())
	require.Error(t, err)
	_, err = storage.StartSetupCheck(ctx, closed, p.ID)
	require.Error(t, err)
	err = storage.RecordSetupObservations(ctx, closed, p.ID, nil)
	require.NoError(t, err)
	err = storage.RecordSetupObservations(ctx, testPool, p.ID, []storage.SetupObservation{{ObservedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}})
	require.Error(t, err)
}

func TestSetupObservationWindowAndReplacement(t *testing.T) {
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "setup-observation-"+uuid.NewString(), "Setup")
	require.NoError(t, err)
	now := time.Now().UTC()
	record := func(reason string, at time.Time) {
		t.Helper()
		require.NoError(t, storage.RecordSetupObservations(ctx, testPool, p.ID, []storage.SetupObservation{{Kind: "events", Outcome: "rejected", Reason: reason, ObservedAt: at}}))
	}
	record("buffer_full", now.Add(-2*time.Hour))
	rows, err := storage.ListSetupObservations(ctx, testPool, p.ID)
	require.NoError(t, err)
	require.Empty(t, rows)
	record("buffer_full", now)
	record("buffer_full", now.Add(time.Second))
	rows, err = storage.ListSetupObservations(ctx, testPool, p.ID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.WithinDuration(t, now, rows[0].ObservedAt, time.Microsecond)
	record("storage_failed", now.Add(2*time.Second))
	record("buffer_full", now.Add(-time.Second))
	rows, err = storage.ListSetupObservations(ctx, testPool, p.ID)
	require.NoError(t, err)
	require.Equal(t, "storage_failed", rows[0].Reason)
}

func TestSetupCheckLookupIsProjectScoped(t *testing.T) {
	ctx := t.Context()
	project, err := storage.CreateProject(ctx, testPool, "check-owner-"+uuid.NewString(), "Owner")
	require.NoError(t, err)
	other, err := storage.CreateProject(ctx, testPool, "check-other-"+uuid.NewString(), "Other")
	require.NoError(t, err)
	check, err := storage.StartSetupCheck(ctx, testPool, project.ID)
	require.NoError(t, err)
	found, err := storage.GetSetupCheck(ctx, testPool, project.ID, check.ID)
	require.NoError(t, err)
	require.Equal(t, check, found)
	found, err = storage.GetSetupCheck(ctx, testPool, other.ID, check.ID)
	require.NoError(t, err)
	require.Nil(t, found)
	found, err = storage.GetSetupCheck(ctx, testPool, project.ID, uuid.NewString())
	require.NoError(t, err)
	require.Nil(t, found)
}

func TestSetupReceiptsSurviveEventRetention(t *testing.T) {
	ctx := t.Context()
	project, err := storage.CreateProject(ctx, testPool, "receipt-owner-"+uuid.NewString(), "Owner")
	require.NoError(t, err)
	receipts, err := storage.ListSetupReceipts(ctx, testPool, project.ID)
	require.NoError(t, err)
	require.Empty(t, receipts)
	var eventID string
	require.NoError(t, testPool.QueryRow(ctx, `INSERT INTO events(project_id,timestamp,payload) VALUES($1,now(),'{}') RETURNING id`, project.ID).Scan(&eventID))
	receipts, err = storage.ListSetupReceipts(ctx, testPool, project.ID)
	require.NoError(t, err)
	require.Len(t, receipts, 1)
	require.Equal(t, "events", receipts[0].Kind)
	require.Equal(t, eventID, receipts[0].LatestID)
	require.False(t, receipts[0].FirstReceivedAt.IsZero())
	require.Equal(t, receipts[0].FirstReceivedAt, receipts[0].LastReceivedAt)
	_, err = testPool.Exec(ctx, `DELETE FROM events WHERE id=$1`, eventID)
	require.NoError(t, err)
	after, err := storage.ListSetupReceipts(ctx, testPool, project.ID)
	require.NoError(t, err)
	require.Equal(t, receipts, after)
}
