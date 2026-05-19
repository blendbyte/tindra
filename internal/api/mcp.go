package api

// MCP (Model Context Protocol) integration.
// Transport: Streamable HTTP - single POST /mcp endpoint, JSON-RPC 2.0.
// Auth: same requireAuth middleware as all other API routes (session cookie or Bearer token).
// Bearer tokens are project-scoped; all queries are automatically filtered to that project.
// Spec: https://spec.modelcontextprotocol.io/specification/2024-11-05/

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/blendbyte/tindra/internal/alerts"
	"github.com/blendbyte/tindra/internal/storage"
)

const mcpBodyLimit = 64 * 1024 // 64 KB

// --- JSON-RPC 2.0 wire types ---

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"` // absent on notifications
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type mcpResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      any         `json:"id"`
	Result  any         `json:"result,omitempty"`
	Error   *mcpErrBody `json:"error,omitempty"`
}

type mcpErrBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func mcpOK(id any, result any) mcpResponse {
	return mcpResponse{JSONRPC: "2.0", ID: id, Result: result}
}

func mcpRPCError(id any, code int, msg string) mcpResponse {
	return mcpResponse{JSONRPC: "2.0", ID: id, Error: &mcpErrBody{Code: code, Message: msg}}
}

// mcpToolError is a user-visible tool error (bad params, not found, etc.)
// It becomes isError:true content in the tool result, not a JSON-RPC error.
type mcpToolError struct{ msg string }

func (e mcpToolError) Error() string { return e.msg }

// mcpContent wraps text as an MCP tool content list.
func mcpContent(text string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	}
}

// mcpContentError wraps an error message as an MCP tool error result.
func mcpContentError(msg string) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": "Error: " + msg}},
		"isError": true,
	}
}

// rawIDToAny unmarshals a json.RawMessage ID to a Go value for response echo-back.
func rawIDToAny(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

// handleMCP is the single entry-point for the MCP Streamable HTTP transport.
func (ro *router) handleMCP(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, mcpBodyLimit+1))
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	if len(body) > mcpBodyLimit {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	var req mcpRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mcpRPCError(nil, -32700, "parse error"))
		return
	}

	if req.JSONRPC != "2.0" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mcpRPCError(rawIDToAny(req.ID), -32600, "invalid request"))
		return
	}

	// Notifications carry no id field - acknowledge with 202 and no body.
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	resp := ro.dispatchMCP(r.Context(), req)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (ro *router) dispatchMCP(ctx context.Context, req mcpRequest) mcpResponse {
	id := rawIDToAny(req.ID)
	switch req.Method {
	case "initialize":
		return ro.mcpInitialize(id)
	case "ping":
		return mcpOK(id, map[string]any{})
	case "tools/list":
		return mcpOK(id, map[string]any{"tools": mcpToolList()})
	case "tools/call":
		return ro.mcpCallTool(ctx, id, req.Params)
	default:
		return mcpRPCError(id, -32601, "method not found: "+req.Method)
	}
}

func (ro *router) mcpInitialize(id any) mcpResponse {
	return mcpOK(id, map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "tindra", "version": "1.0"},
	})
}

// --- Tool registry ---

