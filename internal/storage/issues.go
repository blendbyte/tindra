package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Issue struct {
	ID               string     `json:"id"`
	ProjectID        string     `json:"project_id"`
	Fingerprint      string     `json:"fingerprint"`
	Title            string     `json:"title"`
	Level            string     `json:"level"`
	Kind             string     `json:"kind"`
	FirstSeen        time.Time  `json:"first_seen"`
	LastSeen         time.Time  `json:"last_seen"`
	EventCount       int64      `json:"event_count"`
	Status           string     `json:"status"`
	AssigneeID       *string    `json:"assignee_id"`
	AssigneeEmail    *string    `json:"assignee_email,omitempty"`
	FingerprintCount int        `json:"fingerprint_count,omitempty"`
	Sparkline        []int      `json:"sparkline,omitempty"`
	Environment      *string    `json:"environment"`
	TopFrames        []string   `json:"top_frames,omitempty"` // populated on demand (alerts, not API responses)
	IgnoreUntil      *time.Time `json:"ignore_until"`
	IgnoreCountLimit *int       `json:"ignore_count_limit"`
	IgnoreCount      int        `json:"ignore_count"`
	Release          *string    `json:"release"`
	ReleaseID        *string    `json:"release_id"`
	UserCount        int64      `json:"user_count"`
	FirstRelease     *string    `json:"first_release"`
}

// IgnoreOptions carries the optional limit when setting status to "ignored".
// Both fields are nil for a permanent ignore.
type IgnoreOptions struct {
	Until      *time.Time // expire at this absolute time
	CountLimit *int       // expire after this many new occurrences
}

type IssueFilter struct {
	Status      string
	Level       string
	Kind        string // "error" | "n1_query" | "" (all)
	Environment string
	AssigneeID  string
	TagKey      string
	TagValue    string
	Title       string   // ILIKE substring match on title
	ProjectIDs  []string // nil or empty = all projects
	CursorTime  *time.Time
	CursorID    *string
	Limit       int
	Since       *time.Time // first_seen > Since
	SinceLast   *time.Time // last_seen > SinceLast
}

// issueSelectCols is the canonical SELECT column list for the issues table.
const issueSelectCols = `id, project_id, fingerprint, title, level, kind, first_seen, last_seen, event_count, status, assignee_id, environment, ignore_until, ignore_count_limit, ignore_count, first_release`

func scanIssue(row pgx.Row, iss *Issue) error {
	return row.Scan(
		&iss.ID, &iss.ProjectID, &iss.Fingerprint, &iss.Title,
		&iss.Level, &iss.Kind, &iss.FirstSeen, &iss.LastSeen, &iss.EventCount, &iss.Status,
		&iss.AssigneeID, &iss.Environment,
		&iss.IgnoreUntil, &iss.IgnoreCountLimit, &iss.IgnoreCount, &iss.FirstRelease,
	)
}

// UpsertIssue finds an existing issue by fingerprint (via issue_fingerprints) or creates one.
// Returns the issue, whether it was newly created, and whether it just regressed.
func UpsertIssue(ctx context.Context, pool *pgxpool.Pool, projectID, fingerprint, title, level, kind, environment, release string, ts time.Time) (*Issue, bool, bool, error) {
	for range 2 {
		iss, created, regressed, err := upsertIssueOnce(ctx, pool, projectID, fingerprint, title, level, kind, environment, release, ts)
		if err == nil {
			return iss, created, regressed, nil
		}
		// Retry once on fingerprint duplicate key - another goroutine won the race.
		if !errors.Is(err, errFingerprintConflict) {
			return nil, false, false, err
		}
	}
	return nil, false, false, fmt.Errorf("upsert issue: persistent fingerprint conflict")
}

var errFingerprintConflict = errors.New("fingerprint conflict")

