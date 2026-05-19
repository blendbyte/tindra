package issues

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

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

// Run processes ungrouped events every 500ms. Call in a dedicated goroutine.
func (g *Grouper) Run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			g.processBatch(ctx)
		}
	}
}

func (g *Grouper) processBatch(ctx context.Context) {
	rows, err := g.pool.Query(ctx, `
		SELECT id, project_id, timestamp, payload
		FROM events
		WHERE issue_id IS NULL
		ORDER BY received_at ASC
		LIMIT 200
	`)
	if err != nil {
		slog.Error("grouper query", "err", err)
		return
	}

	type rawEvent struct {
		ID        string
		ProjectID string
		Timestamp time.Time
		Payload   json.RawMessage
	}
	var events []rawEvent
	for rows.Next() {
		var e rawEvent
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Timestamp, &e.Payload); err != nil {
			slog.Error("grouper scan", "err", err)
			rows.Close()
			return
		}
		events = append(events, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		slog.Error("grouper rows", "err", err)
		return
	}

	for _, e := range events {
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

		issue, isNew, wasRegressed, err := storage.UpsertIssue(ctx, g.pool, e.ProjectID, fp, title, level, "error", partial.Environment, partial.Release, e.Timestamp)
		if err != nil {
			slog.Error("upsert issue", "err", err)
			continue
		}
		if err := storage.LinkEventToIssue(ctx, g.pool, e.ID, issue.ID, fp); err != nil {
			slog.Error("link event", "err", err)
			continue
		}
		if tags := storage.ParseTags(partial.Tags); len(tags) > 0 {
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

	}
}
