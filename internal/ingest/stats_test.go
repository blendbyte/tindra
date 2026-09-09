package ingest_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestBufferStatsConcurrentPushes(t *testing.T) {
	b := ingest.NewBuffer(100)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				b.Push(ingest.BufferedEvent{})
			}
		}()
	}
	wg.Wait()
	s := b.Stats()
	require.Equal(t, 100, s.Queued)
	require.Equal(t, 100, s.Capacity)
	require.EqualValues(t, 100, s.Accepted)
	require.EqualValues(t, 900, s.Rejected)
	tx := ingest.NewTransactionBuffer(1)
	require.True(t, tx.Push(ingest.BufferedTransaction{}))
	require.False(t, tx.Push(ingest.BufferedTransaction{}))
	require.EqualValues(t, 1, tx.Stats().Rejected)
	logs := ingest.NewLogBuffer(1)
	require.True(t, logs.Push(ingest.BufferedLog{}))
	require.False(t, logs.Push(ingest.BufferedLog{}))
	require.EqualValues(t, 0, logs.Stats().Rejected)
	require.EqualValues(t, 1, logs.Stats().Dropped["buffer_full"])
}
