package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/robfig/cron/v3"
)

// CheckinDot is a minimal check-in summary used for the timeline strip.
type CheckinDot struct {
	Status     string    `json:"status"`
	ReceivedAt time.Time `json:"received_at"`
}

type CronMonitor struct {
	ID                string       `json:"id"`
	ProjectID         string       `json:"project_id"`
	Name              string       `json:"name"`
	Schedule          string       `json:"schedule"`
	GracePeriodSecs   int          `json:"grace_period_secs"`
	Status            string       `json:"status"` // active, paused
	IsRunning         bool         `json:"is_running"`
	LastOkAt          *time.Time   `json:"last_ok_at,omitempty"`
	NextExpectedAt    *time.Time   `json:"next_expected_at,omitempty"`
	LastCheckinStatus *string      `json:"last_checkin_status,omitempty"`
	LastCheckinAt     *time.Time   `json:"last_checkin_at,omitempty"`
	CreatedAt         time.Time    `json:"created_at"`
	State             string       `json:"state"`           // derived
	RecentCheckins    []CheckinDot `json:"recent_checkins"` // last 20, oldest-first
}

type CronCheckin struct {
	ID          string     `json:"id"`
	MonitorID   string     `json:"monitor_id"`
	Status      string     `json:"status"` // in_progress, ok, error
	DurationMs  *int       `json:"duration_ms,omitempty"`
	Environment *string    `json:"environment,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	ReceivedAt  time.Time  `json:"received_at"`
}

// ParseCronSchedule validates and parses a standard 5-field cron expression.
func ParseCronSchedule(expr string) (cron.Schedule, error) {
	p := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	return p.Parse(expr)
}

func computeMonitorState(m *CronMonitor) string {
	if m.IsRunning {
		return "in_progress"
	}
	if m.LastCheckinStatus != nil && *m.LastCheckinStatus == "error" {
		return "error"
	}
	if m.NextExpectedAt == nil {
		return "unknown"
	}
	deadline := m.NextExpectedAt.Add(time.Duration(m.GracePeriodSecs) * time.Second)
	if time.Now().UTC().After(deadline) {
		return "missed"
	}
	return "ok"
}

const cronMonitorCols = `id, project_id, name, schedule, grace_period_secs, status,
    is_running, last_ok_at, next_expected_at, last_checkin_status, last_checkin_at, created_at`

func scanCronMonitor(scan func(...any) error) (*CronMonitor, error) {
	var m CronMonitor
	err := scan(
		&m.ID, &m.ProjectID, &m.Name, &m.Schedule,
		&m.GracePeriodSecs, &m.Status,
		&m.IsRunning, &m.LastOkAt, &m.NextExpectedAt,
		&m.LastCheckinStatus, &m.LastCheckinAt,
		&m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	m.State = computeMonitorState(&m)
	return &m, nil
}

const recentCheckinsExpr = `COALESCE((
	SELECT json_agg(j ORDER BY j.received_at ASC)
	FROM (
		SELECT status, received_at FROM cron_checkins
		WHERE monitor_id = m.id
		ORDER BY received_at DESC LIMIT 20
	) j
), '[]'::json)`

func ListCronMonitors(ctx context.Context, pool *pgxpool.Pool, projectIDs []string) ([]*CronMonitor, error) {
	base := `SELECT m.id, m.project_id, m.name, m.schedule, m.grace_period_secs, m.status,
		m.is_running, m.last_ok_at, m.next_expected_at, m.last_checkin_status, m.last_checkin_at, m.created_at,
		` + recentCheckinsExpr + ` AS recent_checkins
		FROM cron_monitors m`

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
	var out []*CronMonitor
	for rows.Next() {
		var m CronMonitor
		var recentJSON []byte
		err := rows.Scan(
			&m.ID, &m.ProjectID, &m.Name, &m.Schedule,
			&m.GracePeriodSecs, &m.Status,
			&m.IsRunning, &m.LastOkAt, &m.NextExpectedAt,
			&m.LastCheckinStatus, &m.LastCheckinAt,
			&m.CreatedAt,
			&recentJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		m.State = computeMonitorState(&m)
		if len(recentJSON) > 0 {
			_ = json.Unmarshal(recentJSON, &m.RecentCheckins)
		}
		if m.RecentCheckins == nil {
			m.RecentCheckins = []CheckinDot{}
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

func GetCronMonitor(ctx context.Context, pool *pgxpool.Pool, id string) (*CronMonitor, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+cronMonitorCols+` FROM cron_monitors WHERE id = $1`, id)
	m, err := scanCronMonitor(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return m, nil
}

func CreateCronMonitor(ctx context.Context, pool *pgxpool.Pool, m *CronMonitor) (*CronMonitor, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO cron_monitors (project_id, name, schedule, grace_period_secs, status)
		VALUES ($1, $2, $3, $4, 'active')
		RETURNING `+cronMonitorCols,
		m.ProjectID, m.Name, m.Schedule, m.GracePeriodSecs,
	)
	created, err := scanCronMonitor(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	return created, nil
}

