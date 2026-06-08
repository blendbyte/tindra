package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
)

// Evaluator checks alert rules every 15 seconds and fires deliveries.
type Evaluator struct {
	pool      *pgxpool.Pool
	client    *http.Client
	email     EmailSender // nil when EMAIL_PROVIDER is not configured
	publicURL string
}

func NewEvaluator(pool *pgxpool.Pool, email EmailSender, publicURL string, allowPrivateIPs bool) *Evaluator {
	return &Evaluator{
		pool:      pool,
		client:    NewWebhookClient(allowPrivateIPs),
		email:     email,
		publicURL: publicURL,
	}
}

func (e *Evaluator) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			e.evaluate(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (e *Evaluator) evaluate(ctx context.Context) {
	rules, err := storage.ListDueAlertRules(ctx, e.pool)
	if err != nil {
		slog.Error("list due alert rules", "err", err)
		return
	}
	for _, rule := range rules {
		met, details, err := e.conditionMet(ctx, rule)
		if err != nil {
			slog.Error("condition check", "rule", rule.ID, "err", err)
			continue
		}
		if !met {
			continue
		}
		e.fire(ctx, rule, details)
	}
}

// levelsAtOrAbove returns all severity levels >= minLevel.
// Order (most to least severe): fatal, error, warning, info, debug.
// "performance" is a separate category (N+1 and other perf issues) and
// is never included in error-severity queries; it must be selected explicitly.
func levelsAtOrAbove(minLevel string) []string {
	if minLevel == "performance" {
		return []string{"performance"}
	}
	all := []string{"fatal", "error", "warning", "info", "debug"}
	for i, l := range all {
		if l == minLevel {
			return all[:i+1]
		}
	}
	return all
}

// appendIssueFilters extends a WHERE clause + args slice with the optional
// noise-reduction filters from the rule.
func appendIssueFilters(rule *storage.AlertRule, where string, args []any) (string, []any) {
	if rule.FilterLevel != nil {
		args = append(args, levelsAtOrAbove(*rule.FilterLevel))
		// Level filter applies only to error-kind issues; performance issues
		// (N+1, etc.) are a separate category and always pass through.
		where += fmt.Sprintf(" AND (kind != 'error' OR level = ANY($%d::text[]))", len(args))
	}
	if rule.FilterEnvironment != nil {
		args = append(args, *rule.FilterEnvironment)
		where += fmt.Sprintf(" AND environment = $%d", len(args))
	}
	if rule.MinOccurrences != nil {
		args = append(args, *rule.MinOccurrences)
		where += fmt.Sprintf(" AND event_count >= $%d", len(args))
	}
	return where, args
}

// conditionMet evaluates whether the rule's trigger condition is currently
// satisfied. Returns true + a details map used in the delivery payload.
func (e *Evaluator) conditionMet(ctx context.Context, rule *storage.AlertRule) (bool, map[string]any, error) {
	switch rule.Trigger {
	case "new_issue":
		since := rule.CreatedAt
		if rule.LastFiredAt != nil {
			since = *rule.LastFiredAt
		}
		var clauses []string
		var args []any
		if len(rule.ProjectIDs) > 0 {
			args = append(args, rule.ProjectIDs)
			clauses = append(clauses, fmt.Sprintf("project_id = ANY($%d::uuid[])", len(args)))
		}
		args = append(args, since)
		clauses = append(clauses, fmt.Sprintf("first_seen > $%d", len(args)))
		where, args := appendIssueFilters(rule, strings.Join(clauses, " AND "), args)
		var count int
		if err := e.pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM issues WHERE "+where, args...,
		).Scan(&count); err != nil {
			return false, nil, fmt.Errorf("new_issue count: %w", err)
		}
		if count == 0 {
			return false, nil, nil
		}
		return true, map[string]any{"new_issue_count": count}, nil

	case "regressed":
		since := rule.CreatedAt
		if rule.LastFiredAt != nil {
			since = *rule.LastFiredAt
		}
		var clauses []string
		var args []any
		if len(rule.ProjectIDs) > 0 {
			args = append(args, rule.ProjectIDs)
			clauses = append(clauses, fmt.Sprintf("project_id = ANY($%d::uuid[])", len(args)))
		}
		args = append(args, since)
		clauses = append(clauses, fmt.Sprintf("regressed_at > $%d", len(args)))
		where, args := appendIssueFilters(rule, strings.Join(clauses, " AND "), args)
		var count int
		if err := e.pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM issues WHERE "+where, args...,
		).Scan(&count); err != nil {
			return false, nil, fmt.Errorf("regressed count: %w", err)
		}
		if count == 0 {
			return false, nil, nil
		}
		return true, map[string]any{"regressed_count": count}, nil

	case "new_or_regressed":
		since := rule.CreatedAt
		if rule.LastFiredAt != nil {
			since = *rule.LastFiredAt
		}
		var newArgs, regArgs []any
		var newClauses, regClauses []string

		if len(rule.ProjectIDs) > 0 {
			newArgs = append(newArgs, rule.ProjectIDs)
			newClauses = append(newClauses, fmt.Sprintf("project_id = ANY($%d::uuid[])", len(newArgs)))
			regArgs = append(regArgs, rule.ProjectIDs)
			regClauses = append(regClauses, fmt.Sprintf("project_id = ANY($%d::uuid[])", len(regArgs)))
		}
		newArgs = append(newArgs, since)
		newClauses = append(newClauses, fmt.Sprintf("first_seen > $%d", len(newArgs)))
		regArgs = append(regArgs, since)
		regClauses = append(regClauses, fmt.Sprintf("regressed_at > $%d", len(regArgs)))

		newWhere, newArgs := appendIssueFilters(rule, strings.Join(newClauses, " AND "), newArgs)
		regWhere, regArgs := appendIssueFilters(rule, strings.Join(regClauses, " AND "), regArgs)

		var newCount, regressedCount int
		if err := e.pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM issues WHERE "+newWhere, newArgs...,
		).Scan(&newCount); err != nil {
			return false, nil, fmt.Errorf("new_or_regressed new count: %w", err)
		}
		if err := e.pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM issues WHERE "+regWhere, regArgs...,
		).Scan(&regressedCount); err != nil {
			return false, nil, fmt.Errorf("new_or_regressed regressed count: %w", err)
		}
		if newCount == 0 && regressedCount == 0 {
			return false, nil, nil
		}
		return true, map[string]any{
			"new_issue_count": newCount,
			"regressed_count": regressedCount,
		}, nil

	case "event_count":
		var clauses []string
		var args []any
		if len(rule.ProjectIDs) > 0 {
			args = append(args, rule.ProjectIDs)
			clauses = append(clauses, fmt.Sprintf("project_id = ANY($%d::uuid[])", len(args)))
		}
		args = append(args, *rule.WindowMins)
		clauses = append(clauses, fmt.Sprintf("received_at > NOW() - make_interval(mins => $%d::int)", len(args)))
		where := strings.Join(clauses, " AND ")
		if rule.FilterLevel != nil {
			args = append(args, levelsAtOrAbove(*rule.FilterLevel))
			where += fmt.Sprintf(" AND level = ANY($%d::text[])", len(args))
		}
		if rule.FilterEnvironment != nil {
			args = append(args, *rule.FilterEnvironment)
			where += fmt.Sprintf(" AND environment = $%d", len(args))
		}
		var count int
		if err := e.pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM events WHERE "+where, args...,
		).Scan(&count); err != nil {
			return false, nil, fmt.Errorf("event_count: %w", err)
		}
		if count < *rule.Threshold {
			return false, nil, nil
		}
		return true, map[string]any{
			"event_count": count,
			"threshold":   *rule.Threshold,
			"window_mins": *rule.WindowMins,
		}, nil

	case "cron_missed":
		var clauses []string
		var args []any
		if len(rule.ProjectIDs) > 0 {
			args = append(args, rule.ProjectIDs)
			clauses = append(clauses, fmt.Sprintf("project_id = ANY($%d::uuid[])", len(args)))
		}
		clauses = append(clauses,
			"status = 'active'",
			"next_expected_at IS NOT NULL",
			"next_expected_at + make_interval(secs => grace_period_secs) < NOW()",
		)
		where := strings.Join(clauses, " AND ")
		var count int
		if err := e.pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM cron_monitors WHERE "+where, args...,
		).Scan(&count); err != nil {
			return false, nil, fmt.Errorf("cron_missed count: %w", err)
		}
		if count == 0 {
			return false, nil, nil
		}
		return true, map[string]any{"missed_count": count}, nil

	case "cron_error":
		since := rule.CreatedAt
		if rule.LastFiredAt != nil {
			since = *rule.LastFiredAt
		}
		var clauses []string
		var args []any
		if len(rule.ProjectIDs) > 0 {
			args = append(args, rule.ProjectIDs)
			clauses = append(clauses, fmt.Sprintf("m.project_id = ANY($%d::uuid[])", len(args)))
		}
		clauses = append(clauses, "m.status = 'active'", "ci.status = 'error'")
		args = append(args, since)
		clauses = append(clauses, fmt.Sprintf("ci.received_at > $%d", len(args)))
		where := strings.Join(clauses, " AND ")
		var count int
		if err := e.pool.QueryRow(ctx,
			"SELECT COUNT(*) FROM cron_checkins ci JOIN cron_monitors m ON m.id = ci.monitor_id WHERE "+where, args...,
		).Scan(&count); err != nil {
			return false, nil, fmt.Errorf("cron_error count: %w", err)
		}
		if count == 0 {
			return false, nil, nil
		}
		return true, map[string]any{"error_count": count}, nil
	}
	return false, nil, nil
}

