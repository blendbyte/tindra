package storage

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AlertRule struct {
	ID         string   `json:"id"`
	ProjectIDs []string `json:"project_ids"`
	// IsGlobal is true for rules saved without projects (they cover every
	// project). A non-global rule whose projects were all deleted matches
	// nothing and is never evaluated.
	IsGlobal   bool    `json:"is_global"`
	Name       string  `json:"name"`
	Enabled    bool    `json:"enabled"`
	Trigger    string  `json:"trigger"`
	Threshold  *int    `json:"threshold,omitempty"`
	WindowMins *int    `json:"window_mins,omitempty"`
	Channel    string  `json:"channel"`
	WebhookURL *string `json:"webhook_url,omitempty"`
	EmailTo    *string `json:"email_to,omitempty"`
	// RecipientIDs are the users a push rule notifies on all their devices.
	RecipientIDs []string `json:"recipient_ids"`
	CooldownMins int      `json:"cooldown_mins"`
	FilterLevel  *string  `json:"filter_level,omitempty"`
	// FilterEnvironments limits the rule to these environments; empty
	// matches all of them.
	FilterEnvironments []string   `json:"filter_environments"`
	MinOccurrences     *int       `json:"min_occurrences,omitempty"`
	FilterSearch       *string    `json:"filter_search,omitempty"`
	LastFiredAt        *time.Time `json:"last_fired_at"`
	CreatedAt          time.Time  `json:"created_at"`
}

// alertRuleSelect is the base SELECT with LEFT JOIN to pick up project
// associations; %s is the recipient_ids column. All callers must append a
// WHERE clause and GROUP BY ar.id.
const alertRuleSelect = `
	SELECT ar.id, ar.name, ar.enabled, ar.trigger, ar.threshold, ar.window_mins,
	       ar.channel, ar.webhook_url, ar.email_to, ar.cooldown_mins,
	       ar.filter_level, ar.filter_environments, ar.min_occurrences, ar.filter_search,
	       ar.last_fired_at, ar.created_at, ar.is_global,
	       COALESCE(array_agg(arp.project_id::text) FILTER (WHERE arp.project_id IS NOT NULL), '{}') AS project_ids,
	       %s AS recipient_ids
	FROM alert_rules ar
	LEFT JOIN alert_rule_projects arp ON arp.rule_id = ar.id`

// alertRuleQuery reads rules with their push recipients, for the API.
var alertRuleQuery = fmt.Sprintf(alertRuleSelect,
	`ARRAY(SELECT rr.user_id::text FROM alert_rule_recipients rr WHERE rr.rule_id = ar.id ORDER BY rr.user_id)`)

// evaluatorRuleQuery leaves recipients out: the evaluator reads them with
// the devices when a push rule fires, and runs this every 15 seconds.
var evaluatorRuleQuery = fmt.Sprintf(alertRuleSelect, `'{}'::text[]`)

func scanAlertRule(scan func(...any) error) (*AlertRule, error) {
	var r AlertRule
	err := scan(
		&r.ID, &r.Name, &r.Enabled, &r.Trigger,
		&r.Threshold, &r.WindowMins,
		&r.Channel, &r.WebhookURL, &r.EmailTo,
		&r.CooldownMins,
		&r.FilterLevel, &r.FilterEnvironments, &r.MinOccurrences, &r.FilterSearch,
		&r.LastFiredAt, &r.CreatedAt, &r.IsGlobal,
		&r.ProjectIDs, &r.RecipientIDs,
	)
	if err != nil {
		return nil, err
	}
	if r.ProjectIDs == nil {
		r.ProjectIDs = []string{}
	}
	if r.RecipientIDs == nil {
		r.RecipientIDs = []string{}
	}
	if r.FilterEnvironments == nil {
		r.FilterEnvironments = []string{}
	}
	return &r, nil
}