func mcpToolList() []map[string]any {
	prop := func(typ, desc string) map[string]any {
		return map[string]any{"type": typ, "description": desc}
	}
	projProp := prop("string", "Scope to a single project ID. Omit for all projects.")

	return []map[string]any{
		{
			"name":        "get_overview",
			"description": "Returns a health summary: open issue count, transaction error rate, cron monitor states, and recently fired alerts.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"project_id": projProp},
			},
		},
		{
			"name":        "list_issues",
			"description": "Lists issues with event counts and last-seen times.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id": projProp,
					"status":     prop("string", "Filter by status: unresolved, resolved, or ignored. Defaults to unresolved."),
					"level":      prop("string", "Filter by level: error, warning, info, or debug."),
					"search":     prop("string", "Search by issue title substring."),
					"limit":      prop("integer", "Max results (1–50, default 20)."),
				},
			},
		},
		{
			"name":        "get_issue",
			"description": "Returns full details for a single issue including top stack frames.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": prop("string", "Issue ID.")},
				"required":   []string{"id"},
			},
		},
		{
			"name":        "list_transactions",
			"description": "Lists transaction summaries sorted by P95 latency descending.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id": projProp,
					"name":       prop("string", "Filter by transaction name substring."),
					"hours":      prop("integer", "Lookback window in hours (1–168, default 24)."),
					"limit":      prop("integer", "Max results (1–50, default 20)."),
				},
			},
		},
		{
			"name":        "list_monitors",
			"description": "Lists cron monitors with their current state (ok, error, missed, unknown, in_progress).",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"project_id": projProp},
			},
		},
		{
			"name":        "get_monitor",
			"description": "Returns a single cron monitor with its recent check-in history.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": prop("string", "Monitor ID.")},
				"required":   []string{"id"},
			},
		},
		{
			"name":        "list_releases",
			"description": "Lists recent releases with new/regressed issue counts and transaction error rates.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id": projProp,
					"limit":      prop("integer", "Max results (1–50, default 10)."),
				},
			},
		},
		{
			"name":        "list_alerts",
			"description": "Lists alert rules with channels, thresholds, and last-fired timestamps.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"project_id": projProp},
			},
		},
		// --- Phase 2: deeper reads ---
		{
			"name":        "get_transaction",
			"description": "Returns a single transaction with its full span waterfall for performance debugging.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": prop("string", "Transaction ID.")},
				"required":   []string{"id"},
			},
		},
		{
			"name":        "list_issue_events",
			"description": "Lists recent event occurrences for an issue showing timestamps, environment, and release.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"issue_id": prop("string", "Issue ID."),
					"limit":    prop("integer", "Max results (1–50, default 20)."),
				},
				"required": []string{"issue_id"},
			},
		},
		{
			"name":        "list_span_summaries",
			"description": "Lists aggregated span performance data grouped by operation. Use type=db for database queries, type=cache for cache ops, type=jobs for background jobs.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":       prop("string", "Span category: db, cache, or jobs."),
					"project_id": projProp,
					"hours":      prop("integer", "Lookback window in hours (1–720, default 24)."),
				},
				"required": []string{"type"},
			},
		},
		// --- Phase 2: writes (require writable token or session with manage_* permission) ---
		{
			"name":        "update_issue",
			"description": "Updates an issue's status (resolved, ignored, unresolved) or assignee.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":          prop("string", "Issue ID."),
					"status":      prop("string", "New status: resolved, ignored, or unresolved."),
					"assignee_id": prop("string", "User ID to assign, or null to unassign."),
				},
				"required": []string{"id"},
			},
		},
		{
			"name":        "bulk_update_issues",
			"description": "Updates the status of multiple issues by ID in one call.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"ids":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Issue IDs to update."},
					"status": prop("string", "New status: resolved, ignored, or unresolved."),
				},
				"required": []string{"ids", "status"},
			},
		},
		{
			"name":        "get_logs",
			"description": "Searches structured log entries. Useful for correlating errors with application logs.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project_id":  projProp,
					"level":       prop("string", "Filter by level: trace, debug, info, warn, error, fatal."),
					"search":      prop("string", "Full-text search across log body and attributes."),
					"environment": prop("string", "Filter by environment."),
					"trace_id":    prop("string", "Filter by trace ID to see logs for a specific trace."),
					"limit":       prop("integer", "Max results (1–100, default 50)."),
				},
			},
		},
		{
			"name":        "create_alert_rule",
			"description": "Creates a new alert rule. Requires a writable token or manage_alerts permission.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":          prop("string", "Rule name."),
					"trigger":       prop("string", "Trigger: new_issue, regressed, new_or_regressed, event_count, cron_missed, or cron_error."),
					"channel":       prop("string", "Notification channel: webhook, slack, discord, or email."),
					"webhook_url":   prop("string", "Webhook/Slack/Discord URL (required for those channels)."),
					"email_to":      prop("string", "Email address (required for email channel)."),
					"threshold":     prop("integer", "Event count threshold (required for event_count trigger)."),
					"window_mins":   prop("integer", "Window in minutes for event_count trigger."),
					"project_id":    prop("string", "Project to apply the rule to. Defaults to token's project or all projects for session auth."),
					"cooldown_mins": prop("integer", "Minutes between repeated firings (default 60)."),
				},
				"required": []string{"name", "trigger", "channel"},
			},
		},
	}
}

