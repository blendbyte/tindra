package ingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

func TestFailedEventBatchRecordsRejectionWithoutReceipt(t *testing.T) {
	ctx := t.Context()
	pool, cleanup := testutil.SetupDB(ctx)
	defer cleanup()
	p, err := storage.CreateProject(ctx, pool, "failed-setup", "Setup")
	require.NoError(t, err)
	buffer := NewBuffer(2)
	require.True(t, buffer.Push(BufferedEvent{ProjectID: p.ID, Timestamp: time.Now(), Payload: []byte("invalid JSON")}))
	drain, cancel := context.WithCancel(ctx)
	cancel()
	buffer.Run(drain, pool)
	require.EqualValues(t, 1, buffer.Stats().Dropped["invalid_record"])
	observations, err := storage.ListSetupObservations(ctx, pool, p.ID)
	require.NoError(t, err)
	require.Len(t, observations, 1)
	require.Equal(t, "storage_failed", observations[0].Reason)
	receipts, err := storage.ListSetupReceipts(ctx, pool, p.ID)
	require.NoError(t, err)
	require.Empty(t, receipts)
	// An unavailable diagnostics database must respect the shared timeout.
	_, err = pool.Exec(ctx, `CREATE FUNCTION stall_setup_observation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(5); RETURN NEW; END $$; CREATE TRIGGER stall_setup BEFORE INSERT ON project_setup_observations FOR EACH ROW EXECUTE FUNCTION stall_setup_observation()`)
	require.NoError(t, err)
	start := time.Now()
	recordSetupWriteFailure(ctx, pool, "events", []string{p.ID, p.ID}, errors.New("write failed"))
	require.Less(t, time.Since(start), time.Second)
}

func TestSetupDiagnosticsFollowAtomicWriteOutcome(t *testing.T) {
	ctx := t.Context()
	pool, cleanup := testutil.SetupDB(ctx)
	defer cleanup()
	p, err := storage.CreateProject(ctx, pool, "atomic-setup", "Setup")
	require.NoError(t, err)
	recordSetupWriteFailure(ctx, pool, "events", []string{p.ID}, &unknownCommit{errors.New("acknowledgement lost")})
	observations, err := storage.ListSetupObservations(ctx, pool, p.ID)
	require.NoError(t, err)
	require.Empty(t, observations, "an unknown commit must not be reported as failed storage")
	_, err = pool.Exec(ctx, `CREATE FUNCTION reject_setup_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject test write' USING ERRCODE='22000'; END $$`)
	require.NoError(t, err)
	for _, kind := range []string{"events", "transactions", "profile_chunks"} {
		t.Run(kind, func(t *testing.T) {
			_, err := pool.Exec(ctx, "CREATE TRIGGER z_reject_setup AFTER INSERT ON "+kind+" FOR EACH STATEMENT EXECUTE FUNCTION reject_setup_write()")
			require.NoError(t, err)
			drain, cancel := context.WithCancel(ctx)
			cancel()
			switch kind {
			case "events":
				b := NewBuffer(1)
				require.True(t, b.Push(BufferedEvent{ProjectID: p.ID, Timestamp: time.Now(), Payload: []byte(`{}`)}))
				b.Run(drain, pool)
				require.EqualValues(t, 1, b.Stats().Dropped["invalid_record"])
			case "transactions":
				b := NewTransactionBuffer(1)
				require.True(t, b.Push(BufferedTransaction{ProjectID: p.ID, Transaction: "setup", StartTimestamp: time.Now(), Timestamp: time.Now()}))
				b.Hook = func(context.Context, *pgxpool.Pool, []BufferedTransaction, []string) {
					t.Error("hook ran for a rolled-back transaction")
				}
				b.Run(drain, pool)
				require.EqualValues(t, 1, b.Stats().Dropped["invalid_record"])
			case "profile_chunks":
				b := NewProfileBuffer(1)
				require.True(t, b.Push(BufferedProfile{ProjectID: p.ID, Format: ProfileFormatV1, StartTs: time.Now(), EndTs: time.Now(), SampleCount: 1, Encoding: 1, Data: []byte("test")}))
				b.Run(drain, pool)
				require.EqualValues(t, 1, b.Stats().Dropped["invalid_record"])
			}
			receipts, err := storage.ListSetupReceipts(ctx, pool, p.ID)
			require.NoError(t, err)
			require.Empty(t, receipts, "the receipt must roll back with its data")
			observations, err := storage.ListSetupObservations(ctx, pool, p.ID)
			require.NoError(t, err)
			found := false
			for _, o := range observations {
				if o.Kind == kind {
					found = true
					require.Equal(t, "storage_failed", o.Reason)
				}
			}
			require.True(t, found, "diagnostics must survive the rollback")
		})
	}
}

// Deduplication is independent of database latency. Persistence is covered above.
func TestSetupFailureRecordsEachProjectOncePerBatch(t *testing.T) {
	writes := map[string][]storage.SetupObservation{}
	recordSetupFailures(t.Context(), "events", []string{"first", "first", "second", "first", "second"}, errors.New("batch rolled back"),
		func(ctx context.Context, id string, observations []storage.SetupObservation) error {
			require.NoError(t, ctx.Err())
			writes[id] = append(writes[id], observations...)
			return nil
		})
	require.Len(t, writes, 2)
	for _, id := range []string{"first", "second"} {
		require.Len(t, writes[id], 1)
		require.Equal(t, "events", writes[id][0].Kind)
		require.Equal(t, "rejected", writes[id][0].Outcome)
		require.Equal(t, "storage_failed", writes[id][0].Reason)
	}
}
