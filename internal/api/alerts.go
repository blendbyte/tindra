package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

var validTriggers = map[string]bool{
	"new_issue":        true,
	"regressed":        true,
	"new_or_regressed": true,
	"event_count":      true,
	"log_count":        true,
	"cron_missed":      true,
	"cron_error":       true,
	"uptime_down":      true,
	"uptime_recovered": true,
}
var validChannels = map[string]bool{"webhook": true, "slack": true, "discord": true, "teams": true, "email": true, "push": true}
var validLevels = map[string]bool{"fatal": true, "error": true, "warning": true, "info": true, "debug": true}
var validLogCountLevels = map[string]bool{"fatal": true, "error": true, "warning": true}

const maxFilterSearchLen = 200

// maxFilterEnvironments bounds how many environments one rule can match.
const maxFilterEnvironments = 20

// normalizeEnvironments trims names, drops blanks and repeats, and keeps the
// order given. An empty result matches every environment.
func normalizeEnvironments(envs []string) ([]string, string) {
	out := make([]string, 0, len(envs))
	for _, env := range envs {
		env = strings.TrimSpace(env)
		if env == "" || slices.Contains(out, env) {
			continue
		}
		if len(env) > ingest.MaxIndexedFieldBytes {
			return nil, fmt.Sprintf("filter_environments entries must be at most %d bytes", ingest.MaxIndexedFieldBytes)
		}
		// Checked per entry so a long list is refused before the repeat
		// check above grows quadratic.
		if len(out) == maxFilterEnvironments {
			return nil, fmt.Sprintf("filter_environments can list at most %d environments", maxFilterEnvironments)
		}
		out = append(out, env)
	}
	return out, ""
}

// legacyEnvironments maps the single filter_environment field that came
// before filter_environments, so API clients written for it keep working.
func legacyEnvironments(env *string) []string {
	if env == nil || strings.TrimSpace(*env) == "" {
		return nil
	}
	return []string{*env}
}

func validateAlertRule(r *storage.AlertRule) string {
	if r.Name == "" {
		return "name is required"
	}
	if !validTriggers[r.Trigger] {
		return "trigger must be new_issue, regressed, new_or_regressed, event_count, log_count, cron_missed, cron_error, uptime_down, or uptime_recovered"
	}
	if r.Trigger == "event_count" {
		if r.Threshold == nil || r.WindowMins == nil {
			return "threshold and window_mins required for event_count trigger"
		}
		if *r.Threshold <= 0 || *r.WindowMins <= 0 {
			return "threshold and window_mins must be positive"
		}
	}
	if r.Trigger == "log_count" {
		if nonemptyCount(r.ProjectIDs) == 0 {
			return "project_ids required for log_count trigger"
		}
		if r.Threshold == nil || r.WindowMins == nil {
			return "threshold and window_mins required for log_count trigger"
		}
		if *r.Threshold <= 0 {
			return "threshold must be positive"
		}
		if *r.WindowMins < 1 || *r.WindowMins > 60 {
			return "window_mins must be between 1 and 60 for log_count trigger"
		}
		if r.FilterLevel == nil || *r.FilterLevel == "" {
			return "filter_level required for log_count trigger"
		}
		if !validLogCountLevels[*r.FilterLevel] {
			return "filter_level must be fatal, error, or warning for log_count trigger"
		}
		if r.FilterSearch != nil {
			trimmed := strings.TrimSpace(*r.FilterSearch)
			if trimmed == "" {
				r.FilterSearch = nil
			} else {
				if len(trimmed) > maxFilterSearchLen {
					return "filter_search must be at most 200 characters"
				}
				r.FilterSearch = &trimmed
			}
		}
		if *r.FilterLevel == "warning" && (r.FilterSearch == nil || *r.FilterSearch == "") {
			return "filter_search required when log_count min level is warning"
		}
	}
	if !validChannels[r.Channel] {
		return "channel must be webhook, slack, discord, teams, email, or push"
	}
	if (r.Channel == "webhook" || r.Channel == "slack" || r.Channel == "discord" || r.Channel == "teams") && (r.WebhookURL == nil || *r.WebhookURL == "") {
		return "webhook_url required for " + r.Channel + " channel"
	}
	if r.Channel == "email" && (r.EmailTo == nil || *r.EmailTo == "") {
		return "email_to required for email channel"
	}
	if r.Channel == "push" {
		// A paused rule may have none, so one whose last recipient was
		// deleted can still be paused or edited.
		if r.Enabled && nonemptyCount(r.RecipientIDs) == 0 {
			return "recipient_ids required for an enabled push rule"
		}
	} else {
		// Recipients only mean something for push; drop them so a rule that
		// switched channels does not keep stale ones.
		r.RecipientIDs = nil
	}
	if r.FilterLevel != nil && r.Trigger != "log_count" && !validLevels[*r.FilterLevel] {
		return "filter_level must be fatal, error, warning, info, or debug"
	}
	if r.MinOccurrences != nil && *r.MinOccurrences < 1 {
		return "min_occurrences must be at least 1"
	}
	envs, msg := normalizeEnvironments(r.FilterEnvironments)
	if msg != "" {
		return msg
	}
	r.FilterEnvironments = envs
	if r.CooldownMins <= 0 {
		r.CooldownMins = 60
	}
	return ""
}

