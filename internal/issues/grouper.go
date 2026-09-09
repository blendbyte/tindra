package issues

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
)

// Grouper polls the events table for ungrouped rows and assigns them to issues.
type Grouper struct {
	pool *pgxpool.Pool
}

func NewGrouper(pool *pgxpool.Pool) *Grouper {
	return &Grouper{pool: pool}
}

const (
	grouperPageSize  = 200
	grouperPassLimit = 2000
	grouperWorkTime  = 2 * time.Second
)

type GroupingStats struct {
	Observed, Grouped, Failed, Batches int
	Duration, OldestAge                time.Duration
	BudgetReached                      bool
	next                               *groupingCursor
}

type groupingCursor struct{ after, through *rawEvent }

type rawEvent struct {
	ID, ProjectID         string
	Timestamp, ReceivedAt time.Time
	Payload               json.RawMessage
}

// Run drains backlog in bounded passes, resting longer when idle or failing.
func (g *Grouper) Run(ctx context.Context) {
	var cursor *groupingCursor
	for ctx.Err() == nil {
		stats := g.runPass(ctx, cursor)
		cursor = stats.next
		if stats.Observed > 0 || stats.Failed > 0 {
			slog.Debug("grouper pass", "observed", stats.Observed, "grouped", stats.Grouped,
				"failed", stats.Failed, "batches", stats.Batches, "duration_ms", stats.Duration.Milliseconds(),
				"oldest_age_ms", stats.OldestAge.Milliseconds(), "budget_reached", stats.BudgetReached)
		}
		delay := 500 * time.Millisecond
		if stats.BudgetReached && stats.Failed == 0 {
			delay = 50 * time.Millisecond
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

// RunOnce bounds rows, scheduling time, and database wait time. A cursor moves
// past failed events within this pass. Run carries progress across budgets
// and retries older failures after reaching the end of the sweep.
func (g *Grouper) RunOnce(ctx context.Context) GroupingStats {
	return g.runPass(ctx, nil)
}

func (g *Grouper) runPass(ctx context.Context, cursor *groupingCursor) (stats GroupingStats) {
	start := time.Now()
	defer func() { stats.Duration = time.Since(start) }()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if cursor == nil {
		var through rawEvent
		err := g.pool.QueryRow(ctx, `SELECT id,received_at FROM events WHERE issue_id IS NULL ORDER BY received_at DESC,id DESC LIMIT 1`).Scan(&through.ID, &through.ReceivedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		if err != nil {
			stats.Failed++
			slog.Error("grouper sweep", "err", err)
			return
		}
		cursor = &groupingCursor{through: &through}
	}
	// Freeze the upper boundary so new arrivals cannot postpone failed-row
	// retries forever. The next sweep picks up rows outside this boundary.
	stats.next = cursor
	for ctx.Err() == nil {
		if stats.Observed >= grouperPassLimit || time.Since(start) >= grouperWorkTime {
			stats.BudgetReached = true
			return
		}
		events, err := g.readPage(ctx, cursor)
		if err != nil {
			stats.Failed++
			slog.Error("grouper query", "err", err)
			return
		}
		if len(events) == 0 {
			stats.next = nil
			return
		}
		stats.Batches++
		if stats.Observed == 0 {
			stats.OldestAge = max(time.Since(events[0].ReceivedAt), 0)
		}
		for _, e := range events {
			if ctx.Err() != nil {
				return
			}
			if time.Since(start) >= grouperWorkTime {
				stats.BudgetReached = true
				return
			}
			stats.Observed++
			stats.next = &groupingCursor{after: &rawEvent{ID: e.ID, ReceivedAt: e.ReceivedAt}, through: cursor.through}
			grouped, err := g.group(ctx, e)
			if err != nil {
				stats.Failed++
				slog.Error("group event", "err", err)
			} else if grouped {
				stats.Grouped++
			}
		}
		cursor = stats.next
		if len(events) < grouperPageSize {
			stats.next = nil
			return
		}
	}
	return
}

func (g *Grouper) readPage(ctx context.Context, cursor *groupingCursor) ([]rawEvent, error) {
	query := `SELECT id, project_id, timestamp, received_at, payload FROM events WHERE issue_id IS NULL AND (received_at,id) <= ($1,$2)`
	args := []any{cursor.through.ReceivedAt, cursor.through.ID}
	if cursor.after != nil {
		query += ` AND (received_at,id) > ($3,$4)`
		args = append(args, cursor.after.ReceivedAt, cursor.after.ID)
	}
	query += ` ORDER BY received_at ASC, id ASC LIMIT 200`
	rows, err := g.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []rawEvent
	for rows.Next() {
		var e rawEvent
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Timestamp, &e.ReceivedAt, &e.Payload); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (g *Grouper) group(ctx context.Context, e rawEvent) (bool, error) {

	fp := Compute(e.Payload)
	title := Title(e.Payload)

	var partial struct {
		Level       string          `json:"level"`
		Environment string          `json:"environment"`
		Release     string          `json:"release"`
		Tags        json.RawMessage `json:"tags"`
	}
	_ = json.Unmarshal(e.Payload, &partial)
	level := partial.Level
	if level == "" {
		level = "error"
	}

	issue, isNew, wasRegressed, err := storage.GroupEvent(ctx, g.pool, e.ID, fp, title, level, partial.Environment, partial.Release)
	if err != nil {
		return false, err
	}
	if issue == nil {
		return false, nil
	}
	tags := mergeImplicitTags(storage.ParseTags(partial.Tags), extractImplicitTags(e.Payload))
	if len(tags) > 0 {
		if err := storage.InsertEventTags(ctx, g.pool, e.ID, issue.ID, e.ProjectID, tags); err != nil {
			slog.Error("insert tags", "err", err)
			// Non-fatal: tagging failure must not stall grouping.
		}
	}
	if isNew {
		if err := storage.InsertIssueHistory(ctx, g.pool, storage.IssueHistoryEntry{
			IssueID:   issue.ID,
			EventType: "created",
			CreatedAt: e.Timestamp,
		}); err != nil {
			slog.Error("insert issue history (created)", "err", err)
		}
	}
	if wasRegressed {
		if err := storage.InsertIssueHistory(ctx, g.pool, storage.IssueHistoryEntry{
			IssueID:   issue.ID,
			EventType: "regressed",
			CreatedAt: e.Timestamp,
		}); err != nil {
			slog.Error("insert issue history (regressed)", "err", err)
		}
	}

	return true, nil
}
