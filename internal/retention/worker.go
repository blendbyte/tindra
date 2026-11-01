package retention

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
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

	// now measures table budgets and statement durations. Cutoffs still use
	// wall-clock time. Tests swap it for a clock they advance by hand.
	now         func() time.Time
	tableBudget time.Duration
	// batchSizes remembers each table's adapted batch size across passes, so a
	// table with heavy cascades does not start every pass with a slow statement.
	// Passes never overlap, so the map needs no lock.
	batchSizes map[string]int64
}

// tableBudget bounds how long one pass spends on a single table. The budget is
// per table, so a large backlog in one table cannot starve the others: every
// table gets its turn in every pass. One minute of 5000-row batches removes
// millions of rows, against the fixed 200k a pass used to allow, which capped
// age retention at about 2.4M rows an hour per table and let a busy install
// grow forever.
const tableBudget = time.Minute

// idleDelay is the wait between passes when every table is caught up.
// backlogDelay is the wait when a table ran out of budget with rows still to
// delete. It is short so a backlog keeps draining at most of the budget's
// rate, yet still leaves the database a quiet gap between passes.
const (
	idleDelay    = time.Hour
	backlogDelay = 30 * time.Second
)

// batchTarget is the statement duration batch sizes adapt toward. A batch
// that takes longer is scaled down in proportion, one that finishes in under
// half of it doubles, up to the table's maximum. Statements, and the locks and
// WAL bursts that come with them, stay around a second however many child rows
// a parent row cascades to on this install.
const batchTarget = time.Second

// minBatch keeps a pathologically slow table making progress.
const minBatch = 100

// agePurgeBatch is the largest batch for tables whose rows delete on their
// own (logs, uptime checks, cron check-ins) or with a few children (events
// and their tags). It also caps profile deletes: profiles are the only
// TOASTed payload in the schema, so a single statement for a day of them means
// a long lock and a large WAL burst.
const agePurgeBatch = 5000

// txPurgeBatch is the largest batch for transactions. Each one cascades to its
// spans and performance events, about 40 spans per transaction on real
// installs, so 1000 transactions already delete some 40k rows in a statement.
const txPurgeBatch = 1000

// maxIDsPerPass bounds how many ids a select-then-delete pass holds in memory
// (alert firings, the profile storage cap, expired auth tokens). Dropping
// PROFILE_STORAGE_LIMIT_MB on a large instance would otherwise load every
// over-budget id into the retention goroutine at once.
const maxIDsPerPass = 200_000