func UpdateCronMonitor(ctx context.Context, pool *pgxpool.Pool, m *CronMonitor) (*CronMonitor, error) {
	row := pool.QueryRow(ctx, `
		UPDATE cron_monitors SET name=$2, schedule=$3, grace_period_secs=$4, status=$5
		WHERE id=$1
		RETURNING `+cronMonitorCols,
		m.ID, m.Name, m.Schedule, m.GracePeriodSecs, m.Status,
	)
	updated, err := scanCronMonitor(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	return updated, nil
}

func DeleteCronMonitor(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM cron_monitors WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// RecordCheckin inserts a check-in and updates the monitor state atomically.
// For in_progress check-ins, started_at and finished_at should be nil.
// For terminal (ok/error) single-shot pings, finished_at should be set.
func RecordCheckin(ctx context.Context, pool *pgxpool.Pool, monitorID string, ci *CronCheckin) (*CronCheckin, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var created CronCheckin
	err = tx.QueryRow(ctx, `
		INSERT INTO cron_checkins (monitor_id, status, duration_ms, environment, started_at, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, monitor_id, status, duration_ms, environment, started_at, finished_at, received_at`,
		monitorID, ci.Status, ci.DurationMs, ci.Environment, ci.StartedAt, ci.FinishedAt,
	).Scan(
		&created.ID, &created.MonitorID, &created.Status,
		&created.DurationMs, &created.Environment,
		&created.StartedAt, &created.FinishedAt, &created.ReceivedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert checkin: %w", err)
	}

	if err := updateMonitorAfterCheckin(ctx, tx, monitorID, ci.Status); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &created, nil
}

// FinishCheckin updates an existing in_progress check-in to ok/error.
func FinishCheckin(ctx context.Context, pool *pgxpool.Pool, monitorID, checkinID, status string, durationMs *int) (*CronCheckin, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()
	var updated CronCheckin
	err = tx.QueryRow(ctx, `
		UPDATE cron_checkins SET status=$3, duration_ms=$4, finished_at=$5
		WHERE id=$1 AND monitor_id=$2 AND status='in_progress'
		RETURNING id, monitor_id, status, duration_ms, environment, started_at, finished_at, received_at`,
		checkinID, monitorID, status, durationMs, now,
	).Scan(
		&updated.ID, &updated.MonitorID, &updated.Status,
		&updated.DurationMs, &updated.Environment,
		&updated.StartedAt, &updated.FinishedAt, &updated.ReceivedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update checkin: %w", err)
	}

	if err := updateMonitorAfterCheckin(ctx, tx, monitorID, status); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &updated, nil
}

func updateMonitorAfterCheckin(ctx context.Context, tx pgx.Tx, monitorID, status string) error {
	now := time.Now().UTC()
	switch status {
	case "in_progress":
		_, err := tx.Exec(ctx,
			`UPDATE cron_monitors SET is_running=TRUE WHERE id=$1`, monitorID)
		return err
	case "ok":
		var schedule string
		var graceSecs int
		err := tx.QueryRow(ctx,
			`SELECT schedule, grace_period_secs FROM cron_monitors WHERE id=$1`, monitorID,
		).Scan(&schedule, &graceSecs)
		if err != nil {
			return fmt.Errorf("get schedule: %w", err)
		}
		sched, parseErr := ParseCronSchedule(schedule)
		if parseErr != nil {
			return fmt.Errorf("parse schedule: %w", parseErr)
		}
		nextAt := sched.Next(now)
		_, err = tx.Exec(ctx, `
			UPDATE cron_monitors SET
				is_running=FALSE, last_ok_at=$2, next_expected_at=$3,
				last_checkin_status='ok', last_checkin_at=$2
			WHERE id=$1`, monitorID, now, nextAt)
		return err
	case "error":
		_, err := tx.Exec(ctx, `
			UPDATE cron_monitors SET
				is_running=FALSE, last_checkin_status='error', last_checkin_at=$2
			WHERE id=$1`, monitorID, now)
		return err
	}
	return nil
}

func GetCheckin(ctx context.Context, pool *pgxpool.Pool, id string) (*CronCheckin, error) {
	var ci CronCheckin
	err := pool.QueryRow(ctx, `
		SELECT id, monitor_id, status, duration_ms, environment, started_at, finished_at, received_at
		FROM cron_checkins WHERE id=$1`, id,
	).Scan(
		&ci.ID, &ci.MonitorID, &ci.Status,
		&ci.DurationMs, &ci.Environment,
		&ci.StartedAt, &ci.FinishedAt, &ci.ReceivedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &ci, nil
}

func ListCheckins(ctx context.Context, pool *pgxpool.Pool, monitorID string, limit int) ([]*CronCheckin, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := pool.Query(ctx, `
		SELECT id, monitor_id, status, duration_ms, environment, started_at, finished_at, received_at
		FROM cron_checkins WHERE monitor_id=$1
		ORDER BY received_at DESC LIMIT $2`, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var out []*CronCheckin
	for rows.Next() {
		var ci CronCheckin
		err := rows.Scan(
			&ci.ID, &ci.MonitorID, &ci.Status,
			&ci.DurationMs, &ci.Environment,
			&ci.StartedAt, &ci.FinishedAt, &ci.ReceivedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, &ci)
	}
	return out, rows.Err()
}

// ListMonitorsWithRecentErrors returns active monitors whose last check-in was an error
// after the given time. Used by the alert evaluator for cron_error rules.
func ListMonitorsWithRecentErrors(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, since time.Time) ([]*CronMonitor, error) {
	var (
		query string
		args  []any
	)
	args = append(args, since)
	base := `SELECT ` + cronMonitorCols + ` FROM cron_monitors
		WHERE status='active' AND last_checkin_status='error' AND last_checkin_at > $1`
	if len(projectIDs) > 0 {
		args = append(args, projectIDs)
		query = base + fmt.Sprintf(` AND project_id = ANY($%d::uuid[]) ORDER BY last_checkin_at DESC`, len(args))
	} else {
		query = base + ` ORDER BY last_checkin_at DESC`
	}
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var out []*CronMonitor
	for rows.Next() {
		m, err := scanCronMonitor(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListOverdueMonitors returns active monitors that have missed their expected check-in window.
// Used by the alert evaluator for cron_missed rules.
func ListOverdueMonitors(ctx context.Context, pool *pgxpool.Pool, projectIDs []string) ([]*CronMonitor, error) {
	var (
		query string
		args  []any
	)
	base := `SELECT ` + cronMonitorCols + ` FROM cron_monitors
		WHERE status='active' AND next_expected_at IS NOT NULL
		  AND next_expected_at + make_interval(secs => grace_period_secs) < NOW()`
	if len(projectIDs) > 0 {
		args = append(args, projectIDs)
		query = base + ` AND project_id = ANY($1::uuid[]) ORDER BY next_expected_at`
	} else {
		query = base + ` ORDER BY next_expected_at`
	}
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var out []*CronMonitor
	for rows.Next() {
		m, err := scanCronMonitor(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
