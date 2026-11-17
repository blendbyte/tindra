package storage

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PerformanceTrendBuckets is how many equal slices of the window a project's
// traffic trend is split into, whatever the window's length.
const PerformanceTrendBuckets = 24

// ProjectPerformance rolls up transactions for one project, or for everything
// in scope when it is the overview's total. Prev* cover the window of the same
// length right before it, so callers can say what changed.
type ProjectPerformance struct {
	ProjectID       string  `json:"project_id,omitempty"`
	Requests        int64   `json:"requests"`
	P50             float64 `json:"p50"`
	P95             float64 `json:"p95"`
	Apdex           float64 `json:"apdex"`
	FailureRate     float64 `json:"failure_rate"`
	PrevRequests    int64   `json:"prev_requests"`
	PrevP95         float64 `json:"prev_p95"`
	PrevFailureRate float64 `json:"prev_failure_rate"`
	Trend           []int64 `json:"trend,omitempty"`
}

// PerformanceOverview holds one row per project with transactions in either
// window, and a total computed from the raw rows. The total's percentiles are
// real percentiles, not an average of the projects' own.
type PerformanceOverview struct {
	Projects []*ProjectPerformance `json:"projects"`
	Total    ProjectPerformance    `json:"total"`
}

// GetPerformanceOverview aggregates transactions per project without going
// through per-transaction summaries, so a project with few requests is never
// cut off by a busier one's transaction names.
func GetPerformanceOverview(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, hours int, env, userIdentity string) (*PerformanceOverview, error) {
	window := ResolveTimeRange(ctx, hours, 0)
	length := window.To.Sub(window.From)
	previousFrom := window.From.Add(-length)

	filter := ""
	var extra []any
	next := 4
	if len(projectIDs) > 0 {
		extra = append(extra, projectIDs)
		filter += fmt.Sprintf(" AND project_id = ANY($%d::uuid[])", next)
		next++
	}
	if env != "" {
		extra = append(extra, env)
		filter += fmt.Sprintf(" AND environment = $%d", next)
		next++
	}
	if userIdentity != "" {
		extra = append(extra, userIdentity)
		filter += fmt.Sprintf(" AND app_user_identity_hash(user_identity) = app_user_identity_hash($%[1]d::text) AND user_identity = $%[1]d", next)
	}

	// $1 starts the previous window, $2 starts the current one, $3 ends it.
	args := append([]any{previousFrom, window.From, window.To}, extra...)
	// The histogram keeps both windows apart; GROUPING SETS adds the total's
	// own histogram, so its percentiles are real percentiles.
	cur := func(expr string) string { return `COALESCE(MAX(` + expr + `) FILTER (WHERE current), 0)` }
	prev := func(expr string) string { return `COALESCE(MAX(` + expr + `) FILTER (WHERE NOT current), 0)` }
	rows, err := pool.Query(ctx, `
		WITH hist AS (
			SELECT project_id, start_timestamp >= $2 AS current, duration_ms AS d, COUNT(*) AS c,
				COUNT(*) FILTER (WHERE status IN (`+failedTransactionStatuses+`)) AS failed
			FROM transactions
			WHERE start_timestamp >= $1 AND start_timestamp < $3`+filter+`
			GROUP BY 1, 2, 3
		), merged AS (
			SELECT project_id, current, d, SUM(c)::bigint AS c, SUM(failed)::bigint AS failed
			FROM hist
			GROUP BY GROUPING SETS ((project_id, current, d), (current, d))
		), ranked AS (
			SELECT *, `+histogramRanks("project_id, current")+` FROM merged
		), windows AS (
			SELECT
				project_id,
				current,
				SUM(c)::bigint AS requests,
				`+histogramPercentile("0.5")+` AS p50,
				`+histogramPercentile("0.95")+` AS p95,
				`+histogramApdex+` AS apdex,
				SUM(failed)::float8 / SUM(c)::float8 AS failure_rate
			FROM ranked
			GROUP BY project_id, current
		)
		SELECT
			project_id::text,
			`+cur("requests")+`, `+cur("p50")+`, `+cur("p95")+`, `+cur("apdex")+`, `+cur("failure_rate")+`,
			`+prev("requests")+`, `+prev("p95")+`, `+prev("failure_rate")+`
		FROM windows
		GROUP BY project_id
	`, rangeQueryArgs(userIdentity, args)...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	overview := &PerformanceOverview{Projects: []*ProjectPerformance{}}
	byProject := map[string]*ProjectPerformance{}
	for rows.Next() {
		var projectID *string
		var row ProjectPerformance
		if err := rows.Scan(
			&projectID, &row.Requests, &row.P50, &row.P95, &row.Apdex, &row.FailureRate,
			&row.PrevRequests, &row.PrevP95, &row.PrevFailureRate,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if projectID == nil {
			overview.Total = row
			continue
		}
		row.ProjectID = *projectID
		row.Trend = make([]int64, PerformanceTrendBuckets)
		overview.Projects = append(overview.Projects, &row)
		byProject[row.ProjectID] = &row
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	if err := fillPerformanceTrends(ctx, pool, byProject, window, filter, extra, userIdentity); err != nil {
		return nil, err
	}
	return overview, nil
}

// fillPerformanceTrends counts each project's requests per slice of the window.
// filter refers to its arguments from $4 on, so it fits this query as well.
func fillPerformanceTrends(ctx context.Context, pool *pgxpool.Pool, byProject map[string]*ProjectPerformance, window TimeRange, filter string, extra []any, userIdentity string) error {
	if len(byProject) == 0 {
		return nil
	}
	seconds := window.To.Sub(window.From).Seconds()
	if seconds <= 0 {
		return nil
	}
	args := append([]any{float64(PerformanceTrendBuckets) / seconds, window.From, window.To}, extra...)
	rows, err := pool.Query(ctx, `
		SELECT project_id::text, bucket, COUNT(*)
		FROM (
			SELECT project_id, FLOOR(date_part('epoch', start_timestamp - $2::timestamptz) * $1::float8)::int AS bucket
			FROM transactions
			WHERE start_timestamp >= $2 AND start_timestamp < $3`+filter+`
		) slices
		GROUP BY project_id, bucket
	`, userQueryArgs(userIdentity, args)...)
	if err != nil {
		return fmt.Errorf("trend query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var projectID string
		var bucket int
		var count int64
		if err := rows.Scan(&projectID, &bucket, &count); err != nil {
			return fmt.Errorf("trend scan: %w", err)
		}
		row := byProject[projectID]
		if row == nil || bucket < 0 {
			continue
		}
		// Rounding can put the window's last instant one slice past the end.
		if bucket >= PerformanceTrendBuckets {
			bucket = PerformanceTrendBuckets - 1
		}
		row.Trend[bucket] += count
	}
	return rows.Err()
}

// Percentiles over many transactions are computed from a duration histogram:
// one row per group and duration_ms with its count c, instead of sorting every
// transaction. histogramRanks adds below, the group's rows with a shorter
// duration, and last, the group's row count minus one. partition lists the
// group's columns.
func histogramRanks(partition string) string {
	return `(SUM(c) OVER (PARTITION BY ` + partition + ` ORDER BY d) - c)::bigint AS below,
		(SUM(c) OVER (PARTITION BY ` + partition + `) - 1)::float8 AS last`
}

// histogramPercentile aggregates histogramRanks rows into the same value as
// PERCENTILE_CONT(p): it interpolates between the durations at rank
// floor(p*last) and ceil(p*last), in the same float8 arithmetic.
func histogramPercentile(p string) string {
	at := func(rank string) string {
		return `MIN(d) FILTER (WHERE below + c > ` + rank + `(` + p + `::float8 * last))`
	}
	lo, hi := at("FLOOR"), at("CEIL")
	pos := p + `::float8 * MAX(last)`
	return `COALESCE(` + lo + `::float8 + (` + pos + ` - FLOOR(` + pos + `)) * (` + hi + ` - ` + lo + `)::float8, 0)`
}

// histogramApdex aggregates histogram rows into the apdex score with a 500 ms
// threshold.
const histogramApdex = `COALESCE((
		COALESCE(SUM(c) FILTER (WHERE d <= 500), 0)::float8 +
		COALESCE(SUM(c) FILTER (WHERE d > 500 AND d <= 2000), 0)::float8 * 0.5
	) / NULLIF(SUM(c), 0)::float8, 0)`
