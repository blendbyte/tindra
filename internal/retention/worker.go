package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
)

// Worker periodically enforces age, row-count, and profile retention limits.
// Setting RetentionDays to 0 disables only the general age-based policy.
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

// rowCapBatch limits parent rows per transaction; cascaded children may exceed it.
const rowCapBatch = 5000

// maxRowCapDeletesPerPass lets a large backlog yield to the next scheduled cycle.
const maxRowCapDeletesPerPass = 200_000

// maxStorageCapIDsPerPass bounds how many ids one storage-cap pass holds in
// memory. Dropping PROFILE_STORAGE_LIMIT_MB on a large instance would otherwise
// load every over-budget id into the retention goroutine at once.
const maxStorageCapIDsPerPass = 200_000

func NewWorker(pool *pgxpool.Pool, retentionDays int) *Worker {
	return &Worker{pool: pool, retentionDays: retentionDays}
}

// WithRowLimits sets instance-wide row caps for logs and transactions.
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

// enabled reports whether any independent retention policy is configured.
func (w *Worker) enabled() bool {
	return w.retentionDays > 0 || w.logRowLimit > 0 || w.txRowLimit > 0 ||
		w.profileRetentionDays > 0 || w.profileStorageLimitMB > 0
}

// RunOnce runs a single purge cycle and reports whether a telemetry deletion
// budget was exhausted. Useful for testing and explicit maintenance passes.
func (w *Worker) RunOnce(ctx context.Context) bool {
	if !w.enabled() {
		return false
	}
	return w.purge(ctx)
}

// Run starts the retention loop. Call in a dedicated goroutine.
// Runs immediately, then hourly when caught up or after five minutes when a
// telemetry deletion budget was exhausted. Passes never overlap.
func (w *Worker) Run(ctx context.Context) {
	if !w.enabled() {
		slog.Info("retention: disabled (no retention limits configured)")
		return
	}

	runPurgeLoop(ctx, w.purge)
}

func runPurgeLoop(ctx context.Context, purge func(context.Context) bool) {
	for ctx.Err() == nil {
		delay := time.Hour
		if purge(ctx) {
			delay = 5 * time.Minute
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) purge(ctx context.Context) bool {
	var (
		eventsDeleted, issuesDeleted        int64
		txDeleted, logsDeleted              int64
		uptimeChecksDeleted, firingsDeleted int64
		logsCapDeleted, txCapDeleted        int64
		profilesDeleted, profilesCapDeleted int64
	)

	// Row caps and profile policies run independently of the general age window.
	if w.retentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -w.retentionDays)
		slog.Info("retention: purging", "cutoff", cutoff.Format(time.DateOnly))

		eventsDeleted, issuesDeleted = w.purgeEvents(ctx, cutoff)
		txDeleted = w.purgeTransactions(ctx, cutoff)
		logsDeleted = w.purgeLogs(ctx, cutoff)
		uptimeChecksDeleted = w.purgeUptimeChecks(ctx, cutoff)
		firingsDeleted = w.purgeAlertFirings(ctx)
		w.purgeExpiredAuthTokens(ctx)
	}
	logsCapDeleted = w.purgeLogsRowCap(ctx)
	txCapDeleted = w.purgeTransactionsRowCap(ctx)

	if w.profileRetentionDays > 0 {
		profileCutoff := time.Now().AddDate(0, 0, -w.profileRetentionDays)
		profilesDeleted = w.purgeProfiles(ctx, profileCutoff)
	}
	profilesCapDeleted = w.purgeProfilesStorageCap(ctx)

	budgetReached := eventsDeleted >= maxAgeDeletesPerPass || txDeleted >= maxAgeDeletesPerPass ||
		logsDeleted >= maxAgeDeletesPerPass || uptimeChecksDeleted >= maxAgeDeletesPerPass ||
		profilesDeleted >= maxAgeDeletesPerPass || firingsDeleted >= maxRowCapDeletesPerPass ||
		logsCapDeleted >= maxRowCapDeletesPerPass || txCapDeleted >= maxRowCapDeletesPerPass ||
		profilesCapDeleted >= maxStorageCapIDsPerPass
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
		"budget_reached", budgetReached,
	)
	return budgetReached
}