// AlertPayload is the JSON body sent to webhook endpoints and used to build
// the email body.
type AlertPayload struct {
	RuleID       string                 `json:"rule_id"`
	RuleName     string                 `json:"rule_name"`
	ProjectID    string                 `json:"project_id"`
	ProjectName  string                 `json:"project_name,omitempty"`
	ProjectNames map[string]string      `json:"project_names,omitempty"`
	Trigger      string                 `json:"trigger"`
	FiredAt      time.Time              `json:"fired_at"`
	Details      map[string]any         `json:"details"`
	Issues       []*storage.Issue       `json:"issues,omitempty"`
	Monitors     []*storage.CronMonitor `json:"monitors,omitempty"`
}

// firstProjectID returns the first project ID for scoped rules, or "" for global.
func firstProjectID(rule *storage.AlertRule) string {
	if len(rule.ProjectIDs) > 0 {
		return rule.ProjectIDs[0]
	}
	return ""
}

func (e *Evaluator) enrichPayload(ctx context.Context, payload *AlertPayload, rule *storage.AlertRule) {
	projID := firstProjectID(rule)
	if projID != "" {
		_ = e.pool.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, projID).Scan(&payload.ProjectName)
	}

	filterLevel := ""
	if rule.FilterLevel != nil {
		filterLevel = *rule.FilterLevel
	}
	filterEnv := ""
	if rule.FilterEnvironment != nil {
		filterEnv = *rule.FilterEnvironment
	}

	switch rule.Trigger {
	case "new_issue":
		since := rule.CreatedAt
		if rule.LastFiredAt != nil {
			since = *rule.LastFiredAt
		}
		issues, err := storage.ListIssues(ctx, e.pool, projID, storage.IssueFilter{
			Limit:       5,
			Since:       &since,
			Level:       filterLevel,
			Environment: filterEnv,
		})
		if err == nil {
			payload.Issues = issues
		}
	case "regressed":
		since := rule.CreatedAt
		if rule.LastFiredAt != nil {
			since = *rule.LastFiredAt
		}
		issues, err := storage.ListIssues(ctx, e.pool, projID, storage.IssueFilter{
			Status:         "regressed",
			SinceRegressed: &since,
			Limit:          5,
			Level:          filterLevel,
			Environment:    filterEnv,
		})
		if err == nil {
			payload.Issues = issues
		}
	case "new_or_regressed":
		since := rule.CreatedAt
		if rule.LastFiredAt != nil {
			since = *rule.LastFiredAt
		}
		newIssues, _ := storage.ListIssues(ctx, e.pool, projID, storage.IssueFilter{
			Since:       &since,
			Limit:       5,
			Level:       filterLevel,
			Environment: filterEnv,
		})
		regIssues, _ := storage.ListIssues(ctx, e.pool, projID, storage.IssueFilter{
			Status:         "regressed",
			SinceRegressed: &since,
			Limit:          5,
			Level:          filterLevel,
			Environment:    filterEnv,
		})
		seen := map[string]bool{}
		for _, iss := range append(newIssues, regIssues...) {
			if !seen[iss.ID] && len(payload.Issues) < 5 {
				seen[iss.ID] = true
				payload.Issues = append(payload.Issues, iss)
			}
		}
	case "cron_missed":
		monitors, err := storage.ListOverdueMonitors(ctx, e.pool, rule.ProjectIDs)
		if err == nil && len(monitors) > 5 {
			monitors = monitors[:5]
		}
		if err == nil {
			payload.Monitors = monitors
		}
	case "cron_error":
		since := rule.CreatedAt
		if rule.LastFiredAt != nil {
			since = *rule.LastFiredAt
		}
		monitors, err := storage.ListMonitorsWithRecentErrors(ctx, e.pool, rule.ProjectIDs, since)
		if err == nil && len(monitors) > 5 {
			monitors = monitors[:5]
		}
		if err == nil {
			payload.Monitors = monitors
		}
	}

	for _, iss := range payload.Issues {
		evd := storage.GetAlertEventData(ctx, e.pool, iss.ID, 3)
		iss.TopFrames = evd.TopFrames
		iss.AlertMessage = evd.Message
		iss.AlertReqURL = evd.RequestURL
		iss.AlertReqMethod = evd.RequestMethod
		iss.AlertOccurredAt = evd.OccurredAt
	}

	if len(payload.Issues) > 0 {
		seen := map[string]bool{}
		ids := make([]string, 0, len(payload.Issues))
		for _, iss := range payload.Issues {
			if iss.ProjectID != "" && !seen[iss.ProjectID] {
				seen[iss.ProjectID] = true
				ids = append(ids, iss.ProjectID)
			}
		}
		rows, err := e.pool.Query(ctx, `SELECT id, name FROM projects WHERE id = ANY($1)`, ids)
		if err == nil {
			payload.ProjectNames = make(map[string]string, len(ids))
			for rows.Next() {
				var id, name string
				if rows.Scan(&id, &name) == nil {
					payload.ProjectNames[id] = name
				}
			}
			rows.Close()
		}
	}
}