// nonNilStrings keeps a nil list from reaching a NOT NULL array column as NULL.
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func CreateAlertRule(ctx context.Context, pool *pgxpool.Pool, r *AlertRule) (*AlertRule, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := lockRecipientUsers(ctx, tx, r.RecipientIDs); err != nil {
		return nil, err
	}

	var created AlertRule
	err = tx.QueryRow(ctx, `
		INSERT INTO alert_rules
			(name, enabled, trigger, threshold, window_mins,
			 channel, webhook_url, email_to, cooldown_mins,
			 filter_level, filter_environments, min_occurrences, filter_search, is_global)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
		RETURNING id, name, enabled, trigger, threshold, window_mins,
		          channel, webhook_url, email_to, cooldown_mins,
		          filter_level, filter_environments, min_occurrences, filter_search,
		          last_fired_at, created_at, is_global`,
		r.Name, r.Enabled, r.Trigger, r.Threshold, r.WindowMins,
		r.Channel, r.WebhookURL, r.EmailTo, r.CooldownMins,
		r.FilterLevel, nonNilStrings(r.FilterEnvironments), r.MinOccurrences, r.FilterSearch,
		len(normaliseProjectIDs(r.ProjectIDs)) == 0,
	).Scan(
		&created.ID, &created.Name, &created.Enabled, &created.Trigger,
		&created.Threshold, &created.WindowMins,
		&created.Channel, &created.WebhookURL, &created.EmailTo,
		&created.CooldownMins,
		&created.FilterLevel, &created.FilterEnvironments, &created.MinOccurrences, &created.FilterSearch,
		&created.LastFiredAt, &created.CreatedAt, &created.IsGlobal,
	)
	if err != nil {
		return nil, fmt.Errorf("insert: %w", err)
	}

	if err := setAlertRuleProjects(ctx, tx, created.ID, r.ProjectIDs); err != nil {
		return nil, err
	}
	created.ProjectIDs = normaliseProjectIDs(r.ProjectIDs)
	if created.RecipientIDs, err = setAlertRuleRecipients(ctx, tx, created.ID, r.RecipientIDs); err != nil {
		return nil, err
	}
	if r.Channel == "push" && r.Enabled && len(created.RecipientIDs) == 0 {
		return nil, ErrNoPushRecipients
	}
	if err := requireRecipientsSeeRule(ctx, tx, created.ID); err != nil {
		return nil, err
	}

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
// ruleHasScope excludes rules that are neither global nor scoped to any
// remaining project: their projects were deleted, so they must not fire.
const ruleHasScope = `(ar.is_global OR EXISTS (SELECT 1 FROM alert_rule_projects WHERE rule_id = ar.id))`

// RecipientIDs is not filled in.
func ListDueAlertRules(ctx context.Context, pool *pgxpool.Pool) ([]*AlertRule, error) {
	rows, err := pool.Query(ctx,
		evaluatorRuleQuery+`
		WHERE ar.enabled = TRUE
		  AND (ar.last_fired_at IS NULL
		       OR ar.last_fired_at + make_interval(mins => ar.cooldown_mins) <= NOW())
		  AND `+ruleHasScope+`
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
// RecipientIDs is not filled in.
func ListEnabledAlertRules(ctx context.Context, pool *pgxpool.Pool) ([]*AlertRule, error) {
	rows, err := pool.Query(ctx,
		evaluatorRuleQuery+` WHERE ar.enabled = TRUE AND `+ruleHasScope+` GROUP BY ar.id`)
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
	if err := lockRecipientUsers(ctx, tx, r.RecipientIDs); err != nil {
		return nil, err
	}

	var updated AlertRule
	err = tx.QueryRow(ctx, `
		UPDATE alert_rules SET
			name=$2, enabled=$3, trigger=$4, threshold=$5, window_mins=$6,
			channel=$7, webhook_url=$8, email_to=$9, cooldown_mins=$10,
			filter_level=$11, filter_environments=$12, min_occurrences=$13,
			filter_search=$14, is_global=$15
		WHERE id=$1
		RETURNING id, name, enabled, trigger, threshold, window_mins,
		          channel, webhook_url, email_to, cooldown_mins,
		          filter_level, filter_environments, min_occurrences, filter_search,
		          last_fired_at, created_at, is_global`,
		r.ID, r.Name, r.Enabled, r.Trigger, r.Threshold, r.WindowMins,
		r.Channel, r.WebhookURL, r.EmailTo, r.CooldownMins,
		r.FilterLevel, nonNilStrings(r.FilterEnvironments), r.MinOccurrences, r.FilterSearch,
		len(normaliseProjectIDs(r.ProjectIDs)) == 0,
	).Scan(
		&updated.ID, &updated.Name, &updated.Enabled, &updated.Trigger,
		&updated.Threshold, &updated.WindowMins,
		&updated.Channel, &updated.WebhookURL, &updated.EmailTo,
		&updated.CooldownMins,
		&updated.FilterLevel, &updated.FilterEnvironments, &updated.MinOccurrences, &updated.FilterSearch,
		&updated.LastFiredAt, &updated.CreatedAt, &updated.IsGlobal,
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
	if updated.RecipientIDs, err = setAlertRuleRecipients(ctx, tx, updated.ID, r.RecipientIDs); err != nil {
		return nil, err
	}
	if r.Channel == "push" && r.Enabled && len(updated.RecipientIDs) == 0 {
		return nil, ErrNoPushRecipients
	}
	if err := requireRecipientsSeeRule(ctx, tx, updated.ID); err != nil {
		return nil, err
	}

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

// MarkAlertFired sets last_fired_at to firedAt, the instant the evaluation
// counted up to. Always called even on delivery failure so a broken endpoint
// can't cause a spam loop.
func MarkAlertFired(ctx context.Context, pool *pgxpool.Pool, id string, firedAt time.Time) error {
	if _, err := pool.Exec(ctx, `UPDATE alert_rules SET last_fired_at = $2 WHERE id = $1`, id, firedAt); err != nil {
		return fmt.Errorf("mark fired: %w", err)
	}
	return nil
}

// setAlertRuleProjects replaces all project associations for a rule within an
// existing transaction. Callers set is_global alongside it.
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

// lockRecipientUsers takes the recipients' users rows before anything else
// in the transaction. Deleting a user locks that row first and then reaches
// alert_rule_recipients through its cascade, so taking the rows first here
// keeps the two in the same order and they wait for each other instead of
// deadlocking. It also keeps the users from being deleted until the
// recipients are written, so their foreign keys always hold.
func lockRecipientUsers(ctx context.Context, tx pgx.Tx, userIDs []string) error {
	ids := validUUIDs(userIDs)
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM users WHERE id = ANY($1::uuid[]) ORDER BY id FOR KEY SHARE`, ids); err != nil {
		return fmt.Errorf("lock recipients: %w", err)
	}
	return nil
}

// validUUIDs drops IDs that are not UUIDs, so a malformed one is ignored
// like an unknown one, and the rest can use the users primary key.
func validUUIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		// Only the plain form: uuid.Parse also takes "urn:uuid:..." and
		// braces, which Postgres would reject.
		if u, err := uuid.Parse(id); err == nil && u.String() == strings.ToLower(id) {
			out = append(out, id)
		}
	}
	return out
}

