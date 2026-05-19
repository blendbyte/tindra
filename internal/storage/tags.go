package storage

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TagValue struct {
	Value string `json:"value"`
	Count int    `json:"count"`
	Pct   int    `json:"pct"`
}

type TagSummary struct {
	Key    string     `json:"key"`
	Total  int        `json:"total"`
	Values []TagValue `json:"values"`
}

// ParseTags normalises Sentry's two tag formats into [][2]string pairs.
// SDKs send either an object {"key":"val"} or an array [["key","val"]].
func ParseTags(raw json.RawMessage) [][2]string {
	if len(raw) == 0 {
		return nil
	}
	var arr [][2]string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr
	}
	var obj map[string]string
	if err := json.Unmarshal(raw, &obj); err == nil {
		out := make([][2]string, 0, len(obj))
		for k, v := range obj {
			out = append(out, [2]string{k, v})
		}
		return out
	}
	return nil
}

// InsertEventTags stores extracted tag pairs for one event.
// Silently skips duplicate keys (PRIMARY KEY constraint).
func InsertEventTags(ctx context.Context, pool *pgxpool.Pool, eventID, issueID, projectID string, tags [][2]string) error {
	if len(tags) == 0 {
		return nil
	}
	b := &pgx.Batch{}
	for _, t := range tags {
		b.Queue(`
			INSERT INTO event_tags (event_id, issue_id, project_id, key, value)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (event_id, key) DO NOTHING
		`, eventID, issueID, projectID, t[0], t[1])
	}
	results := pool.SendBatch(ctx, b)
	defer results.Close()
	for range tags {
		if _, err := results.Exec(); err != nil {
			return fmt.Errorf("insert event tag: %w", err)
		}
	}
	return nil
}

// GetIssueTags returns aggregated tag distribution across all events for an issue.
// Returns at most 5 values per key, ordered by frequency.
func GetIssueTags(ctx context.Context, pool *pgxpool.Pool, issueID string) ([]TagSummary, error) {
	rows, err := pool.Query(ctx, `
		SELECT key, value, cnt, total
		FROM (
			SELECT
				key,
				value,
				COUNT(*) AS cnt,
				SUM(COUNT(*)) OVER (PARTITION BY key) AS total,
				ROW_NUMBER() OVER (PARTITION BY key ORDER BY COUNT(*) DESC) AS rn
			FROM event_tags
			WHERE issue_id = $1
			GROUP BY key, value
		) t
		WHERE rn <= 5
		ORDER BY key, cnt DESC
	`, issueID)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	byKey := map[string]*TagSummary{}
	var order []string
	for rows.Next() {
		var key, value string
		var cnt, total int
		if err := rows.Scan(&key, &value, &cnt, &total); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		s, ok := byKey[key]
		if !ok {
			s = &TagSummary{Key: key, Total: total}
			byKey[key] = s
			order = append(order, key)
		}
		pct := 0
		if total > 0 {
			pct = int(float64(cnt) / float64(total) * 100)
		}
		s.Values = append(s.Values, TagValue{Value: value, Count: cnt, Pct: pct})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]TagSummary, 0, len(order))
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	return out, nil
}
