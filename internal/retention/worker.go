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
	logRowLimit   int
	txRowLimit    int

	profileRetentionDays  int
	profileStorageLimitMB int
}

// profilePurgeBatch caps how many profile rows one DELETE removes. Profiles
// are the only TOASTed payload in the schema, so purging a day of them in a
// single statement means a long lock and a large WAL burst. Looping in batches
// keeps both bounded.
const profilePurgeBatch = 5000

func NewWorker(pool *pgxpool.Pool, retentionDays int) *Worker {
	return &Worker{pool: pool, retentionDays: retentionDays}
}

// WithRowLimits sets per-project row caps for logs and transactions.
// Zero means no cap. Returns the worker for chaining.
func (w *Worker) WithRowLimits(logRowLimit, txRowLimit int) *Worker {
	w.logRowLimit = logRowLimit
	w.txRowLimit = txRowLimit
	return w
}

// WithProfileLimits sets the profile-specific retention window and the
// instance-wide storage budget in megabytes. Zero disables either.
//
// Profiles get their own knobs rather than following RetentionDays because
// they are orders of magnitude larger per unit of time than anything else
// stored: a handful of processes profiling continuously would run to tens of
// gigabytes at the default 90-day window. Returns the worker for chaining.
func (w *Worker) WithProfileLimits(retentionDays, storageLimitMB int) *Worker {
	w.profileRetentionDays = retentionDays
	w.profileStorageLimitMB = storageLimitMB
	return w
}

// enabled reports whether any purge is configured. The profile limits stand on
// their own: an instance that keeps everything forever still needs a ceiling on
// profile storage, or a single misconfigured SDK fills the disk.
func (w *Worker) enabled() bool {
	return w.retentionDays > 0 || w.profileRetentionDays > 0 || w.profileStorageLimitMB > 0
}

// RunOnce runs a single purge cycle and returns. Useful for testing.
func (w *Worker) RunOnce(ctx context.Context) {
	if !w.enabled() {
		return
	}
	w.purge(ctx)
}

// Run starts the retention loop. Call in a dedicated goroutine.
// Runs one purge immediately on startup, then every hour.
func (w *Worker) Run(ctx context.Context) {
	if !w.enabled() {
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
	var (
		eventsDeleted, issuesDeleted        int64
		txDeleted, logsDeleted              int64
		uptimeChecksDeleted, firingsDeleted int64
		logsCapDeleted, txCapDeleted        int64
		profilesDeleted, profilesCapDeleted int64
	)

	// The general retention window drives everything except the profile
	// purges, which are configured independently below.
	if w.retentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -w.retentionDays)
		slog.Info("retention: purging", "cutoff", cutoff.Format(time.DateOnly))

		eventsDeleted, issuesDeleted = w.purgeEvents(ctx, cutoff)
		txDeleted = w.purgeTransactions(ctx, cutoff)
		logsDeleted = w.purgeLogs(ctx, cutoff)
		uptimeChecksDeleted = w.purgeUptimeChecks(ctx, cutoff)
		firingsDeleted = w.purgeAlertFirings(ctx)
		w.purgeExpiredAuthTokens(ctx)

		logsCapDeleted = w.purgeLogsRowCap(ctx)
		txCapDeleted = w.purgeTransactionsRowCap(ctx)
	}

	if w.profileRetentionDays > 0 {
		profileCutoff := time.Now().AddDate(0, 0, -w.profileRetentionDays)
		profilesDeleted = w.purgeProfiles(ctx, profileCutoff)
	}
	profilesCapDeleted = w.purgeProfilesStorageCap(ctx)

	slog.Info("retention: done",
		"events", eventsDeleted,
		"issues_removed", issuesDeleted,
		"transactions", txDeleted,
		"logs", logsDeleted,
		"uptime_checks", uptimeChecksDeleted,
		"alert_firings", firingsDeleted,
		"logs_row_cap", logsCapDeleted,
		"tx_row_cap", txCapDeleted,
		"profiles", profilesDeleted,
		"profiles_storage_cap", profilesCapDeleted,
	)
}

