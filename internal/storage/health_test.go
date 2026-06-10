package storage_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestGetInstanceHealth(t *testing.T) {
	h, err := storage.GetInstanceHealth(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	require.NotNil(t, h)
	if h.DBSizeBytes <= 0 {
		t.Errorf("DBSizeBytes: got %d, want > 0", h.DBSizeBytes)
	}
}

func TestGetInstanceHealth_countsAreNonNegative(t *testing.T) {
	h, err := storage.GetInstanceHealth(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.EventsTotal < 0 {
		t.Errorf("EventsTotal: got %d, want >= 0", h.EventsTotal)
	}
	if h.TxTotal < 0 {
		t.Errorf("TxTotal: got %d, want >= 0", h.TxTotal)
	}
	if h.LogsTotal < 0 {
		t.Errorf("LogsTotal: got %d, want >= 0", h.LogsTotal)
	}
	if h.Events24h < 0 {
		t.Errorf("Events24h: got %d, want >= 0", h.Events24h)
	}
	if h.Tx24h < 0 {
		t.Errorf("Tx24h: got %d, want >= 0", h.Tx24h)
	}
	if h.Logs24h < 0 {
		t.Errorf("Logs24h: got %d, want >= 0", h.Logs24h)
	}
}

func TestGetInstanceHealth_tableSizesArePositive(t *testing.T) {
	h, err := storage.GetInstanceHealth(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// pg_total_relation_size always returns >= 0 for existing tables;
	// even an empty table has metadata pages so the value should be > 0.
	if h.EventsSizeBytes <= 0 {
		t.Errorf("EventsSizeBytes: got %d, want > 0", h.EventsSizeBytes)
	}
	if h.TxSizeBytes <= 0 {
		t.Errorf("TxSizeBytes: got %d, want > 0", h.TxSizeBytes)
	}
	if h.LogsSizeBytes <= 0 {
		t.Errorf("LogsSizeBytes: got %d, want > 0", h.LogsSizeBytes)
	}
}

func TestGetInstanceHealth_tableSizesAreSmallerThanDB(t *testing.T) {
	h, err := storage.GetInstanceHealth(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, tc := range []struct {
		name      string
		tableSize int64
	}{
		{"events", h.EventsSizeBytes},
		{"transactions", h.TxSizeBytes},
		{"logs", h.LogsSizeBytes},
	} {
		if tc.tableSize > h.DBSizeBytes {
			t.Errorf("%s size (%d) exceeds total DB size (%d)", tc.name, tc.tableSize, h.DBSizeBytes)
		}
	}
}