// --- Tool call dispatch ---

func (ro *router) mcpCallTool(ctx context.Context, id any, params json.RawMessage) mcpResponse {
	if len(params) == 0 {
		return mcpRPCError(id, -32602, "params required for tools/call")
	}

	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return mcpRPCError(id, -32602, "invalid params")
	}
	if p.Name == "" {
		return mcpRPCError(id, -32602, "name is required")
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}

	var (
		text string
		err  error
	)
	switch p.Name {
	case "get_overview":
		text, err = ro.mcpGetOverview(ctx, p.Arguments)
	case "list_issues":
		text, err = ro.mcpListIssues(ctx, p.Arguments)
	case "get_issue":
		text, err = ro.mcpGetIssue(ctx, p.Arguments)
	case "list_transactions":
		text, err = ro.mcpListTransactions(ctx, p.Arguments)
	case "list_monitors":
		text, err = ro.mcpListMonitors(ctx, p.Arguments)
	case "get_monitor":
		text, err = ro.mcpGetMonitor(ctx, p.Arguments)
	case "list_releases":
		text, err = ro.mcpListReleases(ctx, p.Arguments)
	case "list_alerts":
		text, err = ro.mcpListAlerts(ctx, p.Arguments)
	case "get_transaction":
		text, err = ro.mcpGetTransaction(ctx, p.Arguments)
	case "list_issue_events":
		text, err = ro.mcpListIssueEvents(ctx, p.Arguments)
	case "list_span_summaries":
		text, err = ro.mcpListSpanSummaries(ctx, p.Arguments)
	case "get_logs":
		text, err = ro.mcpGetLogs(ctx, p.Arguments)
	case "update_issue":
		text, err = ro.mcpUpdateIssue(ctx, p.Arguments)
	case "bulk_update_issues":
		text, err = ro.mcpBulkUpdateIssues(ctx, p.Arguments)
	case "create_alert_rule":
		text, err = ro.mcpCreateAlertRule(ctx, p.Arguments)
	default:
		return mcpOK(id, mcpContentError("unknown tool: "+p.Name))
	}

	var toolErr mcpToolError
	if errors.As(err, &toolErr) {
		return mcpOK(id, mcpContentError(toolErr.msg))
	}
	if err != nil {
		slog.Error("mcp tool error", "tool", p.Name, "err", err)
		return mcpRPCError(id, -32603, "internal error")
	}
	return mcpOK(id, mcpContent(text))
}

// --- Argument helpers ---

// mcpProjectIDs returns the effective project ID list for a request.
// Bearer token requests are always scoped to the token's project, ignoring any argument.
func mcpProjectIDs(ctx context.Context, args map[string]any) []string {
	if tokenProjID, ok := ctx.Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		return []string{tokenProjID}
	}
	if v, _ := args["project_id"].(string); v != "" {
		return []string{v}
	}
	return nil
}

func mcpArgString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func mcpArgInt(args map[string]any, key string, def int) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return def
}

func mcpArgLimit(args map[string]any, key string, def, max int) int {
	n := mcpArgInt(args, key, def)
	if n < 1 {
		n = 1
	}
	if n > max {
		n = max
	}
	return n
}

// --- Tool implementations ---