// purgeProfiles deletes profiles past the profile-specific retention window,
// with bounded batches and a per-pass row budget. A larger backlog continues
// on the next scheduled pass. Neither limit bounds payload bytes or elapsed time.
func (w *Worker) purgeProfiles(ctx context.Context, cutoff time.Time) int64 {
	var total int64
	for total < maxAgeDeletesPerPass && ctx.Err() == nil {
		batch := min(int64(profilePurgeBatch), int64(maxAgeDeletesPerPass)-total)
		tag, err := w.pool.Exec(ctx, `
			DELETE FROM profile_chunks
			WHERE id IN (
				SELECT id FROM profile_chunks
				WHERE received_at < $1
				ORDER BY received_at ASC
				LIMIT $2
			)`, cutoff, batch)
		if err != nil {
			slog.Error("retention: delete profiles", "err", err)
			return total
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < batch {
			return total
		}
	}
	return total
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

	// Ranked by received_at, not start_ts. start_ts comes from the client's own
	// sample timestamps, so a host with a skewed clock would decide the
	// eviction order for the whole instance: a future clock keeps its own
	// profiles permanently and pushes every other project's out of the budget.
	// received_at is ours, and the age purge above already relies on it.
	//
	// The window is evaluated once and the ids deleted in batches. Running it
	// per batch meant a full scan and sort for every 5000 rows. The limit bounds
	// how many ids one pass holds in memory; a later cycle picks up the rest,
	// and the oldest always go first.
	rows, err := w.pool.Query(ctx, `
		SELECT id FROM (
			SELECT id, SUM(size_bytes) OVER (ORDER BY received_at DESC, id DESC) AS running
			FROM profile_chunks
		) ranked
		WHERE running > $1
		ORDER BY running DESC
		LIMIT $2`, limitBytes, maxStorageCapIDsPerPass)
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
	for eventsDeleted < maxAgeDeletesPerPass && ctx.Err() == nil {
		batch := min(int64(agePurgeBatch), int64(maxAgeDeletesPerPass)-eventsDeleted)
		events, issues, err := w.purgeEventBatch(ctx, cutoff, batch)
		if err != nil {
			slog.Error("retention: purge event batch", "err", err)
			return
		}
		eventsDeleted += events
		issuesDeleted += issues
		if events < batch {
			return
		}
	}
	return
}

// Keep event deletion and issue reconciliation atomic. Recount in a separate
// statement after acquiring issue locks, so its snapshot includes grouping
// transactions that committed while we were waiting for those locks.
func (w *Worker) purgeEventBatch(ctx context.Context, cutoff time.Time, batch int64) (int64, int64, error) {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback(ctx)
	if err := storage.LockIssueMembership(ctx, tx, false); err != nil {
		return 0, 0, err
	}
	rows, err := tx.Query(ctx, `
		DELETE FROM events WHERE id = ANY(ARRAY(
			SELECT id FROM events WHERE received_at < $1
			ORDER BY received_at, id LIMIT $2
		)) RETURNING issue_id`, cutoff, batch)
	if err != nil {
		return 0, 0, err
	}
	var deleted int64
	affected := make(map[string]struct{})
	for rows.Next() {
		var id *string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		deleted++
		if id != nil {
			affected[*id] = struct{}{}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	var issuesDeleted int64
	if len(affected) > 0 {
		ids := make([]string, 0, len(affected))
		for id := range affected {
			ids = append(ids, id)
		}
		if _, err := tx.Exec(ctx, `SELECT id FROM issues WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, ids); err != nil {
			return 0, 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE issues
   SET event_count = (SELECT COUNT(*) FROM events WHERE events.issue_id = issues.id)
   WHERE id = ANY($1::uuid[])`, ids); err != nil {
			return 0, 0, err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM issues
   WHERE event_count = 0 AND status IN ('resolved', 'ignored')
   AND id = ANY($1::uuid[])`, ids)
		if err != nil {
			return 0, 0, err
		}
		issuesDeleted = tag.RowsAffected()
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, 0, err
	}
	return deleted, issuesDeleted, nil
}

// agePurgeBatch bounds parent rows, not cascaded children or elapsed time.
const agePurgeBatch = 5000
const maxAgeDeletesPerPass = 200_000

func (w *Worker) purgeTransactions(ctx context.Context, cutoff time.Time) int64 {
	return w.purgeByAge(ctx, "transactions", "received_at", cutoff)
}

func (w *Worker) purgeLogs(ctx context.Context, cutoff time.Time) int64 {
	return w.purgeByAge(ctx, "logs", "received_at", cutoff)
}

// purgeByAge uses a cutoff fixed for the whole pass. Table and time-column
// names come only from fixed wrappers. The corresponding time/ID index
// supports ordered candidate selection without scanning retained rows.
func (w *Worker) purgeByAge(ctx context.Context, table, timeColumn string, cutoff time.Time) int64 {
	query := fmt.Sprintf(`
		DELETE FROM %s WHERE id = ANY(ARRAY(
			SELECT id FROM %s WHERE %s < $1
			ORDER BY %s ASC, id ASC LIMIT $2
		))`, table, table, timeColumn, timeColumn)
	var total int64
	for total < maxAgeDeletesPerPass && ctx.Err() == nil {
		batch := min(int64(agePurgeBatch), int64(maxAgeDeletesPerPass)-total)
		tag, err := w.pool.Exec(ctx, query, cutoff, batch)
		if err != nil {
			slog.Error("retention: delete expired rows", "table", table, "err", err)
			return total
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < batch {
			break
		}
	}
	return total
}

func (w *Worker) purgeUptimeChecks(ctx context.Context, cutoff time.Time) int64 {
	return w.purgeByAge(ctx, "uptime_checks", "checked_at", cutoff)
}

// purgeAlertFirings retains the newest 1000 firings per rule. Rank once per
// pass and materialize a bounded set of IDs before deleting in transactions.
func (w *Worker) purgeAlertFirings(ctx context.Context) int64 {
	if ctx.Err() != nil {
		return 0
	}
	rows, err := w.pool.Query(ctx, `
		SELECT id FROM (
			SELECT id, fired_at, ROW_NUMBER() OVER (
				PARTITION BY rule_id ORDER BY fired_at DESC, id DESC
			) AS rn FROM alert_firings
		) ranked
		WHERE rn > 1000 ORDER BY fired_at ASC, id ASC LIMIT $1`, maxRowCapDeletesPerPass)
	if err != nil {
		slog.Error("retention: rank alert firings", "err", err)
		return 0
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		slog.Error("retention: read alert firing IDs", "err", err)
		return 0
	}
	var total int64
	for len(ids) > 0 && ctx.Err() == nil {
		n := min(rowCapBatch, len(ids))
		tag, err := w.pool.Exec(ctx, `DELETE FROM alert_firings WHERE id = ANY($1::uuid[])`, ids[:n])
		if err != nil {
			slog.Error("retention: purge alert firings", "err", err)
			return total
		}
		total += tag.RowsAffected()
		ids = ids[n:]
	}
	return total
}

// purgeLogsRowCap enforces an instance-wide row cap on the logs table.
// The globally oldest rows are deleted first, within the per-pass work budget.
func (w *Worker) purgeLogsRowCap(ctx context.Context) int64 {
	return w.purgeRowCap(ctx, "logs", "timestamp", w.logRowLimit)
}

// purgeTransactionsRowCap enforces an instance-wide row cap on transactions.
// Deletion cascades to child spans and performance events via foreign keys.
func (w *Worker) purgeTransactionsRowCap(ctx context.Context) int64 {
	return w.purgeRowCap(ctx, "transactions", "start_timestamp", w.txRowLimit)
}

// purgeRowCap finds the newest excess row once, using the global time/ID index.
// Keeping this boundary fixed avoids repeated counts and prevents concurrent
// cleanup workers from independently subtracting the same excess row count.
// Table and column names come only from the fixed wrappers above.
func (w *Worker) purgeRowCap(ctx context.Context, table, timeColumn string, limit int) int64 {
	if limit <= 0 || ctx.Err() != nil {
		return 0
	}
	var cutoff time.Time
	var cutoffID string
	err := w.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s, id FROM %s ORDER BY %s DESC, id DESC OFFSET $1 LIMIT 1`,
		timeColumn, table, timeColumn), limit).Scan(&cutoff, &cutoffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		slog.Error("retention: find row cap boundary", "table", table, "err", err)
		return 0
	}
	query := fmt.Sprintf(`
		DELETE FROM %s WHERE id IN (
			SELECT id FROM %s WHERE (%s, id) <= ($1, $2::uuid)
			ORDER BY %s ASC, id ASC LIMIT $3
		)`, table, table, timeColumn, timeColumn)
	var total int64
	for total < maxRowCapDeletesPerPass && ctx.Err() == nil {
		batch := min(int64(rowCapBatch), int64(maxRowCapDeletesPerPass)-total)
		tag, err := w.pool.Exec(ctx, query, cutoff, cutoffID, batch)
		if err != nil {
			slog.Error("retention: purge row cap", "table", table, "err", err)
			return total
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < batch {
			break
		}
	}
	return total
}

// purgeExpiredAuthTokens removes short-lived tokens that are past their expiry.
// These are cleaned up here as a convenience - they're harmless if they linger,
// but keeping the tables tidy avoids unbounded growth.
func (w *Worker) purgeExpiredAuthTokens(ctx context.Context) {
	cutoff := time.Now()
	for _, table := range []string{"oauth_states", "mfa_challenges"} {
		if ctx.Err() != nil {
			return
		}
		w.purgeExpiredTokenTable(ctx, table, cutoff)
	}
}

// Select once rather than repeatedly scanning retained tokens for each batch.
// Table names come only from purgeExpiredAuthTokens. These small auth tables
// need no additional index just to bound deletion work.
func (w *Worker) purgeExpiredTokenTable(ctx context.Context, table string, cutoff time.Time) {
	rows, err := w.pool.Query(ctx, "SELECT token_hash FROM "+table+" WHERE expires_at < $1 LIMIT $2", cutoff, maxAgeDeletesPerPass)
	if err != nil {
		slog.Error("retention: select expired tokens", "table", table, "err", err)
		return
	}
	hashes, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		slog.Error("retention: read expired token hashes", "table", table, "err", err)
		return
	}
	for len(hashes) > 0 && ctx.Err() == nil {
		n := min(agePurgeBatch, len(hashes))
		// Recheck expiry in case an existing token was extended after selection.
		_, err := w.pool.Exec(ctx, "DELETE FROM "+table+" WHERE token_hash = ANY($1::text[]) AND expires_at < $2", hashes[:n], cutoff)
		if err != nil {
			slog.Error("retention: delete expired tokens", "table", table, "err", err)
			return
		}
		hashes = hashes[n:]
	}
}