func upsertIssueOnce(ctx context.Context, pool *pgxpool.Pool, projectID, fingerprint, title, level, kind, environment, release string, ts time.Time) (*Issue, bool, bool, error) {
	// Fast path: fingerprint already mapped to an issue.
	var existingID string
	err := pool.QueryRow(ctx, `
		SELECT issue_id FROM issue_fingerprints
		WHERE project_id = $1 AND fingerprint = $2
	`, projectID, fingerprint).Scan(&existingID)

	if err == nil {
		// Update existing issue. Use a CTE to capture the pre-update status so we
		// can detect regression transitions without an extra round-trip.
		// For ignored issues:
		//   - time-based: if ignore_until has passed, transition to regressed and clear ignore fields
		//   - count-based: increment ignore_count; if count reaches limit, transition to regressed
		var iss Issue
		var wasRegressed bool
		err = pool.QueryRow(ctx, `
			WITH prev AS (SELECT status FROM issues WHERE id = $1)
			UPDATE issues
			SET event_count        = event_count + 1,
			    last_seen          = GREATEST(last_seen, $2),
			    level              = $3,
			    environment        = COALESCE(NULLIF($4, ''), environment),
			    ignore_count       = CASE
			                          WHEN status = 'ignored' AND ignore_count_limit IS NOT NULL THEN ignore_count + 1
			                          ELSE ignore_count
			                        END,
			    status             = CASE
			                          WHEN status = 'resolved' THEN 'regressed'
			                          WHEN status = 'ignored' AND ignore_until IS NOT NULL AND ignore_until < now() THEN 'regressed'
			                          WHEN status = 'ignored' AND ignore_count_limit IS NOT NULL AND (ignore_count + 1) >= ignore_count_limit THEN 'regressed'
			                          ELSE status
			                        END,
			    ignore_until       = CASE
			                          WHEN status = 'ignored' AND (
			                            (ignore_until IS NOT NULL AND ignore_until < now())
			                            OR (ignore_count_limit IS NOT NULL AND (ignore_count + 1) >= ignore_count_limit)
			                          ) THEN NULL
			                          ELSE ignore_until
			                        END,
			    ignore_count_limit = CASE
			                          WHEN status = 'ignored' AND (
			                            (ignore_until IS NOT NULL AND ignore_until < now())
			                            OR (ignore_count_limit IS NOT NULL AND (ignore_count + 1) >= ignore_count_limit)
			                          ) THEN NULL
			                          ELSE ignore_count_limit
			                        END
			WHERE id = $1
			RETURNING `+issueSelectCols+`,
			          (SELECT status FROM prev) IN ('resolved', 'ignored') AND status = 'regressed'
		`, existingID, ts, level, environment).Scan(
			&iss.ID, &iss.ProjectID, &iss.Fingerprint, &iss.Title,
			&iss.Level, &iss.Kind, &iss.FirstSeen, &iss.LastSeen, &iss.EventCount, &iss.Status,
			&iss.AssigneeID, &iss.Environment,
			&iss.IgnoreUntil, &iss.IgnoreCountLimit, &iss.IgnoreCount, &iss.FirstRelease,
			&wasRegressed,
		)
		if err != nil {
			return nil, false, false, fmt.Errorf("update issue: %w", err)
		}
		return &iss, false, wasRegressed, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, false, fmt.Errorf("lookup fingerprint: %w", err)
	}

	// New fingerprint: create issue + fingerprint row in a transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, false, false, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	var iss Issue
	err = scanIssue(tx.QueryRow(ctx, `
		INSERT INTO issues (project_id, fingerprint, title, level, kind, first_seen, last_seen, event_count, environment, first_release)
		VALUES ($1, $2, $3, $4, $5, $6, $6, 1, NULLIF($7, ''), NULLIF($8, ''))
		RETURNING `+issueSelectCols,
		projectID, fingerprint, title, level, kind, ts, environment, release), &iss)
	if err != nil {
		return nil, false, false, fmt.Errorf("insert issue: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO issue_fingerprints (project_id, fingerprint, issue_id)
		VALUES ($1, $2, $3)
	`, projectID, fingerprint, iss.ID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, false, false, errFingerprintConflict
		}
		return nil, false, false, fmt.Errorf("insert fingerprint: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, false, false, fmt.Errorf("commit: %w", err)
	}
	return &iss, true, false, nil
}

// LinkEventToIssue sets fingerprint and issue_id on an event row.
func LinkEventToIssue(ctx context.Context, pool *pgxpool.Pool, eventID, issueID, fingerprint string) error {
	_, err := pool.Exec(ctx, `
		UPDATE events SET fingerprint = $1, issue_id = $2 WHERE id = $3
	`, fingerprint, issueID, eventID)
	if err != nil {
		return fmt.Errorf("link event: %w", err)
	}
	return nil
}

func ListIssues(ctx context.Context, pool *pgxpool.Pool, projectID string, filter IssueFilter) ([]*Issue, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var args []any
	q := `SELECT ` + issueSelectCols + ` FROM issues WHERE TRUE`
	if projectID != "" {
		args = append(args, projectID)
		q += fmt.Sprintf(" AND project_id = $%d", len(args))
	}

	switch filter.Status {
	case "open":
		q += " AND status IN ('open', 'regressed')"
	case "":
		// no filter
	default:
		args = append(args, filter.Status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if filter.Level != "" {
		args = append(args, filter.Level)
		q += fmt.Sprintf(" AND level = $%d", len(args))
	}
	if filter.Kind != "" {
		args = append(args, filter.Kind)
		q += fmt.Sprintf(" AND kind = $%d", len(args))
	}
	if filter.Since != nil {
		args = append(args, *filter.Since)
		q += fmt.Sprintf(" AND first_seen > $%d", len(args))
	}
	if filter.SinceLast != nil {
		args = append(args, *filter.SinceLast)
		q += fmt.Sprintf(" AND last_seen > $%d", len(args))
	}
	if filter.CursorTime != nil && filter.CursorID != nil {
		n := len(args) + 1
		args = append(args, *filter.CursorTime, *filter.CursorID)
		q += fmt.Sprintf(
			" AND (last_seen < $%d OR (last_seen = $%d AND id < $%d::uuid))",
			n, n, n+1,
		)
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY last_seen DESC, id DESC LIMIT $%d", len(args))

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var issues []*Issue
	for rows.Next() {
		var iss Issue
		if err := rows.Scan(
			&iss.ID, &iss.ProjectID, &iss.Fingerprint, &iss.Title,
			&iss.Level, &iss.Kind, &iss.FirstSeen, &iss.LastSeen, &iss.EventCount, &iss.Status,
			&iss.AssigneeID, &iss.Environment,
			&iss.IgnoreUntil, &iss.IgnoreCountLimit, &iss.IgnoreCount, &iss.FirstRelease,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		issues = append(issues, &iss)
	}
	return issues, rows.Err()
}

func addCommonFilters(q string, args []any, filter IssueFilter) (string, []any) {
	switch filter.Status {
	case "open":
		q += " AND status IN ('open', 'regressed')"
	case "":
		// no filter
	default:
		args = append(args, filter.Status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if filter.Level != "" {
		args = append(args, filter.Level)
		q += fmt.Sprintf(" AND level = $%d", len(args))
	}
	if filter.Kind != "" {
		args = append(args, filter.Kind)
		q += fmt.Sprintf(" AND kind = $%d", len(args))
	}
	if filter.Environment != "" {
		args = append(args, filter.Environment)
		q += fmt.Sprintf(" AND environment = $%d", len(args))
	}
	if filter.AssigneeID != "" {
		args = append(args, filter.AssigneeID)
		q += fmt.Sprintf(" AND assignee_id = $%d::uuid", len(args))
	}
	if filter.Title != "" {
		args = append(args, "%"+filter.Title+"%")
		q += fmt.Sprintf(" AND title ILIKE $%d", len(args))
	}
	if filter.TagKey != "" {
		args = append(args, filter.TagKey)
		keyIdx := len(args)
		if filter.TagValue != "" {
			args = append(args, filter.TagValue)
			q += fmt.Sprintf(
				" AND id IN (SELECT DISTINCT issue_id FROM event_tags WHERE key = $%d AND value = $%d AND issue_id IS NOT NULL)",
				keyIdx, len(args),
			)
		} else {
			q += fmt.Sprintf(
				" AND id IN (SELECT DISTINCT issue_id FROM event_tags WHERE key = $%d AND issue_id IS NOT NULL)",
				keyIdx,
			)
		}
	}
	if len(filter.ProjectIDs) > 0 {
		args = append(args, filter.ProjectIDs)
		q += fmt.Sprintf(" AND project_id = ANY($%d::uuid[])", len(args))
	}
	if filter.SinceLast != nil {
		args = append(args, *filter.SinceLast)
		q += fmt.Sprintf(" AND last_seen > $%d", len(args))
	}
	return q, args
}

func CountAllIssues(ctx context.Context, pool *pgxpool.Pool, filter IssueFilter) (int, error) {
	q := `SELECT COUNT(*) FROM issues WHERE TRUE`
	q, args := addCommonFilters(q, []any{}, filter)
	var n int
	if err := pool.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count issues: %w", err)
	}
	return n, nil
}

func ListAllIssues(ctx context.Context, pool *pgxpool.Pool, filter IssueFilter) ([]*Issue, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	q := `SELECT i.id, i.project_id, i.fingerprint, i.title, i.level, i.kind,
		i.first_seen, i.last_seen, i.event_count, i.status,
		i.assignee_id, i.environment,
		i.ignore_until, i.ignore_count_limit, i.ignore_count,
		(SELECT COUNT(DISTINCT payload->'user'->>'id') FROM events
		 WHERE issue_id = i.id AND payload->'user'->>'id' IS NOT NULL) AS user_count
	FROM issues i WHERE TRUE`
	q, args := addCommonFilters(q, []any{}, filter)

	if filter.CursorTime != nil && filter.CursorID != nil {
		n := len(args) + 1
		args = append(args, *filter.CursorTime, *filter.CursorID)
		q += fmt.Sprintf(
			" AND (i.last_seen < $%d OR (i.last_seen = $%d AND i.id < $%d::uuid))",
			n, n, n+1,
		)
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY i.last_seen DESC, i.id DESC LIMIT $%d", len(args))

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var issues []*Issue
	for rows.Next() {
		var iss Issue
		if err := rows.Scan(
			&iss.ID, &iss.ProjectID, &iss.Fingerprint, &iss.Title,
			&iss.Level, &iss.Kind, &iss.FirstSeen, &iss.LastSeen, &iss.EventCount, &iss.Status,
			&iss.AssigneeID, &iss.Environment,
			&iss.IgnoreUntil, &iss.IgnoreCountLimit, &iss.IgnoreCount,
			&iss.UserCount,
		); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		issues = append(issues, &iss)
	}
	return issues, rows.Err()
}

func GetIssue(ctx context.Context, pool *pgxpool.Pool, id string) (*Issue, error) {
	var iss Issue
	err := pool.QueryRow(ctx, `
		SELECT i.id, i.project_id, i.fingerprint, i.title, i.level, i.kind,
		       i.first_seen, i.last_seen, i.event_count, i.status,
		       i.assignee_id, i.environment,
		       i.ignore_until, i.ignore_count_limit, i.ignore_count,
		       e.release, r.id,
		       u.cnt
		FROM issues i
		LEFT JOIN LATERAL (
		    SELECT release FROM events
		    WHERE issue_id = i.id AND release IS NOT NULL
		    ORDER BY received_at DESC LIMIT 1
		) e ON TRUE
		LEFT JOIN releases r ON r.project_id = i.project_id AND r.version = e.release
		LEFT JOIN LATERAL (
		    SELECT COUNT(DISTINCT payload->'user'->>'id') AS cnt
		    FROM events
		    WHERE issue_id = i.id AND payload->'user'->>'id' IS NOT NULL
		) u ON TRUE
		WHERE i.id = $1
	`, id).Scan(
		&iss.ID, &iss.ProjectID, &iss.Fingerprint, &iss.Title,
		&iss.Level, &iss.Kind, &iss.FirstSeen, &iss.LastSeen, &iss.EventCount, &iss.Status,
		&iss.AssigneeID, &iss.Environment,
		&iss.IgnoreUntil, &iss.IgnoreCountLimit, &iss.IgnoreCount,
		&iss.Release, &iss.ReleaseID,
		&iss.UserCount,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return &iss, nil
}

var validStatuses = map[string]bool{"open": true, "resolved": true, "ignored": true}

func UpdateIssueStatus(ctx context.Context, pool *pgxpool.Pool, projectID, id, status string, opts *IgnoreOptions) (*Issue, error) {
	if !validStatuses[status] {
		return nil, fmt.Errorf("invalid status %q", status)
	}
	var ignoreUntil *time.Time
	var ignoreCountLimit *int
	if status == "ignored" && opts != nil {
		ignoreUntil = opts.Until
		ignoreCountLimit = opts.CountLimit
	}
	var iss Issue
	err := scanIssue(pool.QueryRow(ctx, `
		UPDATE issues
		SET status             = $1,
		    ignore_until       = $4,
		    ignore_count_limit = $5,
		    ignore_count       = 0
		WHERE id = $2 AND project_id = $3
		RETURNING `+issueSelectCols,
		status, id, projectID, ignoreUntil, ignoreCountLimit), &iss)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}
	return &iss, nil
}

// GetIssueFingerprints returns all fingerprints grouped into an issue.
func GetIssueFingerprints(ctx context.Context, pool *pgxpool.Pool, issueID string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT fingerprint FROM issue_fingerprints WHERE issue_id = $1 ORDER BY fingerprint
	`, issueID)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var fps []string
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		fps = append(fps, fp)
	}
	return fps, rows.Err()
}