func (ro *router) mcpGetOverview(ctx context.Context, args map[string]any) (string, error) {
	projectIDs := mcpProjectIDs(ctx, args)

	openCount, err := storage.CountAllIssues(ctx, ro.pool, storage.IssueFilter{
		Status:     "unresolved",
		ProjectIDs: projectIDs,
	})
	if err != nil {
		return "", fmt.Errorf("count issues: %w", err)
	}

	monitors, err := storage.ListCronMonitors(ctx, ro.pool, projectIDs)
	if err != nil {
		return "", fmt.Errorf("list monitors: %w", err)
	}
	monitorStates := map[string]int{}
	for _, m := range monitors {
		monitorStates[m.State]++
	}

	tokenProjID, _ := ctx.Value(ctxTokenProjID).(string)
	allRules, err := storage.ListAlertRules(ctx, ro.pool, tokenProjID)
	if err != nil {
		return "", fmt.Errorf("list alert rules: %w", err)
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	firingCount := 0
	for _, rule := range allRules {
		if rule.LastFiredAt == nil || !rule.LastFiredAt.After(cutoff) {
			continue
		}
		if len(projectIDs) > 0 && !ruleMatchesProjects(rule, projectIDs) {
			continue
		}
		firingCount++
	}

	summaries, err := storage.ListTransactionSummaries(ctx, ro.pool, projectIDs, 24, 0, "", "", "", "")
	if err != nil {
		return "", fmt.Errorf("list tx summaries: %w", err)
	}

	result := map[string]any{
		"open_issues":       openCount,
		"monitors":          monitorStates,
		"firing_alerts_24h": firingCount,
	}
	if len(summaries) > 0 {
		var total float64
		for _, s := range summaries {
			total += s.FailureRate
		}
		result["transaction_error_rate"] = fmt.Sprintf("%.2f%%", total/float64(len(summaries))*100)
	}

	b, _ := json.MarshalIndent(result, "", "  ")
	return string(b), nil
}

func (ro *router) mcpListIssues(ctx context.Context, args map[string]any) (string, error) {
	status := mcpArgString(args, "status")
	if status == "" {
		status = "open"
	}
	filter := storage.IssueFilter{
		Status:     status,
		Level:      mcpArgString(args, "level"),
		Title:      mcpArgString(args, "search"),
		ProjectIDs: mcpProjectIDs(ctx, args),
		Limit:      mcpArgLimit(args, "limit", 20, 50),
	}
	issues, err := storage.ListAllIssues(ctx, ro.pool, filter)
	if err != nil {
		return "", fmt.Errorf("list issues: %w", err)
	}
	if issues == nil {
		issues = []*storage.Issue{}
	}
	b, _ := json.MarshalIndent(issues, "", "  ")
	return string(b), nil
}

func (ro *router) mcpGetIssue(ctx context.Context, args map[string]any) (string, error) {
	id := mcpArgString(args, "id")
	if id == "" {
		return "", mcpToolError{"id is required"}
	}
	issue, err := storage.GetIssue(ctx, ro.pool, id)
	if err != nil {
		return "", fmt.Errorf("get issue: %w", err)
	}
	if issue == nil {
		return "", mcpToolError{"issue not found"}
	}
	// Enforce project scope for Bearer token requests.
	if tokenProjID, ok := ctx.Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		if issue.ProjectID != tokenProjID {
			return "", mcpToolError{"issue not found"}
		}
	}
	b, _ := json.MarshalIndent(issue, "", "  ")
	return string(b), nil
}

func (ro *router) mcpListTransactions(ctx context.Context, args map[string]any) (string, error) {
	projectIDs := mcpProjectIDs(ctx, args)
	hours := mcpArgInt(args, "hours", 24)
	if hours < 1 {
		hours = 1
	}
	if hours > 168 {
		hours = 168
	}
	limit := mcpArgLimit(args, "limit", 20, 50)

	summaries, err := storage.ListTransactionSummaries(ctx, ro.pool, projectIDs, hours, 0, "", mcpArgString(args, "name"), "", "")
	if err != nil {
		return "", fmt.Errorf("list transactions: %w", err)
	}
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	if summaries == nil {
		summaries = []*storage.TransactionSummary{}
	}
	b, _ := json.MarshalIndent(summaries, "", "  ")
	return string(b), nil
}

func (ro *router) mcpListMonitors(ctx context.Context, args map[string]any) (string, error) {
	monitors, err := storage.ListCronMonitors(ctx, ro.pool, mcpProjectIDs(ctx, args))
	if err != nil {
		return "", fmt.Errorf("list monitors: %w", err)
	}
	if monitors == nil {
		monitors = []*storage.CronMonitor{}
	}
	b, _ := json.MarshalIndent(monitors, "", "  ")
	return string(b), nil
}

