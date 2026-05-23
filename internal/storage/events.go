package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type EventRow struct {
	ID         string
	Release    *string
	TraceID    *string
	ReceivedAt time.Time
	Payload    json.RawMessage
}

// GetTopFrames returns up to limit human-readable stack frame strings from the
// most recent event for an issue, ordered outermost-call first.
// In-app frames are preferred; falls back to all frames if none are flagged.
// Returns nil without error when there is no event or no stack trace.
func GetTopFrames(ctx context.Context, pool *pgxpool.Pool, issueID string, limit int) []string {
	ev, err := GetLatestEventForIssue(ctx, pool, issueID)
	if err != nil || ev == nil {
		return nil
	}

	var p struct {
		Exception struct {
			Values []struct {
				Stacktrace struct {
					Frames []struct {
						Function string `json:"function"`
						Filename string `json:"filename"`
						Module   string `json:"module"`
						Lineno   int    `json:"lineno"`
						InApp    bool   `json:"in_app"`
					} `json:"frames"`
				} `json:"stacktrace"`
			} `json:"values"`
		} `json:"exception"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil || len(p.Exception.Values) == 0 {
		return nil
	}

	frames := p.Exception.Values[0].Stacktrace.Frames
	if len(frames) == 0 {
		return nil
	}

	format := func(fn, filename, module string, lineno int) string {
		loc := filename
		if loc == "" {
			loc = module
		}
		if loc == "" {
			return fn
		}
		if lineno > 0 {
			return fmt.Sprintf("%s  %s:%d", fn, loc, lineno)
		}
		return fmt.Sprintf("%s  %s", fn, loc)
	}

	// Sentry frames are bottom-up; walk from the end (top of call stack).
	collect := func(inAppOnly bool) []string {
		var out []string
		for i := len(frames) - 1; i >= 0 && len(out) < limit; i-- {
			f := frames[i]
			if inAppOnly && !f.InApp {
				continue
			}
			out = append(out, format(f.Function, f.Filename, f.Module, f.Lineno))
		}
		return out
	}

	if lines := collect(true); len(lines) > 0 {
		return lines
	}
	return collect(false)
}

// GetLatestEventForIssue returns the most recently received event for an issue.
func GetLatestEventForIssue(ctx context.Context, pool *pgxpool.Pool, issueID string) (*EventRow, error) {
	return GetEventForIssueAtOffset(ctx, pool, issueID, 0)
}

// GetEventForIssueAtOffset returns the event at position offset (0 = newest) for an issue.
func GetEventForIssueAtOffset(ctx context.Context, pool *pgxpool.Pool, issueID string, offset int) (*EventRow, error) {
	if offset < 0 {
		offset = 0
	}
	row := pool.QueryRow(ctx, `
		SELECT id, release, trace_id, received_at, payload
		FROM events
		WHERE issue_id = $1
		ORDER BY received_at DESC
		LIMIT 1 OFFSET $2
	`, issueID, offset)

	var ev EventRow
	if err := row.Scan(&ev.ID, &ev.Release, &ev.TraceID, &ev.ReceivedAt, &ev.Payload); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("query: %w", err)
	}
	return &ev, nil
}

// EventSummary is a lightweight event record used in list views.
type EventSummary struct {
	ID          string            `json:"id"`
	Timestamp   time.Time         `json:"timestamp"`
	ReceivedAt  time.Time         `json:"received_at"`
	Level       *string           `json:"level"`
	Environment *string           `json:"environment"`
	Release     *string           `json:"release"`
	Tags        map[string]string `json:"tags"`
}

// ListEventsForIssue returns a keyset-paginated list of events for an issue,
// newest first, with per-event tag maps attached.
func ListEventsForIssue(ctx context.Context, pool *pgxpool.Pool, issueID string, cursorTime *time.Time, cursorID *string, limit int) ([]EventSummary, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var (
		rows pgx.Rows
		err  error
	)
	if cursorTime != nil && cursorID != nil {
		rows, err = pool.Query(ctx, `
			SELECT id, timestamp, received_at, level, environment, release
			FROM events
			WHERE issue_id = $1
			  AND (received_at, id::text) < ($2, $3)
			ORDER BY received_at DESC, id DESC
			LIMIT $4
		`, issueID, cursorTime, *cursorID, limit+1)
	} else {
		rows, err = pool.Query(ctx, `
			SELECT id, timestamp, received_at, level, environment, release
			FROM events
			WHERE issue_id = $1
			ORDER BY received_at DESC, id DESC
			LIMIT $2
		`, issueID, limit+1)
	}
	if err != nil {
		return nil, false, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var events []EventSummary
	for rows.Next() {
		var e EventSummary
		e.Tags = map[string]string{}
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.ReceivedAt, &e.Level, &e.Environment, &e.Release); err != nil {
			return nil, false, fmt.Errorf("scan: %w", err)
		}
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("rows: %w", err)
	}

	hasMore := len(events) > limit
	if hasMore {
		events = events[:limit]
	}
	if events == nil {
		events = []EventSummary{}
	}

	// Attach tags.
	if len(events) > 0 {
		ids := make([]string, len(events))
		idxByID := make(map[string]int, len(events))
		for i, e := range events {
			ids[i] = e.ID
			idxByID[e.ID] = i
		}
		tagRows, tagErr := pool.Query(ctx, `
			SELECT event_id::text, key, value
			FROM event_tags
			WHERE event_id::text = ANY($1)
		`, ids)
		if tagErr == nil {
			defer tagRows.Close()
			for tagRows.Next() {
				var evID, key, value string
				if err := tagRows.Scan(&evID, &key, &value); err != nil {
					continue
				}
				if i, ok := idxByID[evID]; ok {
					events[i].Tags[key] = value
				}
			}
		}
	}

	return events, hasMore, nil
}

// HistogramBucket holds an event count for one time bucket.
type HistogramBucket struct {
	Time  time.Time `json:"time"`
	Count int64     `json:"count"`
}

// HistogramResult is the response for the issue frequency chart.
type HistogramResult struct {
	Buckets    []HistogramBucket `json:"buckets"`
	BucketSize string            `json:"bucket_size"` // "hour" | "day" | "week"
}

// GetIssueHistogram returns a complete time-series of event counts for an issue,
// with auto-selected bucket granularity based on issue age.
func GetIssueHistogram(ctx context.Context, pool *pgxpool.Pool, issueID string, firstSeen time.Time) (*HistogramResult, error) {
	age := time.Since(firstSeen)

	var bucketSize, truncFunc string
	switch {
	case age < 48*time.Hour:
		bucketSize = "hour"
		truncFunc = "hour"
	case age < 60*24*time.Hour:
		bucketSize = "day"
		truncFunc = "day"
	default:
		bucketSize = "week"
		truncFunc = "week"
	}

	// Fetch sparse counts from the DB.
	rows, err := pool.Query(ctx, fmt.Sprintf(`
		SELECT date_trunc('%s', received_at) AS bucket, count(*)
		FROM events
		WHERE issue_id = $1
		GROUP BY 1
		ORDER BY 1
	`, truncFunc), issueID)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	sparse := map[time.Time]int64{}
	for rows.Next() {
		var t time.Time
		var cnt int64
		if err := rows.Scan(&t, &cnt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		sparse[t.UTC()] = cnt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows: %w", err)
	}

	// Fill the complete series from firstSeen to now with zero buckets for gaps.
	start := truncateBucket(firstSeen.UTC(), bucketSize)
	end := truncateBucket(time.Now().UTC(), bucketSize)

	var buckets []HistogramBucket
	for t := start; !t.After(end); t = advanceBucket(t, bucketSize) {
		buckets = append(buckets, HistogramBucket{Time: t, Count: sparse[t]})
	}
	if buckets == nil {
		buckets = []HistogramBucket{}
	}

	return &HistogramResult{Buckets: buckets, BucketSize: bucketSize}, nil
}

func truncateBucket(t time.Time, bucket string) time.Time {
	switch bucket {
	case "hour":
		return t.Truncate(time.Hour)
	case "day":
		y, m, d := t.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	case "week":
		// Match PostgreSQL date_trunc('week') which starts on Monday (ISO 8601).
		y, m, d := t.Date()
		wd := int(t.Weekday())
		if wd == 0 {
			wd = 7
		}
		d -= (wd - 1)
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	return t
}

func advanceBucket(t time.Time, bucket string) time.Time {
	switch bucket {
	case "day":
		return t.AddDate(0, 0, 1)
	case "week":
		return t.AddDate(0, 0, 7)
	default: // hour
		return t.Add(time.Hour)
	}
}
