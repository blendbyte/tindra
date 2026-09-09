package ingest_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func TestBufferedUsageAccounting(t *testing.T) {
	ctx := context.Background()
	_, err := testPool.Exec(ctx, "TRUNCATE events,transactions,logs CASCADE")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := testPool.Exec(ctx, "TRUNCATE events,transactions,logs CASCADE")
		require.NoError(t, err)
	})
	events := ingest.NewBuffer(2052)
	for i := range 2050 {
		id := fmt.Sprint(i % 1000)
		require.True(t, events.Push(ingest.BufferedEvent{ProjectID: testProject.ID, EventID: &id, Timestamp: time.Now(), Payload: []byte(`{}`)}))
	}
	for range 2 {
		require.True(t, events.Push(ingest.BufferedEvent{ProjectID: testProject.ID, Timestamp: time.Now(), Payload: []byte(`{}`)}))
	}
	count, err := storage.CountMonthlyEvents(ctx, testPool)
	require.NoError(t, err)
	require.Zero(t, count, "pending buffers retain the existing soft-quota semantics")
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	events.Run(cancelled, testPool)
	count, err = storage.CountMonthlyEvents(ctx, testPool)
	require.NoError(t, err)
	require.EqualValues(t, 1002, count)
	logs := ingest.NewLogBuffer(10)
	for range 10 {
		require.True(t, logs.Push(ingest.BufferedLog{ProjectID: testProject.ID, Timestamp: time.Now(), Level: "info", Body: "test"}))
	}
	logs.Run(cancelled, testPool)
	count, err = storage.CountMonthlyEvents(ctx, testPool)
	require.NoError(t, err)
	require.EqualValues(t, 1002, count, "logs must not count toward event quota")
	transactions := ingest.NewTransactionBuffer(2050)
	for i := range 2050 {
		id := fmt.Sprint(i)
		require.True(t, transactions.Push(ingest.BufferedTransaction{ProjectID: testProject.ID, EventID: "retry", Transaction: id, Status: "ok", StartTimestamp: time.Now(), Timestamp: time.Now(), Spans: []ingest.BufferedSpan{{SpanID: id, StartTimestamp: time.Now(), Timestamp: time.Now()}}}))
	}
	transactions.Run(cancelled, testPool)
	count, err = storage.CountMonthlyEvents(ctx, testPool)
	require.NoError(t, err)
	require.EqualValues(t, 3052, count, "transaction retries retain the existing separate-row semantics")
	var linked int
	require.NoError(t, testPool.QueryRow(ctx, `SELECT count(*) FROM spans s JOIN transactions t ON t.id=s.transaction_id WHERE s.span_id=t.transaction`).Scan(&linked))
	require.Equal(t, 2050, linked, "explicit IDs must link every span to the right input transaction")
}

func TestFailedGroupedTransactionsPublishOnlyRecoveredIDs(t *testing.T) {
	ctx := context.Background()
	_, err := testPool.Exec(ctx, "TRUNCATE transactions CASCADE")
	require.NoError(t, err)
	buffer := ingest.NewTransactionBuffer(2)
	for _, project := range []string{testProject.ID, "00000000-0000-0000-0000-000000000000"} {
		require.True(t, buffer.Push(ingest.BufferedTransaction{ProjectID: project, Transaction: "test", Status: "ok", StartTimestamp: time.Now(), Timestamp: time.Now()}))
	}
	hookCalled := false
	buffer.Hook = func(ctx context.Context, pool *pgxpool.Pool, batch []ingest.BufferedTransaction, ids []string) {
		hookCalled = true
		require.Len(t, batch, 1)
		require.Len(t, ids, 1)
		require.Equal(t, testProject.ID, batch[0].ProjectID)
		var projectID string
		require.NoError(t, pool.QueryRow(ctx, "SELECT project_id FROM transactions WHERE id=$1", ids[0]).Scan(&projectID))
		require.Equal(t, testProject.ID, projectID)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	buffer.Run(cancelled, testPool)
	require.True(t, hookCalled)
	require.EqualValues(t, 1, buffer.Stats().Persisted)
	require.EqualValues(t, 1, buffer.Stats().Dropped["invalid_record"])
	var count int
	require.NoError(t, testPool.QueryRow(ctx, "SELECT count(*) FROM transactions").Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, testPool.QueryRow(ctx, "SELECT COALESCE(sum(n),0) FROM telemetry_usage WHERE kind='transactions'").Scan(&count))
	require.Equal(t, 1, count)
}