func (ro *router) mcpGetMonitor(ctx context.Context, args map[string]any) (string, error) {
	id := mcpArgString(args, "id")
	if id == "" {
		return "", mcpToolError{"id is required"}
	}
	monitor, err := storage.GetCronMonitor(ctx, ro.pool, id)
	if err != nil {
		return "", fmt.Errorf("get monitor: %w", err)
	}
	if monitor == nil {
		return "", mcpToolError{"monitor not found"}
	}
	// Enforce project scope for Bearer token requests.
	if tokenProjID, ok := ctx.Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		if monitor.ProjectID != tokenProjID {
			return "", mcpToolError{"monitor not found"}
		}
	}
	b, _ := json.MarshalIndent(monitor, "", "  ")
	return string(b), nil
}

func (ro *router) mcpListReleases(ctx context.Context, args map[string]any) (string, error) {
	releases, err := storage.ListReleases(ctx, ro.pool, storage.ReleaseFilter{
		ProjectIDs: mcpProjectIDs(ctx, args),
		Limit:      mcpArgLimit(args, "limit", 10, 50),
	})
	if err != nil {
		return "", fmt.Errorf("list releases: %w", err)
	}
	if releases == nil {
		releases = []*storage.Release{}
	}
	b, _ := json.MarshalIndent(releases, "", "  ")
	return string(b), nil
}

func (ro *router) mcpListAlerts(ctx context.Context, args map[string]any) (string, error) {
	projID, _ := ctx.Value(ctxTokenProjID).(string)
	rules, err := storage.ListAlertRules(ctx, ro.pool, projID)
	if err != nil {
		return "", fmt.Errorf("list alert rules: %w", err)
	}

	projectIDs := mcpProjectIDs(ctx, args)
	if len(projectIDs) > 0 {
		var filtered []*storage.AlertRule
		for _, rule := range rules {
			if ruleMatchesProjects(rule, projectIDs) {
				filtered = append(filtered, rule)
			}
		}
		rules = filtered
	}
	if rules == nil {
		rules = []*storage.AlertRule{}
	}
	b, _ := json.MarshalIndent(rules, "", "  ")
	return string(b), nil
}

// ruleMatchesProjects reports whether an alert rule's project list intersects with the given IDs.
func ruleMatchesProjects(rule *storage.AlertRule, projectIDs []string) bool {
	for _, rp := range rule.ProjectIDs {
		for _, p := range projectIDs {
			if rp == p {
				return true
			}
		}
	}
	return false
}

// --- Write permission check ---

// mcpCheckWrite verifies that the caller is allowed to perform a write operation.
// Bearer tokens must have writable=true. Session users must hold the named permission.
func mcpCheckWrite(ctx context.Context, perm string) error {
	if _, ok := ctx.Value(ctxTokenProjID).(string); ok {
		if writable, _ := ctx.Value(ctxTokenWritable).(bool); !writable {
			return mcpToolError{"this token is read-only; create a writable token to use write operations"}
		}
		return nil
	}
	p := permsFromContext(ctx)
	if p == nil {
		return mcpToolError{"permission denied"}
	}
	switch perm {
	case "manage_issues":
		if !p.ManageIssues {
			return mcpToolError{"manage_issues permission required"}
		}
	case "manage_alerts":
		if !p.ManageAlerts {
			return mcpToolError{"manage_alerts permission required"}
		}
	}
	return nil
}

// --- Phase 2: deeper read tools ---

func (ro *router) mcpGetTransaction(ctx context.Context, args map[string]any) (string, error) {
	id := mcpArgString(args, "id")
	if id == "" {
		return "", mcpToolError{"id is required"}
	}
	tx, err := storage.GetTransaction(ctx, ro.pool, id)
	if err != nil {
		return "", fmt.Errorf("get transaction: %w", err)
	}
	if tx == nil {
		return "", mcpToolError{"transaction not found"}
	}
	// Enforce project scope for Bearer token requests.
	if tokenProjID, ok := ctx.Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		if tx.ProjectID != tokenProjID {
			return "", mcpToolError{"transaction not found"}
		}
	}
	spans, err := storage.GetSpansForTransaction(ctx, ro.pool, id)
	if err != nil {
		return "", fmt.Errorf("get spans: %w", err)
	}
	type spanOut struct {
		SpanID        string `json:"span_id"`
		ParentSpanID  string `json:"parent_span_id,omitempty"`
		Op            string `json:"op"`
		Description   string `json:"description,omitempty"`
		Status        string `json:"status"`
		StartOffsetMs int64  `json:"start_offset_ms"`
		DurationMs    int    `json:"duration_ms"`
	}
	spansOut := make([]spanOut, 0, len(spans))
	for _, s := range spans {
		spansOut = append(spansOut, spanOut{
			SpanID:        s.SpanID,
			ParentSpanID:  s.ParentSpanID,
			Op:            s.Op,
			Description:   s.Description,
			Status:        s.Status,
			StartOffsetMs: s.StartTimestamp.Sub(tx.StartTimestamp).Milliseconds(),
			DurationMs:    s.DurationMs,
		})
	}
	result := map[string]any{"transaction": tx, "spans": spansOut}
	b, _ := json.MarshalIndent(result, "", "  ")
	return string(b), nil
}

