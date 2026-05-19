package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type IssueHistoryEntry struct {
	ID        string         `json:"id"`
	IssueID   string         `json:"issue_id"`
	ActorID   *string        `json:"actor_id"`
	EventType string         `json:"event_type"`
	Details   map[string]any `json:"details"`
	CreatedAt time.Time      `json:"created_at"`
}

func InsertIssueHistory(ctx context.Context, pool *pgxpool.Pool, e IssueHistoryEntry) error {
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO issue_history (issue_id, actor_id, event_type, details, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`, e.IssueID, e.ActorID, e.EventType, e.Details, e.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert issue history: %w", err)
	}
	return nil
}

func GetIssueHistory(ctx context.Context, pool *pgxpool.Pool, issueID string) ([]*IssueHistoryEntry, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, issue_id, actor_id, event_type, details, created_at
		FROM issue_history
		WHERE issue_id = $1
		ORDER BY created_at ASC
		LIMIT 500
	`, issueID)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []*IssueHistoryEntry
	for rows.Next() {
		var e IssueHistoryEntry
		var rawDetails []byte
		if err := rows.Scan(&e.ID, &e.IssueID, &e.ActorID, &e.EventType, &rawDetails, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if err := json.Unmarshal(rawDetails, &e.Details); err != nil {
			e.Details = map[string]any{}
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}
