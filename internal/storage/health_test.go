package storage_test

import (
	"context"
	"testing"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestGetInstanceHealth(t *testing.T) {
	h, err := storage.GetInstanceHealth(context.Background(), testPool)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h == nil {
		t.Fatal("expected non-nil health")
	}
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