func NewWorker(pool *pgxpool.Pool, retentionDays int) *Worker {
	return &Worker{
		pool:          pool,
		retentionDays: retentionDays,
		now:           time.Now,
		tableBudget:   tableBudget,
		batchSizes:    make(map[string]int64),
	}
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

// RunOnce runs a single purge cycle and reports whether a table ran out of its
// time budget with rows still to delete. Useful for testing and explicit
// maintenance passes.
func (w *Worker) RunOnce(ctx context.Context) bool {
	return w.purge(ctx)
}

// Run starts the retention loop. Call in a dedicated goroutine.
// Runs immediately, then hourly when caught up or after backlogDelay when a
// table is still behind. Passes never overlap. It runs even without retention
// limits, because housekeeping (expired sessions and tokens, the per-rule
// alert firing cap) is needed regardless.
func (w *Worker) Run(ctx context.Context) {
	if !w.enabled() {
		slog.Info("retention: no retention limits configured; only housekeeping runs")
	}
	runPurgeLoop(ctx, w.purge)
}

func runPurgeLoop(ctx context.Context, purge func(context.Context) bool) {
	for ctx.Err() == nil {
		delay := idleDelay
		if purge(ctx) {
			delay = backlogDelay
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
		cronCheckinsDeleted                 int64
		behind                              bool
	)
	track := func(n int64, tableBehind bool) int64 {
		behind = behind || tableBehind
		return n
	}

	// Housekeeping does not depend on any retention setting.
	firingsDeleted = w.purgeAlertFirings(ctx)
	w.purgeExpiredAuthTokens(ctx)

	// Row caps and profile policies run independently of the general age window.
	if w.retentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -w.retentionDays)
		slog.Info("retention: purging", "cutoff", cutoff.Format(time.DateOnly))

		var eventsBehind bool
		eventsDeleted, issuesDeleted, eventsBehind = w.purgeEvents(ctx, cutoff)
		behind = behind || eventsBehind
		txDeleted = track(w.purgeTransactions(ctx, cutoff))
		logsDeleted = track(w.purgeLogs(ctx, cutoff))
		uptimeChecksDeleted = track(w.purgeUptimeChecks(ctx, cutoff))
		cronCheckinsDeleted = track(w.purgeCronCheckins(ctx, cutoff))
	}
	logsCapDeleted = track(w.purgeLogsRowCap(ctx))
	txCapDeleted = track(w.purgeTransactionsRowCap(ctx))

	if w.profileRetentionDays > 0 {
		profileCutoff := time.Now().AddDate(0, 0, -w.profileRetentionDays)
		profilesDeleted = track(w.purgeProfiles(ctx, profileCutoff))
	}
	profilesCapDeleted = w.purgeProfilesStorageCap(ctx)

	// The id-list purges hold at most maxIDsPerPass ids; reaching it means more
	// are probably waiting.
	behind = behind || firingsDeleted >= maxIDsPerPass || profilesCapDeleted >= maxIDsPerPass
	slog.Info("retention: done",
		"events", eventsDeleted,
		"issues_removed", issuesDeleted,
		"transactions", txDeleted,
		"logs", logsDeleted,
		"uptime_checks", uptimeChecksDeleted,
		"cron_checkins", cronCheckinsDeleted,
		"alert_firings", firingsDeleted,
		"logs_row_cap", logsCapDeleted,
		"tx_row_cap", txCapDeleted,
		"profiles", profilesDeleted,
		"profiles_storage_cap", profilesCapDeleted,
		"behind", behind,
	)
	return behind
}

// drain deletes one table's rows in batches until a batch comes back short,
// meaning the table has caught up, the table's time budget is spent, or ctx is
// cancelled. del removes at most batch rows in one bounded statement or
// transaction and reports how many it removed. The first batch always runs, so
// every table makes progress in every pass. behind reports that the budget ran
// out after a full batch, so rows are probably still waiting.
func (w *Worker) drain(ctx context.Context, table string, maxBatch int64, del func(batch int64) (int64, error)) (total int64, behind bool, err error) {
	start := w.now()
	for ctx.Err() == nil {
		if w.now().Sub(start) >= w.tableBudget {
			return total, true, nil
		}
		batch := w.batchSize(table, maxBatch)
		began := w.now()
		n, err := del(batch)
		if err != nil {
			return total, false, err
		}
		total += n
		w.adapt(table, maxBatch, batch, w.now().Sub(began))
		if n < batch {
			return total, false, nil
		}
	}
	return total, false, nil
}

// drainStatement drains a table with a single DELETE per batch. The batch size
// is passed as the query's last argument.
func (w *Worker) drainStatement(ctx context.Context, table string, maxBatch int64, msg, query string, args ...any) (int64, bool) {
	total, behind, err := w.drain(ctx, table, maxBatch, func(batch int64) (int64, error) {
		tag, err := w.pool.Exec(ctx, query, append(args[:len(args):len(args)], batch)...)
		return tag.RowsAffected(), err
	})
	if err != nil {
		slog.Error(msg, "table", table, "err", err)
	}
	return total, behind
}

func (w *Worker) batchSize(table string, maxBatch int64) int64 {
	if n, ok := w.batchSizes[table]; ok && n <= maxBatch {
		return n
	}
	return maxBatch
}

// adapt sizes the table's next batch from how long this one took, steering
// statement duration toward batchTarget.
func (w *Worker) adapt(table string, maxBatch, batch int64, took time.Duration) {
	switch {
	case took > batchTarget:
		batch = max(minBatch, int64(float64(batch)*float64(batchTarget)/float64(took)))
	case took < batchTarget/2:
		batch = min(maxBatch, batch*2)
	}
	w.batchSizes[table] = batch
}

