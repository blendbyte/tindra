package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Project struct {
	ID             string          `json:"id"`
	Slug           string          `json:"slug"`
	Name           string          `json:"name"`
	PublicKey      string          `json:"public_key"`
	PassthroughDSN *string         `json:"passthrough_dsn"`
	ScrubFields    []string        `json:"scrub_fields"`
	ScrubPatterns  json.RawMessage `json:"scrub_patterns"`
	// ProfilingEnabled gates profile ingestion per project, so a noisy service
	// can be silenced without redeploying it to change its SDK config.
	ProfilingEnabled bool      `json:"profiling_enabled"`
	CreatedAt        time.Time `json:"created_at"`
	EventCount       int64     `json:"event_count"`
	Events24h        int64     `json:"events_24h"`
	StorageBytes     int64     `json:"storage_bytes"`
	LogCount         int64     `json:"log_count"`
	TxCount          int64     `json:"tx_count"`
}

func CreateProject(ctx context.Context, pool *pgxpool.Pool, slug, name string) (*Project, error) {
	key, err := generatePublicKey()
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	var p Project
	err = pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, public_key)
		VALUES ($1, $2, $3)
		RETURNING id, slug, name, public_key, passthrough_dsn, scrub_fields, scrub_patterns, profiling_enabled, created_at
	`, slug, name, key).Scan(&p.ID, &p.Slug, &p.Name, &p.PublicKey, &p.PassthroughDSN, &p.ScrubFields, &p.ScrubPatterns, &p.ProfilingEnabled, &p.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}
	return &p, nil
}

func CountProjects(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM projects`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

