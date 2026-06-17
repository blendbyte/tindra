package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// failureThreshold is the number of consecutive probe failures required to
// transition a monitor's state from "up" (or "unknown") to "down".
const failureThreshold = 2

type UptimeMonitor struct {
	ID                  string           `json:"id"`
	ProjectID           string           `json:"project_id"`
	Name                string           `json:"name"`
	URL                 string           `json:"url"`
	Method              string           `json:"method"`
	IntervalSecs        int              `json:"interval_secs"`
	TimeoutSecs         int              `json:"timeout_secs"`
	ExpectedCodes       string           `json:"expected_codes"`
	BodyContains        *string          `json:"body_contains,omitempty"`
	Status              string           `json:"status"` // active, paused
	State               string           `json:"state"`  // unknown, up, down
	ConsecutiveFailures int              `json:"consecutive_failures"`
	LastCheckedAt       *time.Time       `json:"last_checked_at,omitempty"`
	LastOkAt            *time.Time       `json:"last_ok_at,omitempty"`
	NextCheckAt         *time.Time       `json:"next_check_at,omitempty"`
	LastStatusCode      *int             `json:"last_status_code,omitempty"`
	LastResponseMs      *int             `json:"last_response_ms,omitempty"`
	LastError           *string          `json:"last_error,omitempty"`
	WentDownAt          *time.Time       `json:"went_down_at,omitempty"`
	RecoveredAt         *time.Time       `json:"recovered_at,omitempty"`
	CreatedAt           time.Time        `json:"created_at"`
	RecentChecks        []UptimeCheckDot `json:"recent_checks"`
}

// UptimeCheckDot is a minimal check summary for the timeline strip.
type UptimeCheckDot struct {
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checked_at"`
}

