package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AlertRule struct {
	ID                string     `json:"id"`
	ProjectIDs        []string   `json:"project_ids"`
	Name              string     `json:"name"`
	Enabled           bool       `json:"enabled"`
	Trigger           string     `json:"trigger"`
	Threshold         *int       `json:"threshold,omitempty"`
	WindowMins        *int       `json:"window_mins,omitempty"`
	Channel           string     `json:"channel"`
	WebhookURL        *string    `json:"webhook_url,omitempty"`
	EmailTo           *string    `json:"email_to,omitempty"`
	CooldownMins      int        `json:"cooldown_mins"`
	FilterLevel       *string    `json:"filter_level,omitempty"`
	FilterEnvironment *string    `json:"filter_environment,omitempty"`
	MinOccurrences    *int       `json:"min_occurrences,omitempty"`
	FilterSearch      *string    `json:"filter_search,omitempty"`
	LastFiredAt       *time.Time `json:"last_fired_at"`
	CreatedAt         time.Time  `json:"created_at"`
}

// alertRuleQuery is the base SELECT with LEFT JOIN to pick up project associations.
// All callers must append a WHERE clause and GROUP BY ar.id.
const alertRuleQuery = `
	SELECT ar.id, ar.name, ar.enabled, ar.trigger, ar.threshold, ar.window_mins,
	       ar.channel, ar.webhook_url, ar.email_to, ar.cooldown_mins,
	       ar.filter_level, ar.filter_environment, ar.min_occurrences, ar.filter_search,
	       ar.last_fired_at, ar.created_at,
	       COALESCE(array_agg(arp.project_id::text) FILTER (WHERE arp.project_id IS NOT NULL), '{}') AS project_ids
	FROM alert_rules ar
	LEFT JOIN alert_rule_projects arp ON arp.rule_id = ar.id`

func scanAlertRule(scan func(...any) error) (*AlertRule, error) {
	var r AlertRule
	err := scan(
		&r.ID, &r.Name, &r.Enabled, &r.Trigger,
		&r.Threshold, &r.WindowMins,
		&r.Channel, &r.WebhookURL, &r.EmailTo,
		&r.CooldownMins,
		&r.FilterLevel, &r.FilterEnvironment, &r.MinOccurrences, &r.FilterSearch,
		&r.LastFiredAt, &r.CreatedAt,
		&r.ProjectIDs,
	)
	if err != nil {
		return nil, err
	}
	if r.ProjectIDs == nil {
		r.ProjectIDs = []string{}
	}
	return &r, nil
}

func CreateAlertRule(ctx context.Context, pool *pgxpool.Pool, r *AlertRule) (*AlertRule, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var created AlertRule
	err = tx.QueryRow(ctx, `
		INSERT INTO alert_rules
			(name, enabled, trigger, threshold, window_mins,
			 channel, webhook_url, email_to, cooldown_mins,
			 filter_level, filter_environment, min_occurrences, filter_search)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id, name, enabled, trigger, threshold, window_mins,
		          channel, webhook_url, email_to, cooldown_mins,
		          filter_level, filter_environment, min_occurrences, filter_search,
		          last_fired_at, created_at`,
		r.Name, r.Enabled, r.Trigger, r.Threshold, r.WindowMins,
		r.Channel, r.WebhookURL, r.EmailTo, r.CooldownMins,
		r.FilterLevel, r.FilterEnvironment, r.MinOccurrences, r.FilterSearch,
	).Scan(
		&created.ID, &created.Name, &created.Enabled, &created.Trigger,
		&created.Threshold, &created.WindowMins,
		&created.Channel, &created.WebhookURL, &created.EmailTo,
		&created.CooldownMins,
		&created.FilterLevel, &created.FilterEnvironment, &created.MinOccurrences, &created.FilterSearch,
		&created.LastFiredAt, &created.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}

	if err := setAlertRuleProjects(ctx, tx, created.ID, r.ProjectIDs); err != nil {
		return nil, err
	}
	created.ProjectIDs = normaliseProjectIDs(r.ProjectIDs)

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &created, nil
}