// purgeProfiles deletes profiles past the profile-specific retention window,
// oldest first, within the table's time budget. A larger backlog continues on
// the next pass.
func (w *Worker) purgeProfiles(ctx context.Context, cutoff time.Time) (int64, bool) {
	return w.drainStatement(ctx, "profile_chunks", agePurgeBatch, "retention: delete profiles", `
		DELETE FROM profile_chunks
		WHERE id IN (
			SELECT id FROM profile_chunks
			WHERE received_at < $1
			ORDER BY received_at ASC
			LIMIT $2
		)`, cutoff)
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
		LIMIT $2`, limitBytes, maxIDsPerPass)
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
		n := min(agePurgeBatch, len(doomed))
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

func (w *Worker) purgeEvents(ctx context.Context, cutoff time.Time) (eventsDeleted, issuesDeleted int64, behind bool) {
	var err error
	eventsDeleted, behind, err = w.drain(ctx, "events", agePurgeBatch, func(batch int64) (int64, error) {
		events, issues, err := w.purgeEventBatch(ctx, cutoff, batch)
		issuesDeleted += issues
		return events, err
	})
	if err != nil {
		slog.Error("retention: purge event batch", "err", err)
	}
	return eventsDeleted, issuesDeleted, behind
}

// Keep event deletion and issue reconciliation atomic. Each affected issue's
// event_count drops by the rows this batch removed from it, which costs the
// same however large the issue is. A full recount took seconds per batch on an
// issue with millions of events, all while holding the row lock the grouper's
// issue update waits on. The decrement runs in a separate statement after the
// issue locks are taken, so it starts from the count left by grouping
// transactions that committed while we waited, and the existence probes see
// their events.
//
// Counts can drift, so whether an issue is empty is decided by probing
// events_issue_timestamp rather than by trusting event_count. An empty issue's
// count is reset to 0, and a resolved or ignored one is deleted.
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
	perIssue := make(map[string]int32)
	for rows.Next() {
		var id *string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		deleted++
		if id != nil {
			perIssue[*id]++
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	var issuesDeleted int64
	if len(perIssue) > 0 {
		ids := make([]string, 0, len(perIssue))
		for id := range perIssue {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		counts := make([]int32, len(ids))
		for i, id := range ids {
			counts[i] = perIssue[id]
		}
		// Lock in id order so concurrent lockers cannot deadlock with us.
		if _, err := tx.Exec(ctx, `SELECT id FROM issues WHERE id = ANY($1::uuid[]) ORDER BY id FOR UPDATE`, ids); err != nil {
			return 0, 0, err
		}
		if _, err := tx.Exec(ctx, `UPDATE issues
   SET event_count = CASE
       WHEN EXISTS (SELECT 1 FROM events WHERE events.issue_id = issues.id)
       THEN GREATEST(issues.event_count - d.deleted, 0)
       ELSE 0 END
   FROM unnest($1::uuid[], $2::int[]) AS d(id, deleted)
   WHERE issues.id = d.id`, ids, counts); err != nil {
			return 0, 0, err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM issues
   WHERE id = ANY($1::uuid[]) AND status IN ('resolved', 'ignored')
   AND NOT EXISTS (SELECT 1 FROM events WHERE events.issue_id = issues.id)`, ids)
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

func (w *Worker) purgeTransactions(ctx context.Context, cutoff time.Time) (int64, bool) {
	return w.purgeByAge(ctx, "transactions", "received_at", txPurgeBatch, cutoff)
}

func (w *Worker) purgeLogs(ctx context.Context, cutoff time.Time) (int64, bool) {
	return w.purgeByAge(ctx, "logs", "received_at", agePurgeBatch, cutoff)
}

// purgeByAge uses a cutoff fixed for the whole pass. Table and time-column
// names come only from fixed wrappers. The corresponding time/ID index
// supports ordered candidate selection without scanning retained rows.
func (w *Worker) purgeByAge(ctx context.Context, table, timeColumn string, maxBatch int64, cutoff time.Time) (int64, bool) {
	query := fmt.Sprintf(`
		DELETE FROM %s WHERE id = ANY(ARRAY(
			SELECT id FROM %s WHERE %s < $1
			ORDER BY %s ASC, id ASC LIMIT $2
		))`, table, table, timeColumn, timeColumn)
	return w.drainStatement(ctx, table, maxBatch, "retention: delete expired rows", query, cutoff)
}