// ErrNoPushRecipients is returned when an enabled push rule would be saved
// without a recipient, because none of its recipient IDs is an existing
// user. A disabled one may have none, so a rule whose last recipient was
// deleted can still be paused.
var ErrNoPushRecipients = errors.New("recipient_ids must name existing users")

// ErrRecipientCannotSeeRule is returned when a recipient could not see every
// project of the rule, so the notification would leak what happens there. A
// rule that covers all projects can only notify users with access to all of them.
var ErrRecipientCannotSeeRule = errors.New("recipient_ids must name users who can see every project of the rule")

// recipientCannotSeeRule is true for a recipient u (an alias of users) of the rule
// $1 who lacks access to at least one of its projects, or to any of them for a rule
// that covers all projects.
const recipientCannotSeeRule = `(u.project_access <> 'all' AND (
	NOT EXISTS (SELECT 1 FROM alert_rule_projects WHERE rule_id = $1::uuid)
	OR EXISTS (
		SELECT 1 FROM alert_rule_projects arp WHERE arp.rule_id = $1::uuid AND NOT EXISTS (
			SELECT 1 FROM project_teams pt JOIN team_members m ON m.team_id = pt.team_id
			WHERE m.user_id = u.id AND pt.project_id = arp.project_id))))`

// recipientCannotSeeItsRule is recipientCannotSeeRule for every rule at once, with
// r an alias of alert_rule_recipients.
var recipientCannotSeeItsRule = strings.ReplaceAll(recipientCannotSeeRule, "$1::uuid", "r.rule_id")

// RecipientRemoval is one person taken off one alert rule.
type RecipientRemoval struct {
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	RuleID   string `json:"rule_id"`
	RuleName string `json:"rule_name"`
}

// PausedRule is a push rule turned off because it was left without recipients.
type PausedRule struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// AlertImpact is what a change of access does to alert rules: who stops being notified
// and which push rules are paused. Both lists are empty, never nil, when nothing is
// affected.
type AlertImpact struct {
	RemovedRecipients []RecipientRemoval `json:"removed_recipients"`
	PausedRules       []PausedRule       `json:"paused_rules"`
}