func (ro *router) mcpListIssueEvents(ctx context.Context, args map[string]any) (string, error) {
	issueID := mcpArgString(args, "issue_id")
	if issueID == "" {
		return "", mcpToolError{"issue_id is required"}
	}
	// Verify issue exists and enforce project scope.
	issue, err := storage.GetIssue(ctx, ro.pool, issueID)
	if err != nil {
		return "", fmt.Errorf("get issue: %w", err)
	}
	if issue == nil {
		return "", mcpToolError{"issue not found"}
	}
	if tokenProjID, ok := ctx.Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		if issue.ProjectID != tokenProjID {
			return "", mcpToolError{"issue not found"}
		}
	}
	limit := mcpArgLimit(args, "limit", 20, 50)
	events, _, err := storage.ListEventsForIssue(ctx, ro.pool, issueID, nil, nil, limit)
	if err != nil {
		return "", fmt.Errorf("list events: %w", err)
	}
	if events == nil {
		events = []storage.EventSummary{}
	}
	b, _ := json.MarshalIndent(events, "", "  ")
	return string(b), nil
}

func (ro *router) mcpListSpanSummaries(ctx context.Context, args map[string]any) (string, error) {
	category := mcpArgString(args, "type")
	switch category {
	case "db", "cache":
		// valid as-is
	case "jobs":
		category = "job" // storage uses "job" internally
	default:
		return "", mcpToolError{"type must be db, cache, or jobs"}
	}
	hours := mcpArgInt(args, "hours", 24)
	if hours < 1 {
		hours = 1
	}
	if hours > 720 {
		hours = 720
	}
	summaries, err := storage.GetSpanSummaries(ctx, ro.pool, category, mcpProjectIDs(ctx, args), hours, "", "")
	if err != nil {
		return "", fmt.Errorf("get span summaries: %w", err)
	}
	if summaries == nil {
		summaries = []*storage.SpanSummary{}
	}
	b, _ := json.MarshalIndent(summaries, "", "  ")
	return string(b), nil
}

func (ro *router) mcpGetLogs(ctx context.Context, args map[string]any) (string, error) {
	limit := mcpArgLimit(args, "limit", 50, 100)
	filter := storage.LogFilter{
		ProjectIDs:  mcpProjectIDs(ctx, args),
		Level:       mcpArgString(args, "level"),
		Search:      mcpArgString(args, "search"),
		Environment: mcpArgString(args, "environment"),
		TraceID:     mcpArgString(args, "trace_id"),
		Limit:       limit,
	}
	logs, _, err := storage.ListLogs(ctx, ro.pool, filter)
	if err != nil {
		return "", fmt.Errorf("list logs: %w", err)
	}
	if logs == nil {
		logs = []*storage.Log{}
	}
	b, _ := json.MarshalIndent(logs, "", "  ")
	return string(b), nil
}

// --- Phase 2: write tools ---

