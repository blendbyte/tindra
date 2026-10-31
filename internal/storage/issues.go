package storage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Issue struct {
	ID               string    `json:"id"`
	ProjectID        string    `json:"project_id"`
	Fingerprint      string    `json:"fingerprint"`
	Title            string    `json:"title"`
	Level            string    `json:"level"`
	Kind             string    `json:"kind"`
	FirstSeen        time.Time `json:"first_seen"`
	LastSeen         time.Time `json:"last_seen"`
	EventCount       int64     `json:"event_count"`
	Status           string    `json:"status"`
	AssigneeID       *string   `json:"assignee_id"`
	AssigneeEmail    *string   `json:"assignee_email,omitempty"`
	FingerprintCount int       `json:"fingerprint_count,omitempty"`
	Sparkline        []int     `json:"sparkline,omitempty"`
	Environment      *string   `json:"environment"`
	TopFrames        []string  `json:"top_frames,omitempty"` // populated on demand (alerts, not API responses)
	// Alert-only enrichment fields; not serialised in API responses.
	AlertMessage     string     `json:"-"`
	AlertReqURL      string     `json:"-"`
	AlertReqMethod   string     `json:"-"`
	AlertOccurredAt  *time.Time `json:"-"`
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
	Status         string
	Level          string
	Levels         []string // non-empty = level = ANY(...); takes precedence over Level
	Kind           string   // "error" | "n1_query" | "" (all)
	Environment    string
	AssigneeID     string
	TagKey         string
	TagValue       string
	Title          string   // ILIKE substring match on title
	ProjectIDs     []string // nil or empty = all projects
	CursorTime     *time.Time
	CursorID       *string
	Limit          int
	CreatedSince   *time.Time // created_at > CreatedSince (server time the issue was created)
	SinceLast      *time.Time // last_seen > SinceLast
	SinceRegressed *time.Time // regressed_at > SinceRegressed
	UserIdentity   string     // issues with at least one event from this app user
	NewSince       *time.Time // with Status "new": first_seen >= NewSince
}

// issueUserCountSQL counts the distinct users (id, username or email) behind
// the events of issue i, from events_issue_user_identity without reading payloads.
const issueUserCountSQL = `(SELECT count(DISTINCT user_identity) FROM events
		WHERE issue_id = i.id AND user_identity IS NOT NULL)`

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
	return upsertIssue(ctx, pool, projectID, fingerprint, title, level, kind, environment, release, ts)
}

// issueDB permits the same issue update to run within an event transaction.
// Begin creates a savepoint when the caller is already using a pgx transaction.
type issueDB interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Begin(context.Context) (pgx.Tx, error)
}