func nonemptyCount(ids []string) int {
	n := 0
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			n++
		}
	}
	return n
}

// errRuleProjectsForbidden is the answer when a rule names projects the caller
// cannot see, or names none while the caller cannot see them all.
const errRuleProjectsForbidden = "project_ids must list projects you have access to"

func (ro *router) handleCreateAlertRule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		storage.AlertRule
		LegacyEnvironment *string `json:"filter_environment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	rule := body.AlertRule
	if rule.FilterEnvironments == nil {
		rule.FilterEnvironments = legacyEnvironments(body.LegacyEnvironment)
	}
	rule.Enabled = true // default on creation; caller can override

	if msg := validateAlertRule(&rule); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	if rule.Channel == "webhook" || rule.Channel == "slack" || rule.Channel == "discord" || rule.Channel == "teams" {
		if err := alerts.ValidateWebhookURL(r.Context(), *rule.WebhookURL, ro.webhookAllowPrivateIPs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if !ruleProjectsAllowed(r.Context(), rule.ProjectIDs) {
		http.Error(w, errRuleProjectsForbidden, http.StatusBadRequest)
		return
	}
	created, err := storage.CreateAlertRule(r.Context(), ro.pool, &rule)
	if errors.Is(err, storage.ErrNoPushRecipients) || errors.Is(err, storage.ErrRecipientCannotSeeRule) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.Error("create alert rule", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "alert_rule.created",
		ActorID:   actorFromContext(r.Context()),
		TargetID:  &created.ID,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"name": created.Name, "trigger": created.Trigger},
	})
	writeJSONStatus(w, http.StatusCreated, created)
}

func (ro *router) handleListAlertRules(w http.ResponseWriter, r *http.Request) {
	projectID, _ := r.Context().Value(ctxTokenProjID).(string)
	rules, err := storage.ListAlertRules(r.Context(), ro.pool, projectID)
	if err != nil {
		slog.Error("list alert rules", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rules = slices.DeleteFunc(rules, func(rule *storage.AlertRule) bool {
		return !ruleVisible(r.Context(), rule)
	})

	if !canManageAlerts(r.Context()) {
		for _, rule := range rules {
			redactRuleWebhook(rule)
		}
	}
	if actorFromContext(r.Context()) == nil {
		for _, rule := range rules {
			redactRuleRecipients(rule)
		}
	}

	writeJSON(w, struct {
		Rules []*storage.AlertRule `json:"rules"`
	}{Rules: rules})
}

// canManageAlerts reports whether the caller is a session user with
// manage_alerts. Bearer tokens carry no user permissions.
func canManageAlerts(ctx context.Context) bool {
	if _, isToken := ctx.Value(ctxTokenProjID).(string); isToken {
		return false
	}
	p := permsFromContext(ctx)
	return p != nil && p.ManageAlerts
}

// redactWebhookURL keeps enough of a webhook URL to recognise it (scheme, host
// and the last four characters) while hiding the path and query, which for
// Slack, Discord and Teams incoming webhooks are the credential itself.
func redactWebhookURL(raw string) string {
	tail := raw[max(len(raw)-4, 0):]
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "…" + tail
	}
	return u.Scheme + "://" + u.Host + "/…" + tail
}

// redactRuleRecipients hides who a push rule notifies from callers without a
// session (API tokens, the MCP tools): user IDs are not theirs to see, just
// as /api/users is not.
func redactRuleRecipients(rule *storage.AlertRule) {
	rule.RecipientIDs = []string{}
}

// redactRuleWebhook replaces the rule's webhook URL with its redacted form.
func redactRuleWebhook(rule *storage.AlertRule) {
	if rule.WebhookURL != nil && *rule.WebhookURL != "" {
		redacted := redactWebhookURL(*rule.WebhookURL)
		rule.WebhookURL = &redacted
	}
}

func (ro *router) handleGetAlertRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ruleID")
	rule, err := storage.GetAlertRule(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get alert rule", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rule == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceRuleAccess(w, r, rule) {
		return
	}
	if !canManageAlerts(r.Context()) {
		redactRuleWebhook(rule)
	}
	if actorFromContext(r.Context()) == nil {
		redactRuleRecipients(rule)
	}

	writeJSON(w, rule)
}

// ruleVisible reports whether the caller may see an alert rule. A project-scoped
// token sees rules scoped to its project; global rules span every project, so
// they are hidden from tokens. A session user who cannot see every project only
// sees rules whose projects they can all see, and never a global rule.
func ruleVisible(ctx context.Context, rule *storage.AlertRule) bool {
	if tokenProjID, ok := ctx.Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		return slices.Contains(rule.ProjectIDs, tokenProjID)
	}
	if accessFromContext(ctx).All {
		return true
	}
	return !rule.IsGlobal && ruleProjectsAllowed(ctx, rule.ProjectIDs)
}

// ruleProjectsAllowed reports whether a rule limited to projectIDs stays within
// what the caller may see. A rule with no projects covers every project, so only
// a caller who can see them all may have one.
func ruleProjectsAllowed(ctx context.Context, projectIDs []string) bool {
	access := accessFromContext(ctx)
	if access.All {
		return true
	}
	n := 0
	for _, id := range projectIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !access.Allows(id) {
			return false
		}
		n++
	}
	return n > 0
}

// enforceRuleAccess writes a 404 and returns false when the caller may not see
// the rule, so a rule about a hidden project looks like one that does not exist.
func enforceRuleAccess(w http.ResponseWriter, r *http.Request, rule *storage.AlertRule) bool {
	if !ruleVisible(r.Context(), rule) {
		http.Error(w, "not found", http.StatusNotFound)
		return false
	}
	return true
}

func (ro *router) handleUpdateAlertRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ruleID")
	existing, err := storage.GetAlertRule(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get alert rule", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceRuleAccess(w, r, existing) {
		return
	}

	// Decode patch - only provided fields overwrite the existing rule.
	var patch struct {
		Name               *string  `json:"name"`
		Enabled            *bool    `json:"enabled"`
		Trigger            *string  `json:"trigger"`
		Threshold          *int     `json:"threshold"`
		WindowMins         *int     `json:"window_mins"`
		Channel            *string  `json:"channel"`
		WebhookURL         *string  `json:"webhook_url"`
		EmailTo            *string  `json:"email_to"`
		CooldownMins       *int     `json:"cooldown_mins"`
		FilterLevel        *string  `json:"filter_level"`
		FilterEnvironments []string `json:"filter_environments"`
		LegacyEnvironment  *string  `json:"filter_environment"`
		MinOccurrences     *int     `json:"min_occurrences"`
		FilterSearch       *string  `json:"filter_search"`
		ProjectIDs         []string `json:"project_ids"`
		RecipientIDs       []string `json:"recipient_ids"`
		// Explicit nulls: sending null clears the field.
		ClearFilterLevel      bool `json:"-"`
		HasFilterEnvironments bool `json:"-"`
		HasLegacyEnvironment  bool `json:"-"`
		ClearMinOccurrences   bool `json:"-"`
		ClearFilterSearch     bool `json:"-"`
		HasProjectIDs         bool `json:"-"`
		HasRecipientIDs       bool `json:"-"`
	}
	// Use a raw map to detect explicit nulls vs omitted fields.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var parseErr error
	for k, v := range raw {
		switch k {
		case "name":
			parseErr = json.Unmarshal(v, &patch.Name)
		case "enabled":
			parseErr = json.Unmarshal(v, &patch.Enabled)
		case "trigger":
			parseErr = json.Unmarshal(v, &patch.Trigger)
		case "threshold":
			parseErr = json.Unmarshal(v, &patch.Threshold)
		case "window_mins":
			parseErr = json.Unmarshal(v, &patch.WindowMins)
		case "channel":
			parseErr = json.Unmarshal(v, &patch.Channel)
		case "webhook_url":
			parseErr = json.Unmarshal(v, &patch.WebhookURL)
		case "email_to":
			parseErr = json.Unmarshal(v, &patch.EmailTo)
		case "cooldown_mins":
			parseErr = json.Unmarshal(v, &patch.CooldownMins)
		case "filter_level":
			if string(v) == "null" {
				patch.ClearFilterLevel = true
			} else {
				parseErr = json.Unmarshal(v, &patch.FilterLevel)
			}
		case "filter_environments":
			// null clears the filter, like an empty list.
			parseErr = json.Unmarshal(v, &patch.FilterEnvironments)
			patch.HasFilterEnvironments = true
		case "filter_environment":
			parseErr = json.Unmarshal(v, &patch.LegacyEnvironment)
			patch.HasLegacyEnvironment = true
		case "min_occurrences":
			if string(v) == "null" {
				patch.ClearMinOccurrences = true
			} else {
				parseErr = json.Unmarshal(v, &patch.MinOccurrences)
			}
		case "filter_search":
			if string(v) == "null" {
				patch.ClearFilterSearch = true
			} else {
				parseErr = json.Unmarshal(v, &patch.FilterSearch)
			}
		case "project_ids":
			parseErr = json.Unmarshal(v, &patch.ProjectIDs)
			patch.HasProjectIDs = true
		case "recipient_ids":
			parseErr = json.Unmarshal(v, &patch.RecipientIDs)
			patch.HasRecipientIDs = true
		}
		if parseErr != nil {
			break
		}
	}
	if parseErr != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if patch.Name != nil {
		existing.Name = *patch.Name
	}
	if patch.Enabled != nil {
		existing.Enabled = *patch.Enabled
	}
	if patch.Trigger != nil {
		existing.Trigger = *patch.Trigger
	}
	if patch.Threshold != nil {
		existing.Threshold = patch.Threshold
	}
	if patch.WindowMins != nil {
		existing.WindowMins = patch.WindowMins
	}
	if patch.Channel != nil {
		existing.Channel = *patch.Channel
	}
	// A client that echoes back the redacted URL it was shown keeps the stored one.
	if patch.WebhookURL != nil && (existing.WebhookURL == nil || *patch.WebhookURL != redactWebhookURL(*existing.WebhookURL)) {
		existing.WebhookURL = patch.WebhookURL
	}
	if patch.EmailTo != nil {
		existing.EmailTo = patch.EmailTo
	}
	if patch.CooldownMins != nil {
		existing.CooldownMins = *patch.CooldownMins
	}
	if patch.FilterLevel != nil {
		existing.FilterLevel = patch.FilterLevel
	} else if patch.ClearFilterLevel {
		existing.FilterLevel = nil
	}
	if patch.HasFilterEnvironments {
		existing.FilterEnvironments = patch.FilterEnvironments
	} else if patch.HasLegacyEnvironment {
		existing.FilterEnvironments = legacyEnvironments(patch.LegacyEnvironment)
	}
	if patch.MinOccurrences != nil {
		existing.MinOccurrences = patch.MinOccurrences
	} else if patch.ClearMinOccurrences {
		existing.MinOccurrences = nil
	}
	if patch.FilterSearch != nil {
		existing.FilterSearch = patch.FilterSearch
	} else if patch.ClearFilterSearch {
		existing.FilterSearch = nil
	}
	if patch.HasProjectIDs {
		existing.ProjectIDs = patch.ProjectIDs
	}
	if patch.HasRecipientIDs {
		existing.RecipientIDs = patch.RecipientIDs
	}

	if msg := validateAlertRule(existing); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	if !ruleProjectsAllowed(r.Context(), existing.ProjectIDs) {
		http.Error(w, errRuleProjectsForbidden, http.StatusBadRequest)
		return
	}
	if existing.Channel == "webhook" || existing.Channel == "slack" || existing.Channel == "discord" || existing.Channel == "teams" {
		if err := alerts.ValidateWebhookURL(r.Context(), *existing.WebhookURL, ro.webhookAllowPrivateIPs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	updated, err := storage.UpdateAlertRule(r.Context(), ro.pool, existing)
	if errors.Is(err, storage.ErrNoPushRecipients) || errors.Is(err, storage.ErrRecipientCannotSeeRule) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		slog.Error("update alert rule", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "alert_rule.updated",
		ActorID:   actorFromContext(r.Context()),
		TargetID:  &id,
		IP:        r.RemoteAddr,
		Details:   map[string]any{"name": existing.Name},
	})
	writeJSON(w, updated)
}

func (ro *router) handleDeleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ruleID")
	rule, err := storage.GetAlertRule(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get alert rule for delete", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rule == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceRuleAccess(w, r, rule) {
		return
	}
	deleted, err := storage.DeleteAlertRule(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("delete alert rule", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !deleted {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	storage.WriteAuditLog(ro.pool, storage.AuditEntry{
		EventType: "alert_rule.deleted",
		ActorID:   actorFromContext(r.Context()),
		TargetID:  &id,
		IP:        r.RemoteAddr,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (ro *router) handleListAlertFirings(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ruleID")
	rule, err := storage.GetAlertRule(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get alert rule for firings", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rule == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceRuleAccess(w, r, rule) {
		return
	}

	firings, err := storage.ListAlertFirings(r.Context(), ro.pool, id, 50)
	if err != nil {
		slog.Error("list alert firings", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if firings == nil {
		firings = []*storage.AlertFiring{}
	}
	writeJSON(w, struct {
		Firings []*storage.AlertFiring `json:"firings"`
	}{Firings: firings})
}

func (ro *router) handleTestAlertRule(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "ruleID")
	rule, err := storage.GetAlertRule(r.Context(), ro.pool, id)
	if err != nil {
		slog.Error("get alert rule for test", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rule == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !enforceRuleAccess(w, r, rule) {
		return
	}

	if ro.evaluator == nil {
		http.Error(w, "alert evaluator not configured", http.StatusServiceUnavailable)
		return
	}

	if err := ro.evaluator.FireTest(r.Context(), rule); err != nil {
		slog.Error("test alert rule", "rule", rule.ID, "err", err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
