package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/storage"
)

var validTriggers = map[string]bool{
	"new_issue":        true,
	"regressed":        true,
	"new_or_regressed": true,
	"event_count":      true,
	"cron_missed":      true,
	"cron_error":       true,
	"uptime_down":      true,
	"uptime_recovered": true,
}
var validChannels = map[string]bool{"webhook": true, "slack": true, "discord": true, "teams": true, "email": true}
var validLevels = map[string]bool{"fatal": true, "error": true, "warning": true, "info": true, "debug": true}

func validateAlertRule(r *storage.AlertRule) string {
	if r.Name == "" {
		return "name is required"
	}
	if !validTriggers[r.Trigger] {
		return "trigger must be new_issue, regressed, new_or_regressed, event_count, cron_missed, cron_error, uptime_down, or uptime_recovered"
	}
	if r.Trigger == "event_count" {
		if r.Threshold == nil || r.WindowMins == nil {
			return "threshold and window_mins required for event_count trigger"
		}
		if *r.Threshold <= 0 || *r.WindowMins <= 0 {
			return "threshold and window_mins must be positive"
		}
	}
	if !validChannels[r.Channel] {
		return "channel must be webhook, slack, discord, teams, or email"
	}
	if (r.Channel == "webhook" || r.Channel == "slack" || r.Channel == "discord" || r.Channel == "teams") && (r.WebhookURL == nil || *r.WebhookURL == "") {
		return "webhook_url required for " + r.Channel + " channel"
	}
	if r.Channel == "email" && (r.EmailTo == nil || *r.EmailTo == "") {
		return "email_to required for email channel"
	}
	if r.FilterLevel != nil && !validLevels[*r.FilterLevel] {
		return "filter_level must be fatal, error, warning, info, or debug"
	}
	if r.MinOccurrences != nil && *r.MinOccurrences < 1 {
		return "min_occurrences must be at least 1"
	}
	if r.CooldownMins <= 0 {
		r.CooldownMins = 60
	}
	return ""
}

func (ro *router) handleCreateAlertRule(w http.ResponseWriter, r *http.Request) {
	var rule storage.AlertRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
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

	created, err := storage.CreateAlertRule(r.Context(), ro.pool, &rule)
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

	writeJSON(w, struct {
		Rules []*storage.AlertRule `json:"rules"`
	}{Rules: rules})
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
	if tokenProjID, ok := r.Context().Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		found := slices.Contains(rule.ProjectIDs, tokenProjID)
		if !found {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
	}

	writeJSON(w, rule)
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

	// Decode patch - only provided fields overwrite the existing rule.
	var patch struct {
		Name              *string  `json:"name"`
		Enabled           *bool    `json:"enabled"`
		Trigger           *string  `json:"trigger"`
		Threshold         *int     `json:"threshold"`
		WindowMins        *int     `json:"window_mins"`
		Channel           *string  `json:"channel"`
		WebhookURL        *string  `json:"webhook_url"`
		EmailTo           *string  `json:"email_to"`
		CooldownMins      *int     `json:"cooldown_mins"`
		FilterLevel       *string  `json:"filter_level"`
		FilterEnvironment *string  `json:"filter_environment"`
		MinOccurrences    *int     `json:"min_occurrences"`
		ProjectIDs        []string `json:"project_ids"`
		// Explicit nulls: sending null clears the field.
		ClearFilterLevel       bool `json:"-"`
		ClearFilterEnvironment bool `json:"-"`
		ClearMinOccurrences    bool `json:"-"`
		HasProjectIDs          bool `json:"-"`
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
		case "filter_environment":
			if string(v) == "null" {
				patch.ClearFilterEnvironment = true
			} else {
				parseErr = json.Unmarshal(v, &patch.FilterEnvironment)
			}
		case "min_occurrences":
			if string(v) == "null" {
				patch.ClearMinOccurrences = true
			} else {
				parseErr = json.Unmarshal(v, &patch.MinOccurrences)
			}
		case "project_ids":
			parseErr = json.Unmarshal(v, &patch.ProjectIDs)
			patch.HasProjectIDs = true
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
	if patch.WebhookURL != nil {
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
	if patch.FilterEnvironment != nil {
		existing.FilterEnvironment = patch.FilterEnvironment
	} else if patch.ClearFilterEnvironment {
		existing.FilterEnvironment = nil
	}
	if patch.MinOccurrences != nil {
		existing.MinOccurrences = patch.MinOccurrences
	} else if patch.ClearMinOccurrences {
		existing.MinOccurrences = nil
	}
	if patch.HasProjectIDs {
		existing.ProjectIDs = patch.ProjectIDs
	}

	if msg := validateAlertRule(existing); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	if existing.Channel == "webhook" || existing.Channel == "slack" || existing.Channel == "discord" || existing.Channel == "teams" {
		if err := alerts.ValidateWebhookURL(r.Context(), *existing.WebhookURL, ro.webhookAllowPrivateIPs); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	updated, err := storage.UpdateAlertRule(r.Context(), ro.pool, existing)
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
