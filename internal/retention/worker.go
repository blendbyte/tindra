package retention

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Worker periodically deletes data older than RetentionDays.
// Set RetentionDays to 0 to disable (data is kept forever).
type Worker struct {
	pool          *pgxpool.Pool
	retentionDays int
}

func NewWorker(pool *pgxpool.Pool, retentionDays int) *Worker {
	return &Worker{pool: pool, retentionDays: retentionDays}
}

// Run starts the retention loop. Call in a dedicated goroutine.
// Runs one purge immediately on startup, then every hour.
func (w *Worker) Run(ctx context.Context) {
	if w.retentionDays <= 0 {
		slog.Info("retention: disabled (RETENTION_DAYS=0)")
		return
	}

	w.purge(ctx)

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.purge(ctx)
		}
	}
}

func (w *Worker) purge(ctx context.Context) {
	cutoff := time.Now().AddDate(0, 0, -w.retentionDays)
	slog.Info("retention: purging", "cutoff", cutoff.Format(time.DateOnly))

	eventsDeleted, issuesDeleted := w.purgeEvents(ctx, cutoff)
	txDeleted := w.purgeTransactions(ctx, cutoff)
	logsDeleted := w.purgeLogs(ctx, cutoff)
	w.purgeExpiredAuthTokens(ctx)

	slog.Info("retention: done",
		"events", eventsDeleted,
		"issues_removed", issuesDeleted,
		"transactions", txDeleted,
		"logs", logsDeleted,
	)
}

func (w *Worker) purgeEvents(ctx context.Context, cutoff time.Time) (eventsDeleted, issuesDeleted int64) {
	// Step 1: delete old events, collect the issue IDs that were affected.
	rows, err := w.pool.Query(ctx, `
		DELETE FROM events WHERE received_at < $1 RETURNING issue_id
	`, cutoff)
	if err != nil {
		slog.Error("retention: delete events", "err", err)
		return
	}

	issueIDs := make(map[string]struct{})
	for rows.Next() {
		var id *string
		if err := rows.Scan(&id); err == nil && id != nil {
			issueIDs[*id] = struct{}{}
			eventsDeleted++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		slog.Error("retention: scan deleted event rows", "err", err)
	}

	if len(issueIDs) == 0 {
		return
	}

	// Step 2: recalculate event_count for affected issues from remaining events.
	ids := make([]string, 0, len(issueIDs))
	for id := range issueIDs {
		ids = append(ids, id)
	}
	if _, err := w.pool.Exec(ctx, `
		UPDATE issues
		SET event_count = (SELECT COUNT(*) FROM events WHERE events.issue_id = issues.id)
		WHERE id = ANY($1::uuid[])
	`, ids); err != nil {
		slog.Error("retention: update issue counts", "err", err)
	}

	// Step 3: delete resolved/ignored issues that now have no remaining events.
	// Open and regressed issues are kept as shells so users retain a record
	// that the problem existed even after their events age out.
	tag, err := w.pool.Exec(ctx, `
		DELETE FROM issues
		WHERE event_count = 0
		  AND status IN ('resolved', 'ignored')
		  AND id = ANY($1::uuid[])
	`, ids)
	if err != nil {
		slog.Error("retention: delete empty issues", "err", err)
	} else {
		issuesDeleted = tag.RowsAffected()
	}
	return
}

func (w *Worker) purgeTransactions(ctx context.Context, cutoff time.Time) int64 {
	tag, err := w.pool.Exec(ctx, `DELETE FROM transactions WHERE received_at < $1`, cutoff)
	if err != nil {
		slog.Error("retention: delete transactions", "err", err)
		return 0
	}
	return tag.RowsAffected()
}

func (w *Worker) purgeLogs(ctx context.Context, cutoff time.Time) int64 {
	tag, err := w.pool.Exec(ctx, `DELETE FROM logs WHERE received_at < $1`, cutoff)
	if err != nil {
		slog.Error("retention: delete logs", "err", err)
		return 0
	}
	return tag.RowsAffected()
}

// purgeExpiredAuthTokens removes short-lived tokens that are past their expiry.
// These are cleaned up here as a convenience - they're harmless if they linger,
// but keeping the tables tidy avoids unbounded growth.
func (w *Worker) purgeExpiredAuthTokens(ctx context.Context) {
	_, _ = w.pool.Exec(ctx, `DELETE FROM oauth_states WHERE expires_at < NOW()`)
	_, _ = w.pool.Exec(ctx, `DELETE FROM mfa_challenges WHERE expires_at < NOW()`)
}