// purgeProfiles deletes profiles past the profile-specific retention window,
// in batches so a large backlog does not lock the table or spike WAL.
func (w *Worker) purgeProfiles(ctx context.Context, cutoff time.Time) int64 {
	var total int64
	for {
		tag, err := w.pool.Exec(ctx, `
			DELETE FROM profile_chunks
			WHERE id IN (
				SELECT id FROM profile_chunks
				WHERE received_at < $1
				ORDER BY received_at ASC
				LIMIT $2
			)`, cutoff, profilePurgeBatch)
		if err != nil {
			slog.Error("retention: delete profiles", "err", err)
			return total
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < profilePurgeBatch {
			return total
		}
	}
}

// purgeProfilesStorageCap enforces the instance-wide profile storage budget,
// deleting oldest first until the total compressed size fits.
//
// Profiles need a byte budget rather than the row cap used for logs and
// transactions: those rows are uniformly small, while a profile ranges over
// orders of magnitude depending on sample rate, thread count and stack depth,
// so a row count says nothing about disk. size_bytes is denormalized onto the
// row precisely so this can be a plain sum.
func (w *Worker) purgeProfilesStorageCap(ctx context.Context) int64 {
	if w.profileStorageLimitMB <= 0 {
		return 0
	}
	limitBytes := int64(w.profileStorageLimitMB) * 1024 * 1024

	// The running total is taken newest first, so every row past the budget is
	// one to drop. The window is evaluated once and the ids are then deleted in
	// batches: running it per batch meant a full scan and sort of the table for
	// every 5000 rows, which on a large overage is a scan per batch rather than
	// one for the whole pass.
	rows, err := w.pool.Query(ctx, `
		SELECT id FROM (
			SELECT id, SUM(size_bytes) OVER (ORDER BY start_ts DESC, id DESC) AS running
			FROM profile_chunks
		) ranked
		WHERE running > $1
		ORDER BY running DESC`, limitBytes)
	if err != nil {
		slog.Error("retention: rank profiles for storage cap", "err", err)
		return 0
	}
	var doomed []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			slog.Error("retention: scan profile id", "err", err)
			break
		}
		doomed = append(doomed, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		slog.Error("retention: read profile ids", "err", err)
		return 0
	}

	var total int64
	for len(doomed) > 0 {
		n := min(profilePurgeBatch, len(doomed))
		tag, err := w.pool.Exec(ctx,
			`DELETE FROM profile_chunks WHERE id = ANY($1::uuid[])`, doomed[:n])
		if err != nil {
			slog.Error("retention: purge profile storage cap", "err", err)
			return total
		}
		total += tag.RowsAffected()
		doomed = doomed[n:]
	}
	return total
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
		if err := rows.Scan(&id); err == nil {
			eventsDeleted++
			if id != nil {
				issueIDs[*id] = struct{}{}
			}
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

func (w *Worker) purgeUptimeChecks(ctx context.Context, cutoff time.Time) int64 {
	tag, err := w.pool.Exec(ctx, `DELETE FROM uptime_checks WHERE checked_at < $1`, cutoff)
	if err != nil {
		slog.Error("retention: delete uptime checks", "err", err)
		return 0
	}
	return tag.RowsAffected()
}

// purgeAlertFirings keeps only the 1000 most recent firings per alert rule.
func (w *Worker) purgeAlertFirings(ctx context.Context) int64 {
	tag, err := w.pool.Exec(ctx, `
		DELETE FROM alert_firings
		WHERE id IN (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY rule_id ORDER BY fired_at DESC) AS rn
				FROM alert_firings
			) ranked
			WHERE rn > 1000
		)`)
	if err != nil {
		slog.Error("retention: purge alert firings", "err", err)
		return 0
	}
	return tag.RowsAffected()
}

// purgeLogsRowCap enforces an instance-wide row cap on the logs table.
// The globally oldest rows are deleted first until the total is at or below logRowLimit.
func (w *Worker) purgeLogsRowCap(ctx context.Context) int64 {
	if w.logRowLimit <= 0 {
		return 0
	}
	tag, err := w.pool.Exec(ctx, `
		DELETE FROM logs
		WHERE id IN (
			SELECT id FROM logs
			ORDER BY timestamp ASC
			LIMIT GREATEST(0, (SELECT COUNT(*) FROM logs) - $1)
		)`, w.logRowLimit)
	if err != nil {
		slog.Error("retention: purge logs row cap", "err", err)
		return 0
	}
	return tag.RowsAffected()
}

// purgeTransactionsRowCap enforces an instance-wide row cap on the transactions table.
// The globally oldest rows are deleted first; deletion cascades to child spans via FK.
func (w *Worker) purgeTransactionsRowCap(ctx context.Context) int64 {
	if w.txRowLimit <= 0 {
		return 0
	}
	tag, err := w.pool.Exec(ctx, `
		DELETE FROM transactions
		WHERE id IN (
			SELECT id FROM transactions
			ORDER BY start_timestamp ASC
			LIMIT GREATEST(0, (SELECT COUNT(*) FROM transactions) - $1)
		)`, w.txRowLimit)
	if err != nil {
		slog.Error("retention: purge transactions row cap", "err", err)
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