func (e *Evaluator) fire(ctx context.Context, rule *storage.AlertRule, details map[string]any) {
	payload := AlertPayload{
		RuleID:    rule.ID,
		RuleName:  rule.Name,
		ProjectID: firstProjectID(rule),
		Trigger:   rule.Trigger,
		FiredAt:   time.Now().UTC(),
		Details:   details,
	}
	e.enrichPayload(ctx, &payload, rule)

	var deliveryErr error
	switch rule.Channel {
	case "webhook":
		deliveryErr = e.fireWebhook(ctx, rule, payload)
	case "slack":
		deliveryErr = e.fireSlack(ctx, rule, payload)
	case "discord":
		deliveryErr = e.fireDiscord(ctx, rule, payload)
	case "email":
		deliveryErr = e.fireEmail(ctx, rule, payload)
	}
	if deliveryErr != nil {
		slog.Error("alert delivery failed", "rule", rule.ID, "channel", rule.Channel, "err", deliveryErr)
	}

	// Update last_fired_at regardless of delivery outcome - a broken endpoint
	// must not cause a flood of retries within the same cooldown window.
	storage.MarkAlertFired(ctx, e.pool, rule.ID)
}

// NotifyAutoResolved fires all enabled alert rules with an "issue_auto_resolved"
// payload for the given issues. It does not update last_fired_at so it does not
// interfere with the regular alert evaluation cycle.
func (e *Evaluator) NotifyAutoResolved(ctx context.Context, issues []*storage.Issue) {
	if len(issues) == 0 {
		return
	}

	byProject := make(map[string][]*storage.Issue)
	for _, iss := range issues {
		byProject[iss.ProjectID] = append(byProject[iss.ProjectID], iss)
	}

	rules, err := storage.ListEnabledAlertRules(ctx, e.pool)
	if err != nil {
		slog.Error("notify auto-resolved: list rules", "err", err)
		return
	}

	for _, rule := range rules {
		var ruleIssues []*storage.Issue
		if len(rule.ProjectIDs) == 0 {
			ruleIssues = issues
		} else {
			for _, pid := range rule.ProjectIDs {
				ruleIssues = append(ruleIssues, byProject[pid]...)
			}
		}
		if len(ruleIssues) == 0 {
			continue
		}

		payload := AlertPayload{
			RuleID:    rule.ID,
			RuleName:  rule.Name,
			ProjectID: firstProjectID(rule),
			Trigger:   "issue_auto_resolved",
			FiredAt:   time.Now().UTC(),
			Details:   map[string]any{"resolved_count": len(ruleIssues)},
			Issues:    ruleIssues,
		}
		if payload.ProjectID != "" {
			_ = e.pool.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, payload.ProjectID).Scan(&payload.ProjectName)
		}

		var deliveryErr error
		switch rule.Channel {
		case "webhook":
			deliveryErr = e.fireWebhook(ctx, rule, payload)
		case "slack":
			deliveryErr = e.fireSlack(ctx, rule, payload)
		case "discord":
			deliveryErr = e.fireDiscord(ctx, rule, payload)
		case "email":
			deliveryErr = e.fireEmail(ctx, rule, payload)
		}
		if deliveryErr != nil {
			slog.Error("auto-resolve notification failed", "rule", rule.ID, "channel", rule.Channel, "err", deliveryErr)
		}
	}
}