func upsertIssue(ctx context.Context, db issueDB, projectID, fingerprint, title, level, kind, environment, release string, ts time.Time) (*Issue, bool, bool, error) {
	for range 2 {
		iss, created, regressed, err := upsertIssueOnce(ctx, db, projectID, fingerprint, title, level, kind, environment, release, ts)
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

func upsertIssueOnce(ctx context.Context, pool issueDB, projectID, fingerprint, title, level, kind, environment, release string, ts time.Time) (*Issue, bool, bool, error) {
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
			-- prev is joined in FROM so it is read (and the row locked) before the
			-- update. FOR UPDATE makes a concurrent upsert wait and then read the
			-- already-regressed row, so only one of them reports the regression.
			WITH prev AS (
				SELECT status AS prev_status, snooze_ended_at AS prev_snooze_ended_at
				FROM issues WHERE id = $1 FOR UPDATE
			)
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
			                          WHEN status = 'open' AND snooze_ended_at IS NOT NULL THEN 'regressed'
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
			                        END,
			    regressed_at       = CASE
			                          WHEN status = 'resolved' THEN NOW()
			                          WHEN status = 'open' AND snooze_ended_at IS NOT NULL THEN NOW()
			                          WHEN status = 'ignored' AND ignore_until IS NOT NULL AND ignore_until < now() THEN NOW()
			                          WHEN status = 'ignored' AND ignore_count_limit IS NOT NULL AND (ignore_count + 1) >= ignore_count_limit THEN NOW()
			                          ELSE regressed_at
			                        END,
			    -- A snooze that ended is only remembered until the next event.
			    snooze_ended_at    = NULL
			FROM prev
			WHERE id = $1
			RETURNING `+issueSelectCols+`,
			          status = 'regressed' AND (
			              prev_status IN ('resolved', 'ignored')
			              OR (prev_status = 'open' AND prev_snooze_ended_at IS NOT NULL))
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

// LockIssueMembership coordinates event grouping/retention with merge and
// unmerge. Readers may run concurrently; membership moves take the exclusive
// transaction lock before reading statistics or moving rows.
func LockIssueMembership(ctx context.Context, tx pgx.Tx, exclusive bool) error {
	query := "SELECT pg_advisory_xact_lock_shared(1953066596, 1)"
	if exclusive {
		query = "SELECT pg_advisory_xact_lock(1953066596, 1)"
	}
	_, err := tx.Exec(ctx, query)
	return err
}

// GroupEvent increments an issue and links its event in one transaction.
// A nil issue means the event was deleted or another worker already grouped it.
// When legacyFingerprint is set and an issue already exists under it, the event
// joins that issue, so a change of grouping scheme does not duplicate issues.
func GroupEvent(ctx context.Context, pool *pgxpool.Pool, eventID, fingerprint, legacyFingerprint, title, level, environment, release string) (*Issue, bool, bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, false, false, fmt.Errorf("begin grouping: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := LockIssueMembership(ctx, tx, false); err != nil {
		return nil, false, false, fmt.Errorf("lock issue membership: %w", err)
	}

	var projectID string
	var timestamp time.Time
	var existingIssue *string
	err = tx.QueryRow(ctx, `SELECT project_id, timestamp, issue_id FROM events WHERE id = $1 FOR UPDATE`, eventID).Scan(&projectID, &timestamp, &existingIssue)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, false, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("lock event: %w", err)
	}
	if existingIssue != nil {
		return nil, false, false, nil
	}
	if legacyFingerprint != "" && legacyFingerprint != fingerprint {
		var legacyExists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM issue_fingerprints WHERE project_id = $1 AND fingerprint = $2)
		`, projectID, legacyFingerprint).Scan(&legacyExists); err != nil {
			return nil, false, false, fmt.Errorf("lookup legacy fingerprint: %w", err)
		}
		if legacyExists {
			fingerprint = legacyFingerprint
		}
	}
	issue, created, regressed, err := upsertIssue(ctx, tx, projectID, fingerprint, title, level, "error", environment, release, timestamp)
	if err != nil {
		return nil, false, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE events SET fingerprint = $1, issue_id = $2 WHERE id = $3`, fingerprint, issue.ID, eventID); err != nil {
		return nil, false, false, fmt.Errorf("link grouped event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, false, fmt.Errorf("commit grouping: %w", err)
	}
	return issue, created, regressed, nil
}

// ListIssues returns the issues matching every field of filter, most recently
// seen first. Unlike ListAllIssues it skips the per-issue user count, for
// callers that only need the issue rows (alert payloads, the project API).
func ListIssues(ctx context.Context, pool *pgxpool.Pool, filter IssueFilter) ([]*Issue, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	q, args := addCommonFilters(`SELECT `+issueSelectCols+` FROM issues WHERE TRUE`, nil, filter)
	if filter.CursorTime != nil && filter.CursorID != nil {
		n := len(args) + 1
		args = append(args, *filter.CursorTime, *filter.CursorID)
		q += fmt.Sprintf(" AND (last_seen < $%d OR (last_seen = $%d AND id < $%d::uuid))", n, n, n+1)
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY last_seen DESC, id DESC LIMIT $%d", len(args))

	rows, err := pool.Query(ctx, q, userQueryArgs(filter.UserIdentity, args)...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()
	var issues []*Issue
	for rows.Next() {
		var iss Issue
		if err := scanIssue(rows, &iss); err != nil {
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
	case "new":
		// Open issues first seen at or after NewSince; without it, every open one.
		q += " AND status = 'open'"
		if filter.NewSince != nil {
			args = append(args, *filter.NewSince)
			q += fmt.Sprintf(" AND first_seen >= $%d", len(args))
		}
	case "":
		// no filter
	default:
		args = append(args, filter.Status)
		q += fmt.Sprintf(" AND status = $%d", len(args))
	}
	if len(filter.Levels) > 0 {
		args = append(args, filter.Levels)
		q += fmt.Sprintf(" AND level = ANY($%d::text[])", len(args))
	} else if filter.Level != "" {
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
		args = append(args, likeContains(filter.Title))
		q += fmt.Sprintf(" AND title ILIKE $%d", len(args))
	}
	if filter.TagKey != "" {
		// A correlated EXISTS probes event_tags_issue_key (issue_id, key, value)
		// per candidate issue and stops at the page limit, instead of collecting
		// every matching tag row across all projects first. event_tags has no
		// "id" column, so the bare id refers to the outer issue.
		args = append(args, filter.TagKey)
		cond := fmt.Sprintf("t.key = $%d", len(args))
		if filter.TagValue != "" {
			args = append(args, filter.TagValue)
			cond += fmt.Sprintf(" AND t.value = $%d", len(args))
		}
		q += " AND EXISTS (SELECT 1 FROM event_tags t WHERE t.issue_id = id AND " + cond + ")"
	}
	projIdx := 0
	if len(filter.ProjectIDs) > 0 {
		args = append(args, filter.ProjectIDs)
		projIdx = len(args)
		q += fmt.Sprintf(" AND project_id = ANY($%d::uuid[])", projIdx)
	}
	if filter.UserIdentity != "" {
		args = append(args, filter.UserIdentity)
		sub := fmt.Sprintf(` AND id IN (
			SELECT issue_id FROM events
			WHERE app_user_identity_hash(user_identity) = app_user_identity_hash($%[1]d::text) AND user_identity = $%[1]d AND issue_id IS NOT NULL`, len(args))
		if projIdx > 0 {
			sub += fmt.Sprintf(` AND project_id = ANY($%d::uuid[])`, projIdx)
		}
		q += sub + `)`
	}
	if filter.CreatedSince != nil {
		args = append(args, *filter.CreatedSince)
		q += fmt.Sprintf(" AND created_at > $%d", len(args))
	}
	if filter.SinceLast != nil {
		args = append(args, *filter.SinceLast)
		q += fmt.Sprintf(" AND last_seen > $%d", len(args))
	}
	if filter.SinceRegressed != nil {
		args = append(args, *filter.SinceRegressed)
		q += fmt.Sprintf(" AND regressed_at > $%d", len(args))
	}
	return q, args
}

func CountAllIssues(ctx context.Context, pool *pgxpool.Pool, filter IssueFilter) (int, error) {
	if bounds, ok := InvestigationRange(ctx); ok {
		return countWindowIssues(ctx, pool, filter, bounds)
	}
	q := `SELECT COUNT(*) FROM issues WHERE TRUE`
	q, args := addCommonFilters(q, []any{}, filter)
	var n int
	if err := pool.QueryRow(ctx, q, userQueryArgs(filter.UserIdentity, args)...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count issues: %w", err)
	}
	return n, nil
}

func ListAllIssues(ctx context.Context, pool *pgxpool.Pool, filter IssueFilter) ([]*Issue, error) {
	if bounds, ok := InvestigationRange(ctx); ok {
		return listWindowIssues(ctx, pool, filter, bounds)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	// Group first so PostgreSQL can hash identities instead of sorting every event.
	// COUNT(identity) excludes the NULL group, matching COUNT(DISTINCT ...).
	q := `SELECT i.id, i.project_id, i.fingerprint, i.title, i.level, i.kind,
		i.first_seen, i.last_seen, i.event_count, i.status,
		i.assignee_id, i.environment,
		i.ignore_until, i.ignore_count_limit, i.ignore_count,
		` + issueUserCountSQL + ` AS user_count
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

	rows, err := pool.Query(ctx, q, userQueryArgs(filter.UserIdentity, args)...)
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

// DashboardIssue contains the fields used by the dashboard's hot-issue list.
type DashboardIssue struct {
	ID         string `json:"id"`
	ProjectID  string `json:"project_id"`
	Title      string `json:"title"`
	Level      string `json:"level"`
	EventCount int64  `json:"event_count"`
	Sparkline  []int  `json:"sparkline,omitempty"`
}

// ListDashboardIssues preserves the dashboard's stable client-side selection:
// choose the five highest counts within the 50 most recently seen open issues.
func ListDashboardIssues(ctx context.Context, pool *pgxpool.Pool, projectIDs []string) ([]DashboardIssue, error) {
	page, args := addCommonFilters(`SELECT id,project_id,title,level,event_count,last_seen FROM issues WHERE TRUE`, nil, IssueFilter{Status: "open", ProjectIDs: projectIDs})
	page += " ORDER BY last_seen DESC,id DESC LIMIT 50"
	rows, err := pool.Query(ctx, "WITH recent AS MATERIALIZED ("+page+`) SELECT id,project_id,title,level,event_count
 FROM recent ORDER BY event_count DESC,last_seen DESC,id DESC LIMIT 5`, args...)
	if err != nil {
		return nil, fmt.Errorf("dashboard issues: %w", err)
	}
	defer rows.Close()
	result := []DashboardIssue{}
	for rows.Next() {
		var issue DashboardIssue
		if err := rows.Scan(&issue.ID, &issue.ProjectID, &issue.Title, &issue.Level, &issue.EventCount); err != nil {
			return nil, fmt.Errorf("scan dashboard issue: %w", err)
		}
		result = append(result, issue)
	}
	return result, rows.Err()
}

// AttentionIssue is an issue the dashboard briefing asks someone to look at:
// one that came back after being resolved or ignored, or one that first
// appeared inside the investigation window.
type AttentionIssue struct {
	ID           string     `json:"id"`
	ProjectID    string     `json:"project_id"`
	Title        string     `json:"title"`
	Level        string     `json:"level"`
	Status       string     `json:"status"`
	EventCount   int64      `json:"event_count"`
	UserCount    int64      `json:"user_count"`
	FirstSeen    time.Time  `json:"first_seen"`
	LastSeen     time.Time  `json:"last_seen"`
	RegressedAt  *time.Time `json:"regressed_at"`
	FirstRelease *string    `json:"first_release"`
	Assignee     *string    `json:"assignee"`
	Sparkline    []int      `json:"sparkline,omitempty"`
}

// AttentionIssues groups the briefing's issue rows: regressed issues seen in
// the window, and issues first seen in it that nobody has resolved or ignored.
type AttentionIssues struct {
	Regressed      []AttentionIssue `json:"regressed"`
	RegressedTotal int              `json:"regressed_total"`
	New            []AttentionIssue `json:"new"`
	NewTotal       int              `json:"new_total"`
	// Counts of the totals above that happened after the caller's last visit;
	// only set when a last visit is given.
	UnseenRegressed *int `json:"unseen_regressed,omitempty"`
	UnseenNew       *int `json:"unseen_new,omitempty"`
}

const attentionIssueCols = `id, project_id, title, level, status, event_count, ` + issueUserCountSQL + `,
	first_seen, last_seen, regressed_at, first_release,
	(SELECT COALESCE(NULLIF(u.name, ''), u.email) FROM users u WHERE u.id = i.assignee_id)`

// ListAttentionIssues returns up to limit regressed issues seen since from,
// most recently regressed first, and up to limit new issues first seen in
// [from, to), busiest first. Totals count every match, not only the returned
// rows. When lastVisit is set, the unseen counts say how many of each
// happened after it.
func ListAttentionIssues(ctx context.Context, pool *pgxpool.Pool, filter IssueFilter, from, to time.Time, lastVisit *time.Time, limit int) (AttentionIssues, error) {
	result := AttentionIssues{Regressed: []AttentionIssue{}, New: []AttentionIssue{}}
	filter.Status = ""

	// A regression nobody has seen in the window is not happening now, and the
	// issue list for the same window would not show it either.
	regressed, args := addCommonFilters(` FROM issues i WHERE status = 'regressed'`, nil, filter)
	args = append(args, from)
	regressed += fmt.Sprintf(` AND last_seen >= $%d`, len(args))
	rows, err := queryAttentionIssues(ctx, pool, regressed, "regressed_at", `regressed_at DESC NULLS LAST, last_seen DESC, id DESC`,
		filter.UserIdentity, args, lastVisit, limit, &result.RegressedTotal, &result.UnseenRegressed)
	if err != nil {
		return result, fmt.Errorf("regressed issues: %w", err)
	}
	result.Regressed = rows

	fresh, args := addCommonFilters(` FROM issues i WHERE status = 'open'`, nil, filter)
	args = append(args, from, to)
	fresh += fmt.Sprintf(` AND first_seen >= $%d AND first_seen < $%d`, len(args)-1, len(args))
	rows, err = queryAttentionIssues(ctx, pool, fresh, "first_seen", `event_count DESC, first_seen DESC, id DESC`,
		filter.UserIdentity, args, lastVisit, limit, &result.NewTotal, &result.UnseenNew)
	if err != nil {
		return result, fmt.Errorf("new issues: %w", err)
	}
	result.New = rows
	return result, nil
}

// queryAttentionIssues reads the first limit rows of a FROM/WHERE clause in
// one query. Window counts carry the total and, with a last visit, how many
// rows have their onset column after it; the inner LIMIT keeps the per-row
// user count to the returned rows.
func queryAttentionIssues(ctx context.Context, pool *pgxpool.Pool, clause, onset, order, userIdentity string, args []any, lastVisit *time.Time, limit int, total *int, unseen **int) ([]AttentionIssue, error) {
	args = append([]any{}, args...)
	unseenCol := "0"
	if lastVisit != nil {
		args = append(args, *lastVisit)
		unseenCol = fmt.Sprintf(`COUNT(*) FILTER (WHERE %s > $%d) OVER ()`, onset, len(args))
	}
	args = append(args, limit)
	q := `SELECT ` + attentionIssueCols + `, total, unseen FROM (SELECT *, COUNT(*) OVER () AS total, ` + unseenCol + ` AS unseen` +
		clause + fmt.Sprintf(` ORDER BY %s LIMIT $%d) i ORDER BY %s`, order, len(args), order)
	rows, err := pool.Query(ctx, q, userQueryArgs(userIdentity, args)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AttentionIssue{}
	var n int
	for rows.Next() {
		var issue AttentionIssue
		if err := rows.Scan(&issue.ID, &issue.ProjectID, &issue.Title, &issue.Level, &issue.Status, &issue.EventCount, &issue.UserCount,
			&issue.FirstSeen, &issue.LastSeen, &issue.RegressedAt, &issue.FirstRelease, &issue.Assignee, total, &n); err != nil {
			return nil, err
		}
		result = append(result, issue)
	}
	if lastVisit != nil {
		*unseen = &n
	}
	return result, rows.Err()
}

// IssueMetadata contains only the issue-row fields needed by scope checks,
// history updates and histogram bounds. It is not an enriched API response.
type IssueMetadata struct {
	ID        string
	ProjectID string
	FirstSeen time.Time
	Status    string
}

func GetIssueMetadata(ctx context.Context, pool *pgxpool.Pool, id string) (*IssueMetadata, error) {
	var issue IssueMetadata
	err := pool.QueryRow(ctx, `SELECT id, project_id, first_seen, status FROM issues WHERE id = $1`, id).
		Scan(&issue.ID, &issue.ProjectID, &issue.FirstSeen, &issue.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query issue metadata: %w", err)
	}
	return &issue, nil
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
		LEFT JOIN LATERAL (SELECT `+issueUserCountSQL+` AS cnt) u ON TRUE
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
		    ignore_count       = 0,
		    snooze_ended_at    = NULL
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
	if err := LockIssueMembership(ctx, tx, true); err != nil {
		return nil, fmt.Errorf("lock issue membership: %w", err)
	}

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

	// Move everything else that belongs to the merged issues. These tables
	// cascade on delete, so anything left behind would be lost with them.
	for _, table := range []string{"event_tags", "perf_events", "issue_comments", "issue_history"} {
		if _, err := tx.Exec(ctx, `UPDATE `+table+` SET issue_id = $1 WHERE issue_id = ANY($2::uuid[])`, primaryID, mergeIDs); err != nil {
			return nil, fmt.Errorf("reassign %s: %w", table, err)
		}
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

// Unmerge errors.
var (
	ErrUnmergeAll         = errors.New("cannot unmerge all fingerprints - at least one must remain")
	ErrUnknownFingerprint = errors.New("fingerprint does not belong to this issue")
)

// eventTitleSQL derives an issue title from an event payload the way the
// grouper does: innermost exception "Type: value", else the message (string or
// {formatted, message} object), else logentry.
const eventTitleSQL = `COALESCE(
	NULLIF(concat_ws(': ',
		NULLIF(payload->'exception'->'values'->-1->>'type', ''),
		NULLIF(payload->'exception'->'values'->-1->>'value', '')), ''),
	CASE jsonb_typeof(payload->'message')
		WHEN 'string' THEN NULLIF(payload->>'message', '')
		WHEN 'object' THEN COALESCE(NULLIF(payload->'message'->>'formatted', ''), NULLIF(payload->'message'->>'message', ''))
	END,
	NULLIF(payload->'logentry'->>'formatted', ''),
	NULLIF(payload->'logentry'->>'message', ''),
	'Unknown error')`

// UnmergeFingerprints splits the given fingerprints out of issueID into new individual issues.
// Each fingerprint gets its own issue with stats derived from its events.
// At least one fingerprint must remain on the original issue.
func UnmergeFingerprints(ctx context.Context, pool *pgxpool.Pool, issueID string, fingerprints []string) ([]*Issue, error) {
	fingerprints = slices.Compact(slices.Sorted(slices.Values(fingerprints)))
	if len(fingerprints) == 0 {
		return nil, nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := LockIssueMembership(ctx, tx, true); err != nil {
		return nil, fmt.Errorf("lock issue membership: %w", err)
	}

	// Every fingerprint must be on this issue, and at least one must stay.
	var totalFPs, matched int
	if err := tx.QueryRow(ctx, `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE fingerprint = ANY($2::text[]))
		FROM issue_fingerprints WHERE issue_id = $1
	`, issueID, fingerprints).Scan(&totalFPs, &matched); err != nil {
		return nil, fmt.Errorf("count fingerprints: %w", err)
	}
	if matched != len(fingerprints) {
		return nil, ErrUnknownFingerprint
	}
	if totalFPs <= len(fingerprints) {
		return nil, ErrUnmergeAll
	}

	// The new issues inherit the source's project, kind and environment.
	var projectID, kind string
	var environment *string
	if err := tx.QueryRow(ctx, `SELECT project_id, kind, environment FROM issues WHERE id = $1`, issueID).Scan(&projectID, &kind, &environment); err != nil {
		return nil, fmt.Errorf("get source issue: %w", err)
	}

	var newIssues []*Issue
	var totalUnmergedCount int64
	for _, fp := range fingerprints {
		// Derive title, level, first release and time bounds from the latest
		// and earliest events with this fingerprint.
		title, level := "Unknown error", "error"
		var firstRelease *string
		firstSeen, lastSeen := time.Now().UTC(), time.Now().UTC()
		var count int64
		err := tx.QueryRow(ctx, `
			SELECT `+eventTitleSQL+`,
				COALESCE(payload->>'level', 'error'),
				MIN(timestamp) OVER (), MAX(timestamp) OVER (), COUNT(*) OVER (),
				FIRST_VALUE(release) OVER (ORDER BY timestamp ASC ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING)
			FROM events
			WHERE issue_id = $1 AND fingerprint = $2
			ORDER BY received_at DESC
			LIMIT 1
		`, issueID, fp).Scan(&title, &level, &firstSeen, &lastSeen, &count, &firstRelease)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("derive stats for %s: %w", fp, err)
		}
		// No events (all past retention): the fingerprint still moves, with an
		// empty issue rather than invented counts.

		var newIss Issue
		err = scanIssue(tx.QueryRow(ctx, `
			INSERT INTO issues (project_id, fingerprint, title, level, kind, environment, first_release, first_seen, last_seen, event_count)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			RETURNING `+issueSelectCols,
			projectID, fp, truncRunesStorage(title, 200), level, kind, environment, firstRelease, firstSeen, lastSeen, count), &newIss)
		if err != nil {
			return nil, fmt.Errorf("insert new issue: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			UPDATE issue_fingerprints SET issue_id = $1
			WHERE issue_id = $2 AND fingerprint = $3
		`, newIss.ID, issueID, fp); err != nil {
			return nil, fmt.Errorf("reassign fingerprint: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE events SET issue_id = $1
			WHERE issue_id = $2 AND fingerprint = $3
		`, newIss.ID, issueID, fp); err != nil {
			return nil, fmt.Errorf("reassign events: %w", err)
		}
		// Tags travel with their events.
		if _, err := tx.Exec(ctx, `
			UPDATE event_tags SET issue_id = $1
			WHERE issue_id = $2 AND event_id IN (SELECT id FROM events WHERE issue_id = $1)
		`, newIss.ID, issueID); err != nil {
			return nil, fmt.Errorf("reassign tags: %w", err)
		}

		totalUnmergedCount += count
		newIssues = append(newIssues, &newIss)
	}

	// Subtract the unmerged event counts from the original issue and refresh time bounds.
	if _, err := tx.Exec(ctx, `
		UPDATE issues SET
			event_count = GREATEST(event_count - $2, 0),
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

// truncRunesStorage caps s at max characters, marking the cut with an ellipsis
// like issue titles from the grouper.
func truncRunesStorage(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "\u2026"
}

// BulkUpdateIssueStatus sets status on the given issues, limited to projectIDs
// when non-nil. It returns the IDs that were actually updated so callers only
// record history for issues they changed.
func BulkUpdateIssueStatus(ctx context.Context, pool *pgxpool.Pool, ids []string, status string, opts *IgnoreOptions, projectIDs []string) ([]string, error) {
	if !validStatuses[status] {
		return nil, fmt.Errorf("invalid status %q", status)
	}
	var ignoreUntil *time.Time
	var ignoreCountLimit *int
	if status == "ignored" && opts != nil {
		ignoreUntil = opts.Until
		ignoreCountLimit = opts.CountLimit
	}
	// A nil projectIDs slice is sent as NULL, which disables the scope filter.
	rows, err := pool.Query(ctx, `
		UPDATE issues
		SET status             = $1,
		    ignore_until       = $3,
		    ignore_count_limit = $4,
		    ignore_count       = 0,
		    snooze_ended_at    = NULL
		WHERE id = ANY($2::uuid[])
		  AND ($5::uuid[] IS NULL OR project_id = ANY($5::uuid[]))
		RETURNING id
	`, status, ids, ignoreUntil, ignoreCountLimit, projectIDs)
	if err != nil {
		return nil, fmt.Errorf("bulk update: %w", err)
	}
	updated, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("bulk update: %w", err)
	}
	return updated, nil
}

// AutoResolvePerformanceIssues resolves all open/regressed performance issues
// (kind != 'error') that have not been seen within olderThan. Returns the
// issues that were just resolved so callers can send notifications.
func AutoResolvePerformanceIssues(ctx context.Context, pool *pgxpool.Pool, olderThan time.Duration) ([]*Issue, error) {
	rows, err := pool.Query(ctx, `
		UPDATE issues
		SET status = 'resolved', snooze_ended_at = NULL
		WHERE kind != 'error'
		  AND status IN ('open', 'regressed')
		  AND last_seen < NOW() - make_interval(secs => $1::float8)
		RETURNING `+issueSelectCols,
		olderThan.Seconds())
	if err != nil {
		return nil, fmt.Errorf("auto-resolve performance issues: %w", err)
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

// ExpireIgnoredIssues ends time-based snoozes that have run out. The issue
// reopens quietly (no regression yet) and records snooze_ended_at, so the next
// event marks it regressed and regression alerts fire on recurrence.
func ExpireIgnoredIssues(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE issues
		SET status             = 'open',
		    snooze_ended_at    = now(),
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
