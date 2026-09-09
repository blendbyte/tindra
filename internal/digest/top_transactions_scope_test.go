package digest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTopTransactionsPreviousPeriodScope(t *testing.T) {
	truncateAll(t)
	a := seedProject(t, "top-a", "A")
	b := seedProject(t, "top-b", "B")
	excluded := seedProject(t, "top-excluded", "Excluded")
	from := time.Date(2026, 5, 8, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		seedTransaction(t, a.ID, "shared", 100, from.Add(time.Hour))
	}
	seedTransaction(t, b.ID, "shared", 200, from.Add(time.Hour))
	seedTransaction(t, a.ID, "shared", 10, from.Add(-time.Hour))
	seedTransaction(t, b.ID, "shared", 30, from.Add(-time.Hour))
	seedTransaction(t, excluded.ID, "shared", 1000, from.Add(-time.Hour))
	seedTransaction(t, a.ID, "unrelated", 9999, from.Add(-time.Hour))
	got, err := topTransactions(context.Background(), testPool, []string{a.ID, b.ID}, from, from.AddDate(0, 0, 7), 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "A", got[0].ProjectName)
	require.Equal(t, "shared", got[0].Transaction)
	// Preserve the existing previous-period grouping across selected projects.
	require.EqualValues(t, 20, got[0].P50PrevMs)
	require.EqualValues(t, 100, got[0].P50Ms)
	require.EqualValues(t, 100, got[0].P95Ms)
}