func (e *Evaluator) fireWebhook(ctx context.Context, rule *storage.AlertRule, payload AlertPayload) error {
	if rule.WebhookURL == nil || *rule.WebhookURL == "" {
		return fmt.Errorf("webhook_url is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, *rule.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return nil
}

var alertTriggerLabels = map[string]string{
	"new_issue":           "New issue",
	"regressed":           "Regression",
	"new_or_regressed":    "New issue or regression",
	"event_count":         "Event count",
	"cron_missed":         "Cron monitor missed",
	"cron_error":          "Cron monitor error",
	"issue_auto_resolved": "Performance issue auto-resolved",
}

func (e *Evaluator) fireSlack(ctx context.Context, rule *storage.AlertRule, payload AlertPayload) error {
	if rule.WebhookURL == nil || *rule.WebhookURL == "" {
		return fmt.Errorf("webhook_url is empty")
	}

	subject := buildAlertSubject(payload)
	proj := payload.ProjectName
	if proj == "" {
		proj = payload.ProjectID
	}
	trigLabel := alertTriggerLabels[payload.Trigger]
	if trigLabel == "" {
		trigLabel = payload.Trigger
	}

	type textObj struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	type block struct {
		Type     string    `json:"type"`
		Text     *textObj  `json:"text,omitempty"`
		Fields   []textObj `json:"fields,omitempty"`
		Elements []textObj `json:"elements,omitempty"`
	}

	blocks := []block{
		{
			Type: "section",
			Text: &textObj{Type: "mrkdwn", Text: "*" + subject + "*"},
		},
		{
			Type: "section",
			Fields: []textObj{
				{Type: "mrkdwn", Text: "*Project:*\n" + proj},
				{Type: "mrkdwn", Text: "*Trigger:*\n" + trigLabel},
			},
		},
	}

	if len(payload.Issues) > 0 {
		var sb strings.Builder
		for _, iss := range payload.Issues {
			if e.publicURL != "" {
				sb.WriteString("• <" + e.publicURL + "/issues/" + iss.ID + "|" + iss.Title + ">\n")
			} else {
				sb.WriteString("• " + iss.Title + "\n")
			}
		}
		blocks = append(blocks, block{
			Type: "section",
			Text: &textObj{Type: "mrkdwn", Text: "*Issues:*\n" + strings.TrimRight(sb.String(), "\n")},
		})
	}

	blocks = append(blocks, block{
		Type:     "context",
		Elements: []textObj{{Type: "mrkdwn", Text: "Tindra · " + payload.FiredAt.UTC().Format("2006-01-02 15:04 UTC")}},
	})

	slackMsg := struct {
		Text   string  `json:"text"`
		Blocks []block `json:"blocks"`
	}{
		Text:   subject,
		Blocks: blocks,
	}

	body, err := json.Marshal(slackMsg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, *rule.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack returned %d", resp.StatusCode)
	}
	return nil
}

func (e *Evaluator) fireDiscord(ctx context.Context, rule *storage.AlertRule, payload AlertPayload) error {
	if rule.WebhookURL == nil || *rule.WebhookURL == "" {
		return fmt.Errorf("webhook_url is empty")
	}

	subject := buildAlertSubject(payload)
	proj := payload.ProjectName
	if proj == "" {
		proj = payload.ProjectID
	}
	trigLabel := alertTriggerLabels[payload.Trigger]
	if trigLabel == "" {
		trigLabel = payload.Trigger
	}

	type embedField struct {
		Name   string `json:"name"`
		Value  string `json:"value"`
		Inline bool   `json:"inline"`
	}
	type embedFooter struct {
		Text string `json:"text"`
	}
	type embed struct {
		Title       string       `json:"title"`
		Color       int          `json:"color"`
		Description string       `json:"description,omitempty"`
		Fields      []embedField `json:"fields"`
		Footer      embedFooter  `json:"footer"`
	}

	fields := []embedField{
		{Name: "Project", Value: proj, Inline: true},
		{Name: "Trigger", Value: trigLabel, Inline: true},
	}

	var description string
	if len(payload.Issues) > 0 {
		var sb strings.Builder
		for _, iss := range payload.Issues {
			if e.publicURL != "" {
				sb.WriteString("- [" + iss.Title + "](" + e.publicURL + "/issues/" + iss.ID + ")\n")
			} else {
				sb.WriteString("- " + iss.Title + "\n")
			}
		}
		description = strings.TrimRight(sb.String(), "\n")
	}

	msg := struct {
		Embeds []embed `json:"embeds"`
	}{
		Embeds: []embed{{
			Title:       subject,
			Color:       15548997, // Discord red (#ED4245)
			Description: description,
			Fields:      fields,
			Footer:      embedFooter{Text: "Tindra · " + payload.FiredAt.UTC().Format("2006-01-02 15:04 UTC")},
		}},
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, *rule.WebhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("discord returned %d", resp.StatusCode)
	}
	return nil
}

func (e *Evaluator) fireEmail(ctx context.Context, rule *storage.AlertRule, payload AlertPayload) error {
	if e.email == nil {
		return fmt.Errorf("email not configured (EMAIL_PROVIDER not set)")
	}
	if rule.EmailTo == nil || *rule.EmailTo == "" {
		return fmt.Errorf("email_to missing on rule")
	}
	html, text, err := RenderAlertEmail(payload, e.publicURL)
	if err != nil {
		return fmt.Errorf("render email: %w", err)
	}
	return e.email.Send(ctx, EmailMessage{
		To:      *rule.EmailTo,
		Subject: buildAlertSubject(payload),
		Text:    text,
		HTML:    html,
	})
}

// FireTest fires the rule unconditionally, bypassing condition checks and cooldown.
// Used by the API test endpoint to verify delivery configuration.
// Does not update last_fired_at.
func (e *Evaluator) FireTest(ctx context.Context, rule *storage.AlertRule) error {
	payload := AlertPayload{
		RuleID:    rule.ID,
		RuleName:  rule.Name,
		ProjectID: firstProjectID(rule),
		Trigger:   rule.Trigger,
		FiredAt:   time.Now().UTC(),
		Details:   map[string]any{"test": true},
	}
	e.enrichPayload(ctx, &payload, rule)
	switch rule.Channel {
	case "webhook":
		return e.fireWebhook(ctx, rule, payload)
	case "slack":
		return e.fireSlack(ctx, rule, payload)
	case "discord":
		return e.fireDiscord(ctx, rule, payload)
	case "email":
		return e.fireEmail(ctx, rule, payload)
	}
	return fmt.Errorf("unknown channel: %s", rule.Channel)
}

func buildAlertSubject(p AlertPayload) string {
	proj := p.ProjectName
	if proj == "" {
		proj = p.ProjectID
	}
	suffix := ""
	if proj != "" {
		suffix = " - " + proj
	}
	switch p.Trigger {
	case "new_issue":
		count, _ := p.Details["new_issue_count"].(int)
		if count == 1 {
			if title := singleIssueTitle(p); title != "" {
				return "[Tindra] New issue: " + title + suffix
			}
			return "[Tindra] 1 new issue" + suffix
		}
		return fmt.Sprintf("[Tindra] %d new issues%s", count, suffix)
	case "regressed":
		count, _ := p.Details["regressed_count"].(int)
		if count == 1 {
			if title := singleIssueTitle(p); title != "" {
				return "[Tindra] Regression: " + title + suffix
			}
			return "[Tindra] 1 regression" + suffix
		}
		return fmt.Sprintf("[Tindra] %d regressions%s", count, suffix)
	case "new_or_regressed":
		n, _ := p.Details["new_issue_count"].(int)
		r, _ := p.Details["regressed_count"].(int)
		total := n + r
		if total == 1 {
			if title := singleIssueTitle(p); title != "" {
				return "[Tindra] Issue: " + title + suffix
			}
			return "[Tindra] 1 issue" + suffix
		}
		return fmt.Sprintf("[Tindra] %d issues%s", total, suffix)
	case "event_count":
		count, _ := p.Details["event_count"].(int)
		window, _ := p.Details["window_mins"].(int)
		return fmt.Sprintf("[Tindra] %d events in %dmin%s", count, window, suffix)
	case "cron_missed":
		count, _ := p.Details["missed_count"].(int)
		if count == 1 {
			return "[Tindra] 1 cron monitor missed" + suffix
		}
		return fmt.Sprintf("[Tindra] %d cron monitors missed%s", count, suffix)
	case "cron_error":
		count, _ := p.Details["error_count"].(int)
		if count == 1 {
			return "[Tindra] 1 cron check-in error" + suffix
		}
		return fmt.Sprintf("[Tindra] %d cron check-in errors%s", count, suffix)
	case "issue_auto_resolved":
		count, _ := p.Details["resolved_count"].(int)
		if count == 1 {
			return "[Tindra] 1 performance issue auto-resolved" + suffix
		}
		return fmt.Sprintf("[Tindra] %d performance issues auto-resolved%s", count, suffix)
	default:
		return fmt.Sprintf("[Tindra] %s%s", p.RuleName, suffix)
	}
}

// singleIssueTitle returns the title of the first issue, truncated to 120
// characters, or "" if no issues are attached to the payload.
func singleIssueTitle(p AlertPayload) string {
	if len(p.Issues) == 0 {
		return ""
	}
	t := p.Issues[0].Title
	if len(t) > 120 {
		t = t[:117] + "..."
	}
	return t
}
