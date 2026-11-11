package ingest

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
)

// setupDiagnosticsTimeout bounds the best-effort diagnostics write so a failing
// database cannot stall ingest. Tests that assert the write lands raise it,
// since a slow CI runner can exceed 200ms on its own.
var setupDiagnosticsTimeout = 200 * time.Millisecond

// Diagnostics are best effort when the database itself is failing. A queued
// observation must never be promoted to a storage receipt by the application.
func recordSetupWriteFailure(ctx context.Context, pool *pgxpool.Pool, kind string, projectIDs []string, writeErr error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), setupDiagnosticsTimeout)
	defer cancel()
	recordSetupFailures(ctx, kind, projectIDs, writeErr, func(ctx context.Context, id string, observations []storage.SetupObservation) error {
		return storage.RecordSetupObservations(ctx, pool, id, observations)
	})
}

func recordSetupFailures(ctx context.Context, kind string, projectIDs []string, writeErr error, record func(context.Context, string, []storage.SetupObservation) error) {
	// A lost commit acknowledgement is not evidence that storage failed.
	var unknown *unknownCommit
	if errors.As(writeErr, &unknown) {
		return
	}
	seen := map[string]bool{}
	for _, id := range projectIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		_ = record(ctx, id, []storage.SetupObservation{{Kind: kind, Outcome: "rejected", Reason: "storage_failed", ObservedAt: time.Now().UTC()}})
		if ctx.Err() != nil {
			return
		}
	}
}