type UptimeCheck struct {
	ID         string    `json:"id"`
	MonitorID  string    `json:"monitor_id"`
	Status     string    `json:"status"` // up, down
	StatusCode *int      `json:"status_code,omitempty"`
	ResponseMs *int      `json:"response_ms,omitempty"`
	Error      *string   `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

// UptimeStats aggregates check history over several time windows.
type UptimeStats struct {
	UptimePct24h  float64 `json:"uptime_pct_24h"`
	UptimePct7d   float64 `json:"uptime_pct_7d"`
	UptimePct30d  float64 `json:"uptime_pct_30d"`
	AvgResponseMs *int    `json:"avg_response_ms_24h"`
}

// ParseExpectedCodes validates and expands the expected_codes expression into
// a flat list of accepted HTTP status codes.
// Accepts individual codes ("200"), comma-separated lists ("200,301"),
// inclusive ranges ("200-299"), and combinations ("200-299,301").
func ParseExpectedCodes(s string) ([]int, error) {
	var codes []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if idx := strings.IndexByte(part, '-'); idx > 0 {
			lo, err1 := strconv.Atoi(part[:idx])
			hi, err2 := strconv.Atoi(part[idx+1:])
			if err1 != nil || err2 != nil || lo < 100 || hi > 599 || lo > hi {
				return nil, fmt.Errorf("invalid range %q", part)
			}
			for c := lo; c <= hi; c++ {
				codes = append(codes, c)
			}
		} else {
			c, err := strconv.Atoi(part)
			if err != nil || c < 100 || c > 599 {
				return nil, fmt.Errorf("invalid code %q", part)
			}
			codes = append(codes, c)
		}
	}
	if len(codes) == 0 {
		return nil, fmt.Errorf("expected_codes is empty")
	}
	return codes, nil
}

const uptimeMonitorCols = `id, project_id, name, url, method, interval_secs, timeout_secs,
    expected_codes, body_contains, status, state, consecutive_failures,
    last_checked_at, last_ok_at, next_check_at, last_status_code, last_response_ms,
    last_error, went_down_at, recovered_at, created_at`

func scanUptimeMonitor(scan func(...any) error) (*UptimeMonitor, error) {
	var m UptimeMonitor
	err := scan(
		&m.ID, &m.ProjectID, &m.Name, &m.URL, &m.Method,
		&m.IntervalSecs, &m.TimeoutSecs,
		&m.ExpectedCodes, &m.BodyContains,
		&m.Status, &m.State, &m.ConsecutiveFailures,
		&m.LastCheckedAt, &m.LastOkAt, &m.NextCheckAt,
		&m.LastStatusCode, &m.LastResponseMs,
		&m.LastError, &m.WentDownAt, &m.RecoveredAt, &m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

const recentUptimeChecksExpr = `COALESCE((
    SELECT json_agg(j ORDER BY j.checked_at ASC)
    FROM (
        SELECT status, checked_at FROM uptime_checks
        WHERE monitor_id = m.id
        ORDER BY checked_at DESC LIMIT 20
    ) j
), '[]'::json)`

func ListUptimeMonitors(ctx context.Context, pool *pgxpool.Pool, projectIDs []string) ([]*UptimeMonitor, error) {
	base := `SELECT m.id, m.project_id, m.name, m.url, m.method,
        m.interval_secs, m.timeout_secs, m.expected_codes, m.body_contains,
        m.status, m.state, m.consecutive_failures,
        m.last_checked_at, m.last_ok_at, m.next_check_at,
        m.last_status_code, m.last_response_ms, m.last_error, m.went_down_at, m.recovered_at, m.created_at,
        ` + recentUptimeChecksExpr + ` AS recent_checks
        FROM uptime_monitors m`

	var (
		query string
		args  []any
	)
	if len(projectIDs) > 0 {
		args = append(args, projectIDs)
		query = base + ` WHERE m.project_id = ANY($1::uuid[]) ORDER BY m.created_at DESC`
	} else {
		query = base + ` ORDER BY m.created_at DESC`
	}

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []*UptimeMonitor
	for rows.Next() {
		var m UptimeMonitor
		var recentJSON []byte
		err := rows.Scan(
			&m.ID, &m.ProjectID, &m.Name, &m.URL, &m.Method,
			&m.IntervalSecs, &m.TimeoutSecs, &m.ExpectedCodes, &m.BodyContains,
			&m.Status, &m.State, &m.ConsecutiveFailures,
			&m.LastCheckedAt, &m.LastOkAt, &m.NextCheckAt,
			&m.LastStatusCode, &m.LastResponseMs, &m.LastError, &m.WentDownAt, &m.RecoveredAt, &m.CreatedAt,
			&recentJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if len(recentJSON) > 0 {
			_ = json.Unmarshal(recentJSON, &m.RecentChecks)
		}
		if m.RecentChecks == nil {
			m.RecentChecks = []UptimeCheckDot{}
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func GetUptimeMonitor(ctx context.Context, pool *pgxpool.Pool, id string) (*UptimeMonitor, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+uptimeMonitorCols+` FROM uptime_monitors WHERE id = $1`, id)
	m, err := scanUptimeMonitor(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	m.RecentChecks = []UptimeCheckDot{}
	return m, nil
}

// GetDueUptimeMonitors returns active monitors whose next_check_at is due.
// Called by the uptime worker on each tick.
func GetDueUptimeMonitors(ctx context.Context, pool *pgxpool.Pool) ([]*UptimeMonitor, error) {
	rows, err := pool.Query(ctx, `
        SELECT `+uptimeMonitorCols+`
        FROM uptime_monitors
        WHERE status = 'active'
          AND (next_check_at IS NULL OR next_check_at <= NOW())
        ORDER BY COALESCE(next_check_at, created_at)`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var out []*UptimeMonitor
	for rows.Next() {
		m, err := scanUptimeMonitor(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		m.RecentChecks = []UptimeCheckDot{}
		out = append(out, m)
	}
	return out, rows.Err()
}

func CreateUptimeMonitor(ctx context.Context, pool *pgxpool.Pool, m *UptimeMonitor) (*UptimeMonitor, error) {
	row := pool.QueryRow(ctx, `
        INSERT INTO uptime_monitors
            (project_id, name, url, method, interval_secs, timeout_secs, expected_codes, body_contains)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
        RETURNING `+uptimeMonitorCols,
		m.ProjectID, m.Name, m.URL, m.Method,
		m.IntervalSecs, m.TimeoutSecs, m.ExpectedCodes, m.BodyContains,
	)
	created, err := scanUptimeMonitor(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	created.RecentChecks = []UptimeCheckDot{}
	return created, nil
}

func UpdateUptimeMonitor(ctx context.Context, pool *pgxpool.Pool, m *UptimeMonitor) (*UptimeMonitor, error) {
	row := pool.QueryRow(ctx, `
        UPDATE uptime_monitors SET
            name=$2, url=$3, method=$4, interval_secs=$5, timeout_secs=$6,
            expected_codes=$7, body_contains=$8, status=$9
        WHERE id=$1
        RETURNING `+uptimeMonitorCols,
		m.ID, m.Name, m.URL, m.Method, m.IntervalSecs, m.TimeoutSecs,
		m.ExpectedCodes, m.BodyContains, m.Status,
	)
	updated, err := scanUptimeMonitor(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	updated.RecentChecks = []UptimeCheckDot{}
	return updated, nil
}

func DeleteUptimeMonitor(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM uptime_monitors WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecordUptimeCheck inserts a probe result and atomically updates the monitor
// state and timing fields. The state transitions are:
//
//	up check   → state=up, consecutive_failures=0
//	down check → consecutive_failures++; state transitions to "down" once
//	             failureThreshold consecutive failures have been recorded
func RecordUptimeCheck(ctx context.Context, pool *pgxpool.Pool, monitorID string, check *UptimeCheck) (*UptimeCheck, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var created UptimeCheck
	err = tx.QueryRow(ctx, `
        INSERT INTO uptime_checks (monitor_id, status, status_code, response_ms, error)
        VALUES ($1, $2, $3, $4, $5)
        RETURNING id, monitor_id, status, status_code, response_ms, error, checked_at`,
		monitorID, check.Status, check.StatusCode, check.ResponseMs, check.Error,
	).Scan(
		&created.ID, &created.MonitorID, &created.Status,
		&created.StatusCode, &created.ResponseMs, &created.Error, &created.CheckedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert check: %w", err)
	}

	if check.Status == "up" {
		_, err = tx.Exec(ctx, `
            UPDATE uptime_monitors SET
                state = 'up',
                consecutive_failures = 0,
                last_checked_at = $2::timestamptz,
                last_ok_at = $2::timestamptz,
                next_check_at = $2::timestamptz + make_interval(secs => interval_secs),
                last_status_code = $3,
                last_response_ms = $4,
                last_error = NULL,
                recovered_at = CASE WHEN state = 'down' THEN $2::timestamptz ELSE recovered_at END
            WHERE id = $1`,
			monitorID, created.CheckedAt, check.StatusCode, check.ResponseMs,
		)
	} else {
		_, err = tx.Exec(ctx, `
            UPDATE uptime_monitors SET
                consecutive_failures = consecutive_failures + 1,
                state = CASE
                    WHEN consecutive_failures + 1 >= $2 THEN 'down'
                    ELSE state
                END,
                went_down_at = CASE
                    WHEN consecutive_failures + 1 >= $2 AND state != 'down' THEN $3::timestamptz
                    ELSE went_down_at
                END,
                last_checked_at = $3::timestamptz,
                next_check_at = $3::timestamptz + make_interval(secs => interval_secs),
                last_status_code = $4,
                last_response_ms = $5,
                last_error = $6
            WHERE id = $1`,
			monitorID, failureThreshold, created.CheckedAt, check.StatusCode, check.ResponseMs, check.Error,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("update monitor: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &created, nil
}

func ListUptimeChecks(ctx context.Context, pool *pgxpool.Pool, monitorID string, limit int) ([]*UptimeCheck, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := pool.Query(ctx, `
        SELECT id, monitor_id, status, status_code, response_ms, error, checked_at
        FROM uptime_checks WHERE monitor_id=$1
        ORDER BY checked_at DESC LIMIT $2`, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var out []*UptimeCheck
	for rows.Next() {
		var c UptimeCheck
		if err := rows.Scan(&c.ID, &c.MonitorID, &c.Status,
			&c.StatusCode, &c.ResponseMs, &c.Error, &c.CheckedAt,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// GetUptimeStats returns aggregate uptime percentages and average response
// time over 24-hour, 7-day, and 30-day windows.
func GetUptimeStats(ctx context.Context, pool *pgxpool.Pool, monitorID string) (*UptimeStats, error) {
	var s UptimeStats
	var avgMs *float64
	err := pool.QueryRow(ctx, `
        SELECT
            COALESCE(
                100.0 * SUM(CASE WHEN checked_at > NOW() - INTERVAL '24 hours' AND status='up' THEN 1 ELSE 0 END)::float
                / NULLIF(SUM(CASE WHEN checked_at > NOW() - INTERVAL '24 hours' THEN 1 ELSE 0 END)::float, 0),
            0),
            COALESCE(
                100.0 * SUM(CASE WHEN checked_at > NOW() - INTERVAL '7 days' AND status='up' THEN 1 ELSE 0 END)::float
                / NULLIF(SUM(CASE WHEN checked_at > NOW() - INTERVAL '7 days' THEN 1 ELSE 0 END)::float, 0),
            0),
            COALESCE(
                100.0 * SUM(CASE WHEN checked_at > NOW() - INTERVAL '30 days' AND status='up' THEN 1 ELSE 0 END)::float
                / NULLIF(SUM(CASE WHEN checked_at > NOW() - INTERVAL '30 days' THEN 1 ELSE 0 END)::float, 0),
            0),
            AVG(CASE WHEN checked_at > NOW() - INTERVAL '24 hours' AND status='up' THEN response_ms ELSE NULL END)
        FROM uptime_checks WHERE monitor_id = $1`, monitorID,
	).Scan(&s.UptimePct24h, &s.UptimePct7d, &s.UptimePct30d, &avgMs)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	if avgMs != nil {
		v := int(*avgMs)
		s.AvgResponseMs = &v
	}
	return &s, nil
}

// ListDownUptimeMonitors returns active monitors currently in the "down" state.
// Used by the alert evaluator for uptime_down rules.
func ListDownUptimeMonitors(ctx context.Context, pool *pgxpool.Pool, projectIDs []string) ([]*UptimeMonitor, error) {
	base := `SELECT m.id, m.project_id, m.name, m.url, m.method,
        m.interval_secs, m.timeout_secs, m.expected_codes, m.body_contains,
        m.status, m.state, m.consecutive_failures,
        m.last_checked_at, m.last_ok_at, m.next_check_at,
        m.last_status_code, m.last_response_ms, m.last_error, m.went_down_at, m.recovered_at, m.created_at,
        ` + recentUptimeChecksExpr + ` AS recent_checks
        FROM uptime_monitors m
        WHERE m.status='active' AND m.state='down'`
	var (
		query string
		args  []any
	)
	if len(projectIDs) > 0 {
		args = append(args, projectIDs)
		query = base + ` AND m.project_id = ANY($1::uuid[]) ORDER BY m.last_checked_at DESC`
	} else {
		query = base + ` ORDER BY m.last_checked_at DESC`
	}
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var out []*UptimeMonitor
	for rows.Next() {
		var m UptimeMonitor
		var recentJSON []byte
		if err := rows.Scan(
			&m.ID, &m.ProjectID, &m.Name, &m.URL, &m.Method,
			&m.IntervalSecs, &m.TimeoutSecs, &m.ExpectedCodes, &m.BodyContains,
			&m.Status, &m.State, &m.ConsecutiveFailures,
			&m.LastCheckedAt, &m.LastOkAt, &m.NextCheckAt,
			&m.LastStatusCode, &m.LastResponseMs, &m.LastError, &m.WentDownAt, &m.RecoveredAt, &m.CreatedAt,
			&recentJSON,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if len(recentJSON) > 0 {
			_ = json.Unmarshal(recentJSON, &m.RecentChecks)
		}
		if m.RecentChecks == nil {
			m.RecentChecks = []UptimeCheckDot{}
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// ListRecoveredUptimeMonitors returns active monitors that transitioned back to
// "up" after the given time. Used by the alert evaluator for uptime_recovered rules.
func ListRecoveredUptimeMonitors(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, since time.Time) ([]*UptimeMonitor, error) {
	base := `SELECT m.id, m.project_id, m.name, m.url, m.method,
        m.interval_secs, m.timeout_secs, m.expected_codes, m.body_contains,
        m.status, m.state, m.consecutive_failures,
        m.last_checked_at, m.last_ok_at, m.next_check_at,
        m.last_status_code, m.last_response_ms, m.last_error, m.went_down_at, m.recovered_at, m.created_at,
        ` + recentUptimeChecksExpr + ` AS recent_checks
        FROM uptime_monitors m
        WHERE m.status='active' AND m.state='up' AND m.went_down_at IS NOT NULL AND m.recovered_at > $1`
	var (
		query string
		args  []any
	)
	args = append(args, since)
	if len(projectIDs) > 0 {
		args = append(args, projectIDs)
		query = base + fmt.Sprintf(` AND m.project_id = ANY($%d::uuid[]) ORDER BY m.last_ok_at DESC`, len(args))
	} else {
		query = base + ` ORDER BY m.last_ok_at DESC`
	}
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var out []*UptimeMonitor
	for rows.Next() {
		var m UptimeMonitor
		var recentJSON []byte
		if err := rows.Scan(
			&m.ID, &m.ProjectID, &m.Name, &m.URL, &m.Method,
			&m.IntervalSecs, &m.TimeoutSecs, &m.ExpectedCodes, &m.BodyContains,
			&m.Status, &m.State, &m.ConsecutiveFailures,
			&m.LastCheckedAt, &m.LastOkAt, &m.NextCheckAt,
			&m.LastStatusCode, &m.LastResponseMs, &m.LastError, &m.WentDownAt, &m.RecoveredAt, &m.CreatedAt,
			&recentJSON,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if len(recentJSON) > 0 {
			_ = json.Unmarshal(recentJSON, &m.RecentChecks)
		}
		if m.RecentChecks == nil {
			m.RecentChecks = []UptimeCheckDot{}
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}