func GetAlertRule(ctx context.Context, pool *pgxpool.Pool, id string) (*AlertRule, error) {
	row := pool.QueryRow(ctx,
		alertRuleQuery+` WHERE ar.id = $1 GROUP BY ar.id`, id)
	r, err := scanAlertRule(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return r, nil
}

func ListAlertRules(ctx context.Context, pool *pgxpool.Pool, projectID string) ([]*AlertRule, error) {
	q := alertRuleQuery + ` GROUP BY ar.id ORDER BY ar.created_at DESC`
	var args []any
	if projectID != "" {
		args = []any{projectID}
		q = alertRuleQuery + ` WHERE EXISTS (SELECT 1 FROM alert_rule_projects WHERE rule_id = ar.id AND project_id = $1::uuid) GROUP BY ar.id ORDER BY ar.created_at DESC`
	}
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var rules []*AlertRule
	for rows.Next() {
		r, err := scanAlertRule(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// ListDueAlertRules returns all enabled rules whose cooldown has elapsed.
func ListDueAlertRules(ctx context.Context, pool *pgxpool.Pool) ([]*AlertRule, error) {
	rows, err := pool.Query(ctx,
		alertRuleQuery+`
		WHERE ar.enabled = TRUE
		  AND (ar.last_fired_at IS NULL
		       OR ar.last_fired_at + make_interval(mins => ar.cooldown_mins) <= NOW())
		GROUP BY ar.id`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var rules []*AlertRule
	for rows.Next() {
		r, err := scanAlertRule(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

// ListEnabledAlertRules returns all enabled alert rules regardless of cooldown.
// Used for one-shot notifications that bypass the normal evaluation cycle.
func ListEnabledAlertRules(ctx context.Context, pool *pgxpool.Pool) ([]*AlertRule, error) {
	rows, err := pool.Query(ctx,
		alertRuleQuery+` WHERE ar.enabled = TRUE GROUP BY ar.id`)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var rules []*AlertRule
	for rows.Next() {
		r, err := scanAlertRule(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

func UpdateAlertRule(ctx context.Context, pool *pgxpool.Pool, r *AlertRule) (*AlertRule, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	var updated AlertRule
	err = tx.QueryRow(ctx, `
		UPDATE alert_rules SET
			name=$2, enabled=$3, trigger=$4, threshold=$5, window_mins=$6,
			channel=$7, webhook_url=$8, email_to=$9, cooldown_mins=$10,
			filter_level=$11, filter_environment=$12, min_occurrences=$13,
			filter_search=$14
		WHERE id=$1
		RETURNING id, name, enabled, trigger, threshold, window_mins,
		          channel, webhook_url, email_to, cooldown_mins,
		          filter_level, filter_environment, min_occurrences, filter_search,
		          last_fired_at, created_at`,
		r.ID, r.Name, r.Enabled, r.Trigger, r.Threshold, r.WindowMins,
		r.Channel, r.WebhookURL, r.EmailTo, r.CooldownMins,
		r.FilterLevel, r.FilterEnvironment, r.MinOccurrences, r.FilterSearch,
	).Scan(
		&updated.ID, &updated.Name, &updated.Enabled, &updated.Trigger,
		&updated.Threshold, &updated.WindowMins,
		&updated.Channel, &updated.WebhookURL, &updated.EmailTo,
		&updated.CooldownMins,
		&updated.FilterLevel, &updated.FilterEnvironment, &updated.MinOccurrences, &updated.FilterSearch,
		&updated.LastFiredAt, &updated.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update: %w", err)
	}

	if err := setAlertRuleProjects(ctx, tx, updated.ID, r.ProjectIDs); err != nil {
		return nil, err
	}
	updated.ProjectIDs = normaliseProjectIDs(r.ProjectIDs)

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	return &updated, nil
}

func DeleteAlertRule(ctx context.Context, pool *pgxpool.Pool, id string) (bool, error) {
	tag, err := pool.Exec(ctx, `DELETE FROM alert_rules WHERE id = $1`, id)
	if err != nil {
		return false, fmt.Errorf("delete: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// MarkAlertFired sets last_fired_at to now. Always called even on delivery
// failure so a broken endpoint can't cause a spam loop.
func MarkAlertFired(ctx context.Context, pool *pgxpool.Pool, id string) {
	_, _ = pool.Exec(ctx, `UPDATE alert_rules SET last_fired_at = NOW() WHERE id = $1`, id)
}

// setAlertRuleProjects replaces all project associations for a rule within an
// existing transaction. Passing an empty slice makes the rule global.
func setAlertRuleProjects(ctx context.Context, tx pgx.Tx, ruleID string, projectIDs []string) error {
	if _, err := tx.Exec(ctx,
		`DELETE FROM alert_rule_projects WHERE rule_id = $1`, ruleID); err != nil {
		return fmt.Errorf("clear projects: %w", err)
	}
	for _, pid := range projectIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO alert_rule_projects (rule_id, project_id) VALUES ($1, $2)`,
			ruleID, pid); err != nil {
			return fmt.Errorf("insert project: %w", err)
		}
	}
	return nil
}

func normaliseProjectIDs(ids []string) []string {
	// Remove blank entries so callers can safely pass a filtered slice.
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			out = append(out, id)
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}