// purgeIneligibleRecipients drops the recipients who can no longer see all the
// projects of their rule, and pauses the push rules left without any recipient, as
// deleting their last recipient's account does. It runs in the transaction of
// whatever took the access away (a team's members or projects, a team's deletion, a
// user limited to their teams), so the rule list never shows someone who gets no
// notification. It returns what it did so the admin can be told: before the change
// through the preview variants of those operations, which roll the transaction back,
// and in the audit log afterwards. The pushes were already filtered when they are sent.
func purgeIneligibleRecipients(ctx context.Context, tx pgx.Tx) (*AlertImpact, error) {
	impact := &AlertImpact{RemovedRecipients: []RecipientRemoval{}, PausedRules: []PausedRule{}}
	// Same lock order as saving a rule: the rules, then (through the delete) the
	// recipients. The rules are only locked against other updates, so the evaluator
	// can still record firings for them.
	if _, err := tx.Exec(ctx, `SELECT id FROM alert_rules WHERE id IN (
			SELECT r.rule_id FROM alert_rule_recipients r JOIN users u ON u.id = r.user_id
			WHERE `+recipientCannotSeeItsRule+`)
		ORDER BY id FOR NO KEY UPDATE`); err != nil {
		return nil, fmt.Errorf("lock rules: %w", err)
	}
	removed, err := tx.Query(ctx, `
		WITH gone AS (
			DELETE FROM alert_rule_recipients r USING users u
			WHERE u.id = r.user_id AND `+recipientCannotSeeItsRule+`
			RETURNING r.user_id, u.name AS user_name, r.rule_id)
		SELECT g.user_id, g.user_name, g.rule_id, ar.name
		FROM gone g JOIN alert_rules ar ON ar.id = g.rule_id
		ORDER BY ar.name, g.user_name, g.user_id`)
	if err != nil {
		return nil, fmt.Errorf("purge recipients: %w", err)
	}
	for removed.Next() {
		var rr RecipientRemoval
		if err := removed.Scan(&rr.UserID, &rr.UserName, &rr.RuleID, &rr.RuleName); err != nil {
			removed.Close()
			return nil, fmt.Errorf("scan purged recipient: %w", err)
		}
		impact.RemovedRecipients = append(impact.RemovedRecipients, rr)
	}
	removed.Close()
	if err := removed.Err(); err != nil {
		return nil, fmt.Errorf("purge recipients: %w", err)
	}
	paused, err := tx.Query(ctx, `UPDATE alert_rules ar SET enabled = FALSE
		WHERE ar.channel = 'push' AND ar.enabled
		  AND NOT EXISTS (SELECT 1 FROM alert_rule_recipients rr WHERE rr.rule_id = ar.id)
		RETURNING ar.id, ar.name`)
	if err != nil {
		return nil, fmt.Errorf("pause push rules without recipients: %w", err)
	}
	for paused.Next() {
		var pr PausedRule
		if err := paused.Scan(&pr.ID, &pr.Name); err != nil {
			paused.Close()
			return nil, fmt.Errorf("scan paused rule: %w", err)
		}
		impact.PausedRules = append(impact.PausedRules, pr)
	}
	paused.Close()
	if err := paused.Err(); err != nil {
		return nil, fmt.Errorf("pause push rules without recipients: %w", err)
	}
	return impact, nil
}

// requireRecipientsSeeRule checks, once the rule's projects and recipients are
// stored in tx, that every recipient may see all of the rule's projects. Running it
// after both are written means a change to the projects alone re-checks the
// recipients already on the rule.
func requireRecipientsSeeRule(ctx context.Context, tx pgx.Tx, ruleID string) error {
	var blocked bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM alert_rule_recipients r JOIN users u ON u.id = r.user_id
			WHERE r.rule_id = $1::uuid AND `+recipientCannotSeeRule+`)`, ruleID).Scan(&blocked); err != nil {
		return fmt.Errorf("check recipients: %w", err)
	}
	if blocked {
		return ErrRecipientCannotSeeRule
	}
	return nil
}

// setAlertRuleRecipients replaces a rule's push recipients within an existing
// transaction. IDs that are not existing users are dropped, so a stale or
// malformed ID cannot fail the save. It returns the stored IDs, sorted.
func setAlertRuleRecipients(ctx context.Context, tx pgx.Tx, ruleID string, userIDs []string) ([]string, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM alert_rule_recipients WHERE rule_id = $1`, ruleID); err != nil {
		return nil, fmt.Errorf("clear recipients: %w", err)
	}
	stored := []string{}
	if len(userIDs) == 0 {
		return stored, nil
	}
	rows, err := tx.Query(ctx, `
		INSERT INTO alert_rule_recipients (rule_id, user_id)
		SELECT $1, u.id FROM users u WHERE u.id = ANY($2::uuid[])
		ON CONFLICT DO NOTHING
		RETURNING user_id::text`, ruleID, validUUIDs(userIDs))
	if err != nil {
		return nil, fmt.Errorf("insert recipients: %w", err)
	}
	stored, err = pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("insert recipients: %w", err)
	}
	slices.Sort(stored)
	return stored, nil
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