func (w *Worker) purgeUptimeChecks(ctx context.Context, cutoff time.Time) (int64, bool) {
	return w.purgeByAge(ctx, "uptime_checks", "checked_at", agePurgeBatch, cutoff)
}

// Retain running jobs regardless of age. Completion time keeps recently finished
// long-running jobs available to alerts; legacy terminal pings use receipt time.
func (w *Worker) purgeCronCheckins(ctx context.Context, cutoff time.Time) (int64, bool) {
	return w.drainStatement(ctx, "cron_checkins", agePurgeBatch, "retention: delete completed cron check-ins", `
		DELETE FROM cron_checkins WHERE id = ANY(ARRAY(
			SELECT id FROM cron_checkins
			WHERE status IN ('ok', 'error') AND COALESCE(finished_at, received_at) < $1
			ORDER BY COALESCE(finished_at, received_at), id LIMIT $2
		))`, cutoff)
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
		WHERE rn > 1000 ORDER BY fired_at ASC, id ASC LIMIT $1`, maxIDsPerPass)
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
		n := min(agePurgeBatch, len(ids))
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
// The globally oldest rows are deleted first, within the table's time budget.
func (w *Worker) purgeLogsRowCap(ctx context.Context) (int64, bool) {
	return w.purgeRowCap(ctx, "logs", "timestamp", agePurgeBatch, w.logRowLimit)
}

// purgeTransactionsRowCap enforces an instance-wide row cap on transactions.
// Deletion cascades to child spans and performance events via foreign keys.
func (w *Worker) purgeTransactionsRowCap(ctx context.Context) (int64, bool) {
	return w.purgeRowCap(ctx, "transactions", "start_timestamp", txPurgeBatch, w.txRowLimit)
}

// purgeRowCap finds the newest excess row once, using the global time/ID index.
// Keeping this boundary fixed avoids repeated counts and prevents concurrent
// cleanup workers from independently subtracting the same excess row count.
// Table and column names come only from the fixed wrappers above.
func (w *Worker) purgeRowCap(ctx context.Context, table, timeColumn string, maxBatch int64, limit int) (int64, bool) {
	if limit <= 0 || ctx.Err() != nil {
		return 0, false
	}
	var cutoff time.Time
	var cutoffID string
	err := w.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s, id FROM %s ORDER BY %s DESC, id DESC OFFSET $1 LIMIT 1`,
		timeColumn, table, timeColumn), limit).Scan(&cutoff, &cutoffID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false
	}
	if err != nil {
		slog.Error("retention: find row cap boundary", "table", table, "err", err)
		return 0, false
	}
	query := fmt.Sprintf(`
		DELETE FROM %s WHERE id IN (
			SELECT id FROM %s WHERE (%s, id) <= ($1, $2::uuid)
			ORDER BY %s ASC, id ASC LIMIT $3
		)`, table, table, timeColumn, timeColumn)
	return w.drainStatement(ctx, table, maxBatch, "retention: purge row cap", query, cutoff, cutoffID)
}

// expiredInviteGrace keeps expired invites listed for a while, so admins can
// see that an invite lapsed and send a new one.
const expiredInviteGrace = 30 * 24 * time.Hour

// purgeExpiredAuthTokens removes expired sessions and one-time tokens. They no
// longer authenticate anything, but without cleanup the tables grow forever.
func (w *Worker) purgeExpiredAuthTokens(ctx context.Context) {
	now := time.Now()
	for _, t := range []struct {
		table  string
		cutoff time.Time
	}{
		{"oauth_states", now},
		{"mfa_challenges", now},
		{"sessions", now},
		{"password_reset_tokens", now},
		{"user_invites", now.Add(-expiredInviteGrace)},
	} {
		if ctx.Err() != nil {
			return
		}
		w.purgeExpiredTokenTable(ctx, t.table, t.cutoff)
	}
}

// Select once rather than repeatedly scanning retained tokens for each batch.
// Table names come only from purgeExpiredAuthTokens. These small auth tables
// need no additional index just to bound deletion work.
func (w *Worker) purgeExpiredTokenTable(ctx context.Context, table string, cutoff time.Time) {
	rows, err := w.pool.Query(ctx, "SELECT token_hash FROM "+table+" WHERE expires_at < $1 LIMIT $2", cutoff, maxIDsPerPass)
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