func ListProjects(ctx context.Context, pool *pgxpool.Pool) ([]*Project, error) {
	rows, err := pool.Query(ctx, `
		WITH
		  sizes AS (
		    SELECT
		      GREATEST(1, (SELECT COUNT(*) FROM events))       AS total_ev,
		      GREATEST(1, (SELECT COUNT(*) FROM transactions)) AS total_tx,
		      GREATEST(1, (SELECT COUNT(*) FROM logs))         AS total_log,
		      pg_total_relation_size('events') + pg_total_relation_size('event_tags')                                      AS ev_bytes,
		      pg_total_relation_size('transactions') + pg_total_relation_size('spans') + pg_total_relation_size('perf_events') AS tx_bytes,
		      pg_total_relation_size('logs')                                                                                AS log_bytes
		  ),
		  ev AS (
		    SELECT project_id,
		      COUNT(*) FILTER (WHERE received_at >= date_trunc('month', now()))        AS month_cnt,
		      COUNT(*) FILTER (WHERE received_at > now() - INTERVAL '24 hours')        AS day_cnt,
		      COUNT(*)                                                                  AS total
		    FROM events GROUP BY project_id
		  ),
		  tx AS (
		    SELECT project_id,
		      COUNT(*) FILTER (WHERE received_at >= date_trunc('month', now()))        AS month_cnt,
		      COUNT(*) FILTER (WHERE received_at > now() - INTERVAL '24 hours')        AS day_cnt,
		      COUNT(*)                                                                  AS total
		    FROM transactions GROUP BY project_id
		  ),
		  lg AS (
		    SELECT project_id, COUNT(*) AS total FROM logs GROUP BY project_id
		  ),
		  -- Profiles are the largest thing stored, and unlike the other tables
		  -- every row already carries its own compressed size, so this is summed
		  -- outright rather than apportioned from a table total by row count.
		  pr AS (
		    SELECT project_id, SUM(size_bytes)::float8 AS bytes FROM profile_chunks GROUP BY project_id
		  )
		SELECT
		  p.id, p.slug, p.name, p.public_key, p.passthrough_dsn, p.scrub_fields, p.scrub_patterns, p.profiling_enabled, p.created_at,
		  COALESCE(ev.month_cnt, 0) + COALESCE(tx.month_cnt, 0) AS event_count,
		  COALESCE(ev.day_cnt,   0) + COALESCE(tx.day_cnt,   0) AS events_24h,
		  (
		    COALESCE(ev.total, 0)::float8 / s.total_ev  * s.ev_bytes  +
		    COALESCE(tx.total, 0)::float8 / s.total_tx  * s.tx_bytes  +
		    COALESCE(lg.total, 0)::float8 / s.total_log * s.log_bytes +
		    COALESCE(pr.bytes, 0)
		  )::int8 AS storage_bytes,
		  COALESCE(lg.total, 0) AS log_count,
		  COALESCE(tx.total, 0) AS tx_count
		FROM projects p
		CROSS JOIN sizes s
		LEFT JOIN ev  ON ev.project_id  = p.id
		LEFT JOIN tx  ON tx.project_id  = p.id
		LEFT JOIN lg  ON lg.project_id  = p.id
		LEFT JOIN pr  ON pr.project_id  = p.id
		ORDER BY p.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var projects []*Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.PublicKey, &p.PassthroughDSN, &p.ScrubFields, &p.ScrubPatterns, &p.ProfilingEnabled, &p.CreatedAt, &p.EventCount, &p.Events24h, &p.StorageBytes, &p.LogCount, &p.TxCount); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		projects = append(projects, &p)
	}
	return projects, rows.Err()
}

// UpdateProject renames a project and updates its passthrough DSN.
//
// profilingEnabled is a pointer so that a caller which does not mention it
// leaves the current value alone. An API client that only knows about the
// older fields would otherwise silently turn profiling off for the project.
func UpdateProject(ctx context.Context, pool *pgxpool.Pool, id, name, slug string, passthroughDSN *string, profilingEnabled *bool) (*Project, error) {
	var p Project
	err := pool.QueryRow(ctx, `
		UPDATE projects
		SET name = $2, slug = $3, passthrough_dsn = $4,
		    profiling_enabled = COALESCE($5, profiling_enabled)
		WHERE id = $1
		RETURNING id, slug, name, public_key, passthrough_dsn, scrub_fields, scrub_patterns, profiling_enabled, created_at
	`, id, name, slug, passthroughDSN, profilingEnabled).Scan(&p.ID, &p.Slug, &p.Name, &p.PublicKey, &p.PassthroughDSN, &p.ScrubFields, &p.ScrubPatterns, &p.ProfilingEnabled, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	return &p, nil
}

func DeleteProjectByID(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func CountProjectEvents(ctx context.Context, pool *pgxpool.Pool, projectID string) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM events WHERE project_id = $1 AND received_at >= date_trunc('month', now())) +
			(SELECT COUNT(*) FROM transactions WHERE project_id = $1 AND received_at >= date_trunc('month', now()))
	`, projectID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

func CountMonthlyEvents(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM events WHERE received_at >= date_trunc('month', now())) +
			(SELECT COUNT(*) FROM transactions WHERE received_at >= date_trunc('month', now()))
	`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

func CountLastMonthEvents(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM events
				WHERE received_at >= date_trunc('month', now()) - interval '1 month'
				  AND received_at <  date_trunc('month', now())) +
			(SELECT COUNT(*) FROM transactions
				WHERE received_at >= date_trunc('month', now()) - interval '1 month'
				  AND received_at <  date_trunc('month', now()))
	`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

// DailyEventVolume returns 30 days of per-day event+transaction counts for a project,
// ordered oldest-first. Days with no activity are included as zero.
func DailyEventVolume(ctx context.Context, pool *pgxpool.Pool, projectID string) ([]int64, error) {
	rows, err := pool.Query(ctx, `
		SELECT d.day, COALESCE(SUM(c.n), 0)::bigint
		FROM generate_series(
			current_date - interval '29 days',
			current_date,
			interval '1 day'
		) AS d(day)
		LEFT JOIN (
			SELECT date_trunc('day', received_at)::date AS day, COUNT(*) AS n
			FROM events
			WHERE project_id = $1 AND received_at >= current_date - interval '29 days'
			GROUP BY 1
			UNION ALL
			SELECT date_trunc('day', received_at)::date AS day, COUNT(*) AS n
			FROM transactions
			WHERE project_id = $1 AND received_at >= current_date - interval '29 days'
			GROUP BY 1
		) AS c ON d.day = c.day
		GROUP BY d.day
		ORDER BY d.day
	`, projectID)
	if err != nil {
		return nil, fmt.Errorf("daily volume: %w", err)
	}
	defer rows.Close()

	out := make([]int64, 0, 30)
	for rows.Next() {
		var day time.Time
		var n int64
		if err := rows.Scan(&day, &n); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func DeleteProject(ctx context.Context, pool *pgxpool.Pool, slug string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM projects WHERE slug = $1`, slug)
	if err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func GetByPublicKey(ctx context.Context, pool *pgxpool.Pool, key string) (*Project, error) {
	var p Project
	err := pool.QueryRow(ctx, `
		SELECT id, slug, name, public_key, passthrough_dsn, scrub_fields, scrub_patterns, profiling_enabled, created_at
		FROM projects WHERE public_key = $1
	`, key).Scan(&p.ID, &p.Slug, &p.Name, &p.PublicKey, &p.PassthroughDSN, &p.ScrubFields, &p.ScrubPatterns, &p.ProfilingEnabled, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &p, nil
}

// GetByIDAndPublicKey looks up a project by its UUID and validates the public key in one query.
// Returns nil if either the ID or the key doesn't match, preventing project enumeration.
func GetByIDAndPublicKey(ctx context.Context, pool *pgxpool.Pool, id, publicKey string) (*Project, error) {
	var p Project
	err := pool.QueryRow(ctx, `
		SELECT id, slug, name, public_key, passthrough_dsn, scrub_fields, scrub_patterns, profiling_enabled, created_at
		FROM projects WHERE id = $1 AND public_key = $2
	`, id, publicKey).Scan(&p.ID, &p.Slug, &p.Name, &p.PublicKey, &p.PassthroughDSN, &p.ScrubFields, &p.ScrubPatterns, &p.ProfilingEnabled, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &p, nil
}

func GetProjectBySlug(ctx context.Context, pool *pgxpool.Pool, slug string) (*Project, error) {
	var p Project
	err := pool.QueryRow(ctx, `
		SELECT id, slug, name, public_key, passthrough_dsn, scrub_fields, scrub_patterns, profiling_enabled, created_at
		FROM projects WHERE slug = $1
	`, slug).Scan(&p.ID, &p.Slug, &p.Name, &p.PublicKey, &p.PassthroughDSN, &p.ScrubFields, &p.ScrubPatterns, &p.ProfilingEnabled, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &p, nil
}

func UpdateProjectScrubbing(ctx context.Context, pool *pgxpool.Pool, id string, fields []string, patterns json.RawMessage) (*Project, error) {
	var p Project
	err := pool.QueryRow(ctx, `
		UPDATE projects SET scrub_fields = $2, scrub_patterns = $3
		WHERE id = $1
		RETURNING id, slug, name, public_key, passthrough_dsn, scrub_fields, scrub_patterns, profiling_enabled, created_at
	`, id, fields, patterns).Scan(&p.ID, &p.Slug, &p.Name, &p.PublicKey, &p.PassthroughDSN, &p.ScrubFields, &p.ScrubPatterns, &p.ProfilingEnabled, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update scrubbing: %w", err)
	}
	return &p, nil
}

type ProjectIssueCount struct {
	ProjectID  string `json:"project_id"`
	OpenIssues int64  `json:"open_issues"`
}

func GetProjectIssueCounts(ctx context.Context, pool *pgxpool.Pool, projectIDs []string) ([]*ProjectIssueCount, error) {
	rows, err := pool.Query(ctx, `
		SELECT p.id, COUNT(i.id) AS open_issues
		FROM projects p
		LEFT JOIN issues i ON i.project_id = p.id AND i.status = 'open'
		WHERE p.id = ANY($1)
		GROUP BY p.id
	`, projectIDs)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []*ProjectIssueCount
	for rows.Next() {
		var c ProjectIssueCount
		if err := rows.Scan(&c.ProjectID, &c.OpenIssues); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func generatePublicKey() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