func (ro *router) mcpUpdateIssue(ctx context.Context, args map[string]any) (string, error) {
	if err := mcpCheckWrite(ctx, "manage_issues"); err != nil {
		return "", err
	}
	id := mcpArgString(args, "id")
	if id == "" {
		return "", mcpToolError{"id is required"}
	}
	issue, err := storage.GetIssue(ctx, ro.pool, id)
	if err != nil {
		return "", fmt.Errorf("get issue: %w", err)
	}
	if issue == nil {
		return "", mcpToolError{"issue not found"}
	}
	if tokenProjID, ok := ctx.Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		if issue.ProjectID != tokenProjID {
			return "", mcpToolError{"issue not found"}
		}
	}

	status := mcpArgString(args, "status")
	var updated *storage.Issue
	if status != "" {
		updated, err = storage.UpdateIssueStatus(ctx, ro.pool, issue.ProjectID, id, status, nil)
	} else {
		// assignee update - raw value may be nil (unassign) or a string
		var assigneeID *string
		if v, ok := args["assignee_id"].(string); ok && v != "" {
			assigneeID = &v
		}
		updated, err = storage.UpdateIssueAssignee(ctx, ro.pool, id, assigneeID)
	}
	if err != nil {
		return "", fmt.Errorf("update issue: %w", err)
	}
	if updated == nil {
		return "", mcpToolError{"issue not found"}
	}
	b, _ := json.MarshalIndent(updated, "", "  ")
	return string(b), nil
}

func (ro *router) mcpBulkUpdateIssues(ctx context.Context, args map[string]any) (string, error) {
	if err := mcpCheckWrite(ctx, "manage_issues"); err != nil {
		return "", err
	}
	rawIDs, ok := args["ids"].([]any)
	if !ok || len(rawIDs) == 0 {
		return "", mcpToolError{"ids must be a non-empty array"}
	}
	ids := make([]string, 0, len(rawIDs))
	for _, v := range rawIDs {
		if s, ok := v.(string); ok && s != "" {
			ids = append(ids, s)
		}
	}
	if len(ids) == 0 {
		return "", mcpToolError{"ids must be a non-empty array of strings"}
	}
	status := mcpArgString(args, "status")
	if status == "" {
		return "", mcpToolError{"status is required"}
	}

	// When using a bearer token, restrict to the token's project to prevent cross-project writes.
	var projectIDs []string
	if tokenProjID, ok := ctx.Value(ctxTokenProjID).(string); ok && tokenProjID != "" {
		projectIDs = []string{tokenProjID}
	}

	n, err := storage.BulkUpdateIssueStatus(ctx, ro.pool, ids, status, nil, projectIDs)
	if err != nil {
		return "", fmt.Errorf("bulk update: %w", err)
	}
	b, _ := json.MarshalIndent(map[string]any{"updated": n}, "", "  ")
	return string(b), nil
}

func (ro *router) mcpCreateAlertRule(ctx context.Context, args map[string]any) (string, error) {
	if err := mcpCheckWrite(ctx, "manage_alerts"); err != nil {
		return "", err
	}

	name := mcpArgString(args, "name")
	trigger := mcpArgString(args, "trigger")
	channel := mcpArgString(args, "channel")
	if name == "" || trigger == "" || channel == "" {
		return "", mcpToolError{"name, trigger, and channel are required"}
	}

	rule := &storage.AlertRule{
		Name:         name,
		Trigger:      trigger,
		Channel:      channel,
		Enabled:      true,
		CooldownMins: mcpArgInt(args, "cooldown_mins", 60),
	}

	// Project scoping: bearer token forces its own project; session may specify one.
	projectIDs := mcpProjectIDs(ctx, args)
	if len(projectIDs) > 0 {
		rule.ProjectIDs = projectIDs
	}

	if v := mcpArgString(args, "webhook_url"); v != "" {
		rule.WebhookURL = &v
	}
	if v := mcpArgString(args, "email_to"); v != "" {
		rule.EmailTo = &v
	}
	if v := mcpArgInt(args, "threshold", 0); v > 0 {
		rule.Threshold = &v
	}
	if v := mcpArgInt(args, "window_mins", 0); v > 0 {
		rule.WindowMins = &v
	}

	if msg := validateAlertRule(rule); msg != "" {
		return "", mcpToolError{msg}
	}
	if (channel == "webhook" || channel == "slack" || channel == "discord") && rule.WebhookURL != nil {
		if err := alerts.ValidateWebhookURL(ctx, *rule.WebhookURL, ro.webhookAllowPrivateIPs); err != nil {
			return "", mcpToolError{err.Error()}
		}
	}

	created, err := storage.CreateAlertRule(ctx, ro.pool, rule)
	if err != nil {
		return "", fmt.Errorf("create alert rule: %w", err)
	}
	b, _ := json.MarshalIndent(created, "", "  ")
	return string(b), nil
}
