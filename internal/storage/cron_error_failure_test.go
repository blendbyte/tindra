package storage_test

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestRecentCronErrorsQueryFailure(t *testing.T) {
	pool, err := pgxpool.NewWithConfig(t.Context(), testPool.Config())
	require.NoError(t, err)
	pool.Close()
	monitors, err := storage.ListMonitorsWithRecentErrors(t.Context(), pool, nil, time.Now())
	require.ErrorContains(t, err, "query")
	require.Nil(t, monitors)
}