// MergeIssues reassigns all fingerprints and events from mergeIDs into primaryID,
// aggregates stats, and deletes the now-empty merged issues.
// All issues must belong to the same project.
func MergeIssues(ctx context.Context, pool *pgxpool.Pool, primaryID string, mergeIDs []string) (*Issue, error) {
	if len(mergeIDs) == 0 {
		return GetIssue(ctx, pool, primaryID)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Reassign all fingerprint mappings from the merged issues to the primary.
	if _, err := tx.Exec(ctx, `
		UPDATE issue_fingerprints SET issue_id = $1
		WHERE issue_id = ANY($2::uuid[])
	`, primaryID, mergeIDs); err != nil {
		return nil, fmt.Errorf("reassign fingerprints: %w", err)
	}

	// Reassign all events from the merged issues to the primary.
	if _, err := tx.Exec(ctx, `
		UPDATE events SET issue_id = $1
		WHERE issue_id = ANY($2::uuid[])
	`, primaryID, mergeIDs); err != nil {
		return nil, fmt.Errorf("reassign events: %w", err)
	}

	// Aggregate stats from the merged issues into the primary.
	if _, err := tx.Exec(ctx, `
		UPDATE issues dst SET
			event_count = dst.event_count + merged.total,
			first_seen  = LEAST(dst.first_seen, merged.min_first),
			last_seen   = GREATEST(dst.last_seen, merged.max_last)
		FROM (
			SELECT SUM(event_count) AS total, MIN(first_seen) AS min_first, MAX(last_seen) AS max_last
			FROM issues WHERE id = ANY($1::uuid[])
		) merged
		WHERE dst.id = $2
	`, mergeIDs, primaryID); err != nil {
		return nil, fmt.Errorf("aggregate stats: %w", err)
	}

	// Delete the now-empty merged issues.
	if _, err := tx.Exec(ctx, `
		DELETE FROM issues WHERE id = ANY($1::uuid[])
	`, mergeIDs); err != nil {
		return nil, fmt.Errorf("delete merged: %w", err)
	}

	var iss Issue
	err = scanIssue(tx.QueryRow(ctx, `
		SELECT `+issueSelectCols+` FROM issues WHERE id = $1
	`, primaryID), &iss)
	if err != nil {
		return nil, fmt.Errorf("fetch primary: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &iss, nil
}

// UnmergeFingerprints splits the given fingerprints out of issueID into new individual issues.
// Each fingerprint gets its own issue with stats derived from its events.
// At least one fingerprint must remain on the original issue.
func UnmergeFingerprints(ctx context.Context, pool *pgxpool.Pool, issueID string, fingerprints []string) ([]*Issue, error) {
	if len(fingerprints) == 0 {
		return nil, nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Guard: issue must have more fingerprints than we're removing.
	var totalFPs int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*) FROM issue_fingerprints WHERE issue_id = $1
	`, issueID).Scan(&totalFPs); err != nil {
		return nil, fmt.Errorf("count fingerprints: %w", err)
	}
	if totalFPs <= len(fingerprints) {
		return nil, fmt.Errorf("cannot unmerge all fingerprints - at least one must remain")
	}

	var newIssues []*Issue
	var totalUnmergedCount int64

	for _, fp := range fingerprints {
		// Derive title, level, and time bounds from the events with this fingerprint.
		var title, level string
		var firstSeen, lastSeen time.Time
		var count int64

		err := tx.QueryRow(ctx, `
			SELECT
				COALESCE(payload->'exception'->'values'->0->>'type', payload->>'message', 'Unknown Error'),
				COALESCE(payload->>'level', 'error'),
				MIN(timestamp) OVER (),
				MAX(timestamp) OVER (),
				COUNT(*) OVER ()
			FROM events
			WHERE issue_id = $1 AND fingerprint = $2
			ORDER BY received_at DESC
			LIMIT 1
		`, issueID, fp).Scan(&title, &level, &firstSeen, &lastSeen, &count)
		if errors.Is(err, pgx.ErrNoRows) {
			// No events for this fingerprint (shouldn't happen in practice).
			title, level = fp, "error"
			firstSeen, lastSeen = time.Now(), time.Now()
		} else if err != nil {
			return nil, fmt.Errorf("derive stats for %s: %w", fp, err)
		}

		// Get the project_id for this issue (needed for the new issue row).
		var projectID string
		if err := tx.QueryRow(ctx, `SELECT project_id FROM issues WHERE id = $1`, issueID).Scan(&projectID); err != nil {
			return nil, fmt.Errorf("get project id: %w", err)
		}

		// Create the new issue.
		var newIss Issue
		err = scanIssue(tx.QueryRow(ctx, `
			INSERT INTO issues (project_id, fingerprint, title, level, kind, first_seen, last_seen, event_count)
			VALUES ($1, $2, $3, $4, 'error', $5, $6, $7)
			RETURNING `+issueSelectCols,
			projectID, fp, title, level, firstSeen, lastSeen, count), &newIss)
		if err != nil {
			return nil, fmt.Errorf("insert new issue: %w", err)
		}

		// Reassign the fingerprint mapping.
		if _, err := tx.Exec(ctx, `
			UPDATE issue_fingerprints SET issue_id = $1
			WHERE issue_id = $2 AND fingerprint = $3
		`, newIss.ID, issueID, fp); err != nil {
			return nil, fmt.Errorf("reassign fingerprint: %w", err)
		}

		// Reassign the events.
		if _, err := tx.Exec(ctx, `
			UPDATE events SET issue_id = $1
			WHERE issue_id = $2 AND fingerprint = $3
		`, newIss.ID, issueID, fp); err != nil {
			return nil, fmt.Errorf("reassign events: %w", err)
		}

		totalUnmergedCount += count
		newIssues = append(newIssues, &newIss)
	}

	// Subtract the unmerged event counts from the original issue and refresh time bounds.
	if _, err := tx.Exec(ctx, `
		UPDATE issues SET
			event_count = event_count - $2,
			first_seen  = COALESCE((SELECT MIN(timestamp) FROM events WHERE issue_id = $1), first_seen),
			last_seen   = COALESCE((SELECT MAX(timestamp) FROM events WHERE issue_id = $1), last_seen)
		WHERE id = $1
	`, issueID, totalUnmergedCount); err != nil {
		return nil, fmt.Errorf("update original stats: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return newIssues, nil
}

func BulkUpdateIssueStatus(ctx context.Context, pool *pgxpool.Pool, ids []string, status string, opts *IgnoreOptions, projectIDs []string) (int64, error) {
	if !validStatuses[status] {
		return 0, fmt.Errorf("invalid status %q", status)
	}
	var ignoreUntil *time.Time
	var ignoreCountLimit *int
	if status == "ignored" && opts != nil {
		ignoreUntil = opts.Until
		ignoreCountLimit = opts.CountLimit
	}
	var tag interface{ RowsAffected() int64 }
	var err error
	if len(projectIDs) > 0 {
		tag, err = pool.Exec(ctx, `
			UPDATE issues
			SET status             = $1,
			    ignore_until       = $3,
			    ignore_count_limit = $4,
			    ignore_count       = 0
			WHERE id = ANY($2::uuid[])
			  AND project_id = ANY($5::uuid[])
		`, status, ids, ignoreUntil, ignoreCountLimit, projectIDs)
	} else {
		tag, err = pool.Exec(ctx, `
			UPDATE issues
			SET status             = $1,
			    ignore_until       = $3,
			    ignore_count_limit = $4,
			    ignore_count       = 0
			WHERE id = ANY($2::uuid[])
		`, status, ids, ignoreUntil, ignoreCountLimit)
	}
	if err != nil {
		return 0, fmt.Errorf("bulk update: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ExpireIgnoredIssues transitions any time-limited ignored issues past their expiry to regressed.
// Intended to be called by a background goroutine every ~60 seconds.
func ExpireIgnoredIssues(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE issues
		SET status             = 'regressed',
		    ignore_until       = NULL,
		    ignore_count_limit = NULL,
		    ignore_count       = 0
		WHERE status = 'ignored'
		  AND ignore_until IS NOT NULL
		  AND ignore_until < now()
	`)
	if err != nil {
		return 0, fmt.Errorf("expire ignored: %w", err)
	}
	return tag.RowsAffected(), nil
}

func UpdateIssueAssignee(ctx context.Context, pool *pgxpool.Pool, id string, assigneeID *string) (*Issue, error) {
	var iss Issue
	err := scanIssue(pool.QueryRow(ctx, `
		UPDATE issues SET assignee_id = $1 WHERE id = $2
		RETURNING `+issueSelectCols,
		assigneeID, id), &iss)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update assignee: %w", err)
	}
	return &iss, nil
}

const sparklineDays = 14

// GetIssueSparklines returns a 14-day daily event count histogram for each of the given issue IDs.
// The slice is ordered oldest→newest; index 0 = 13 days ago, index 13 = today.
// Missing issue IDs get an all-zero slice. Requires the events_issue_timestamp index.
func GetIssueSparklines(ctx context.Context, pool *pgxpool.Pool, issueIDs []string) (map[string][]int, error) {
	result := make(map[string][]int, len(issueIDs))
	if len(issueIDs) == 0 {
		return result, nil
	}
	for _, id := range issueIDs {
		result[id] = make([]int, sparklineDays)
	}

	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	start := today.AddDate(0, 0, -(sparklineDays - 1))

	rows, err := pool.Query(ctx, `
		SELECT issue_id::text,
		       date_trunc('day', timestamp AT TIME ZONE 'UTC') AS day,
		       COUNT(*)::int
		FROM events
		WHERE issue_id = ANY($1::uuid[])
		  AND timestamp >= $2
		GROUP BY issue_id, day
	`, issueIDs, start)
	if err != nil {
		return nil, fmt.Errorf("query sparklines: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var issueID string
		var day time.Time
		var count int
		if err := rows.Scan(&issueID, &day, &count); err != nil {
			return nil, fmt.Errorf("scan sparkline: %w", err)
		}
		daysAgo := int(today.Sub(day.UTC()) / (24 * time.Hour))
		if idx := sparklineDays - 1 - daysAgo; idx >= 0 && idx < sparklineDays {
			result[issueID][idx] = count
		}
	}
	return result, rows.Err()
}
