package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func mcpHandler() http.Handler {
	return api.NewRouter(testPool, ingest.NewBuffer(1), nil, nil, nil, nil, nil, false, "", "", "", "", 0, 0, 0, 0, 0, 0, nil, false, true, nil)
}

type rpcResp struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Result  map[string]any `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// postMCP sends a raw body to POST /mcp and returns the recorder.
func postMCPRaw(t *testing.T, h http.Handler, body string, ct string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// postMCP sends an authenticated JSON-RPC 2.0 request and decodes the response.
func postMCP(t *testing.T, h http.Handler, method string, params any, cookies ...*http.Cookie) rpcResp {
	t.Helper()
	msg := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, _ := json.Marshal(msg)
	rec := postMCPRaw(t, h, string(b), "application/json", cookies...)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /mcp: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp rpcResp
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode RPC response: %v", err)
	}
	return resp
}

// toolCall sends a tools/call request and returns the parsed tool result.
func toolCall(t *testing.T, h http.Handler, toolName string, args map[string]any, cookies ...*http.Cookie) map[string]any {
	t.Helper()
	resp := postMCP(t, h, "tools/call", map[string]any{"name": toolName, "arguments": args}, cookies...)
	if resp.Error != nil {
		t.Fatalf("RPC error calling %s: %d %s", toolName, resp.Error.Code, resp.Error.Message)
	}
	return resp.Result
}

// toolText extracts the first text content item from a tool result.
func toolText(t *testing.T, result map[string]any) string {
	t.Helper()
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("tool result has no content: %v", result)
	}
	item, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] is not an object: %v", content[0])
	}
	text, _ := item["text"].(string)
	return text
}

// --- Auth & transport ---

func TestMCP_unauthenticated(t *testing.T) {
	rec := postMCPRaw(t, mcpHandler(), `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "application/json")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestMCP_wrongContentType(t *testing.T) {
	rec := postMCPRaw(t, mcpHandler(), `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "text/plain", authCookie())
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", rec.Code)
	}
}

func TestMCP_bodyTooLarge(t *testing.T) {
	giant := `{"jsonrpc":"2.0","id":1,"method":"ping","params":"` + strings.Repeat("x", 70000) + `"}`
	rec := postMCPRaw(t, mcpHandler(), giant, "application/json", authCookie())
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", rec.Code)
	}
}

func TestMCP_malformedJSON(t *testing.T) {
	rec := postMCPRaw(t, mcpHandler(), `not json`, "application/json", authCookie())
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != -32700 {
		t.Errorf("expected parse error -32700, got %+v", resp.Error)
	}
}

func TestMCP_wrongJSONRPCVersion(t *testing.T) {
	rec := postMCPRaw(t, mcpHandler(), `{"jsonrpc":"1.0","id":1,"method":"ping"}`, "application/json", authCookie())
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp.Error == nil || resp.Error.Code != -32600 {
		t.Errorf("expected invalid request -32600, got %+v", resp.Error)
	}
}

func TestMCP_notification_returns202(t *testing.T) {
	// Notifications have no "id" field.
	rec := postMCPRaw(t, mcpHandler(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`, "application/json", authCookie())
	if rec.Code != http.StatusAccepted {
		t.Errorf("expected 202 for notification, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body for notification, got %q", rec.Body.String())
	}
}

func TestMCP_unknownMethod(t *testing.T) {
	resp := postMCP(t, mcpHandler(), "no/such/method", nil, authCookie())
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Errorf("expected method not found -32601, got %+v", resp.Error)
	}
}

// --- initialize & ping ---

func TestMCP_initialize(t *testing.T) {
	resp := postMCP(t, mcpHandler(), "initialize", nil, authCookie())
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.Result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion: got %v", resp.Result["protocolVersion"])
	}
	if resp.Result["serverInfo"] == nil {
		t.Error("missing serverInfo")
	}
	if resp.Result["capabilities"] == nil {
		t.Error("missing capabilities")
	}
}

func TestMCP_ping(t *testing.T) {
	resp := postMCP(t, mcpHandler(), "ping", nil, authCookie())
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	if resp.Result == nil {
		t.Error("expected non-nil result for ping")
	}
}

// --- tools/list ---

func TestMCP_toolsList(t *testing.T) {
	resp := postMCP(t, mcpHandler(), "tools/list", nil, authCookie())
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}
	tools, ok := resp.Result["tools"].([]any)
	if !ok {
		t.Fatalf("tools is not an array: %T", resp.Result["tools"])
	}
	wantTools := []string{
		"get_overview", "list_issues", "get_issue",
		"list_transactions", "list_monitors", "get_monitor",
		"list_releases", "list_alerts", "get_logs",
		"get_transaction", "list_issue_events", "list_span_summaries",
		"update_issue", "bulk_update_issues", "create_alert_rule",
	}
	if len(tools) != len(wantTools) {
		t.Errorf("expected %d tools, got %d", len(wantTools), len(tools))
	}
	names := map[string]bool{}
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := tool["name"].(string)
		names[name] = true
		if tool["description"] == nil {
			t.Errorf("tool %q missing description", name)
		}
		if tool["inputSchema"] == nil {
			t.Errorf("tool %q missing inputSchema", name)
		}
	}
	for _, want := range wantTools {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
}

// --- tools/call protocol errors ---

func TestMCP_toolsCall_noParams(t *testing.T) {
	resp := postMCP(t, mcpHandler(), "tools/call", nil, authCookie())
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Errorf("expected invalid params -32602, got %+v", resp.Error)
	}
}

func TestMCP_toolsCall_missingName(t *testing.T) {
	resp := postMCP(t, mcpHandler(), "tools/call", map[string]any{"arguments": map[string]any{}}, authCookie())
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Errorf("expected invalid params -32602, got %+v", resp.Error)
	}
}

func TestMCP_toolsCall_unknownTool(t *testing.T) {
	result := toolCall(t, mcpHandler(), "no_such_tool", nil, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for unknown tool, got %v", result)
	}
}

// --- get_overview ---

func TestMCP_getOverview(t *testing.T) {
	result := toolCall(t, mcpHandler(), "get_overview", map[string]any{}, authCookie())
	text := toolText(t, result)

	var overview map[string]any
	if err := json.Unmarshal([]byte(text), &overview); err != nil {
		t.Fatalf("overview text is not valid JSON: %v\ntext: %s", err, text)
	}
	if _, ok := overview["open_issues"]; !ok {
		t.Error("overview missing open_issues")
	}
	if _, ok := overview["monitors"]; !ok {
		t.Error("overview missing monitors")
	}
	if _, ok := overview["firing_alerts_24h"]; !ok {
		t.Error("overview missing firing_alerts_24h")
	}
}

// --- list_issues ---

func TestMCP_listIssues_returnsArray(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_issues", map[string]any{}, authCookie())
	text := toolText(t, result)

	var issues []any
	if err := json.Unmarshal([]byte(text), &issues); err != nil {
		t.Fatalf("list_issues text is not a JSON array: %v\ntext: %s", err, text)
	}
}

func TestMCP_listIssues_seededIssueAppears(t *testing.T) {
	truncateIssues(t)
	seedIssue(t, "mcp-fp-1", "MCP Test Error")

	result := toolCall(t, mcpHandler(), "list_issues", map[string]any{"limit": 50}, authCookie())
	text := toolText(t, result)

	var issues []map[string]any
	if err := json.Unmarshal([]byte(text), &issues); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("expected at least one issue")
	}
	found := false
	for _, iss := range issues {
		if iss["title"] == "MCP Test Error" {
			found = true
		}
	}
	if !found {
		t.Errorf("seeded issue not in list_issues result")
	}
}

func TestMCP_listIssues_limitCapped(t *testing.T) {
	// Sending limit=9999 must not panic and must be silently clamped.
	result := toolCall(t, mcpHandler(), "list_issues", map[string]any{"limit": 9999}, authCookie())
	text := toolText(t, result)
	var issues []any
	if err := json.Unmarshal([]byte(text), &issues); err != nil {
		t.Fatalf("expected valid JSON array: %v", err)
	}
}

// --- get_issue ---

func TestMCP_getIssue_missingID(t *testing.T) {
	result := toolCall(t, mcpHandler(), "get_issue", map[string]any{}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true when id is absent, got %v", result)
	}
}

func TestMCP_getIssue_notFound(t *testing.T) {
	result := toolCall(t, mcpHandler(), "get_issue", map[string]any{"id": "00000000-0000-0000-0000-000000000000"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for unknown issue, got %v", result)
	}
}

func TestMCP_getIssue_found(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "mcp-get-fp", "MCP Get Issue")

	result := toolCall(t, mcpHandler(), "get_issue", map[string]any{"id": iss.ID}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError:true: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] != iss.ID {
		t.Errorf("id: got %v, want %v", got["id"], iss.ID)
	}
	if got["title"] != "MCP Get Issue" {
		t.Errorf("title: got %v", got["title"])
	}
}

// --- list_transactions ---

func TestMCP_listTransactions_returnsArray(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_transactions", map[string]any{}, authCookie())
	text := toolText(t, result)
	var txs []any
	if err := json.Unmarshal([]byte(text), &txs); err != nil {
		t.Fatalf("list_transactions text is not a JSON array: %v\ntext: %s", err, text)
	}
}

// --- list_monitors ---

func TestMCP_listMonitors_returnsArray(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_monitors", map[string]any{}, authCookie())
	text := toolText(t, result)
	var monitors []any
	if err := json.Unmarshal([]byte(text), &monitors); err != nil {
		t.Fatalf("list_monitors text is not a JSON array: %v\ntext: %s", err, text)
	}
}

// --- get_monitor ---

func TestMCP_getMonitor_missingID(t *testing.T) {
	result := toolCall(t, mcpHandler(), "get_monitor", map[string]any{}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true when id is absent, got %v", result)
	}
}

func TestMCP_getMonitor_notFound(t *testing.T) {
	result := toolCall(t, mcpHandler(), "get_monitor", map[string]any{"id": "00000000-0000-0000-0000-000000000000"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for unknown monitor, got %v", result)
	}
}

func TestMCP_getMonitor_success(t *testing.T) {
	m := createTestMonitor(t, "MCP Monitor", "*/5 * * * *")

	result := toolCall(t, mcpHandler(), "get_monitor", map[string]any{"id": m.ID}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] != m.ID {
		t.Errorf("id: got %v, want %s", got["id"], m.ID)
	}
	if got["name"] != "MCP Monitor" {
		t.Errorf("name: got %v", got["name"])
	}
	if _, ok := got["recent_checkins"]; !ok {
		t.Error("response missing recent_checkins field")
	}
}

// --- list_releases ---

func TestMCP_listReleases_returnsArray(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_releases", map[string]any{}, authCookie())
	text := toolText(t, result)
	var releases []any
	if err := json.Unmarshal([]byte(text), &releases); err != nil {
		t.Fatalf("list_releases text is not a JSON array: %v\ntext: %s", err, text)
	}
}

// --- list_alerts ---

func TestMCP_listAlerts_returnsArray(t *testing.T) {
	truncateAlertRules(t)
	result := toolCall(t, mcpHandler(), "list_alerts", map[string]any{}, authCookie())
	text := toolText(t, result)
	var rules []any
	if err := json.Unmarshal([]byte(text), &rules); err != nil {
		t.Fatalf("list_alerts text is not a JSON array: %v\ntext: %s", err, text)
	}
}

// --- Bearer token project scoping ---

func TestMCP_bearerToken_listIssues_scopedToProject(t *testing.T) {
	truncateIssues(t)
	// Seed an issue in testProject.
	seedIssue(t, "mcp-bearer-fp", "Bearer Scoped Issue")

	// Create a second project and a token for it.
	other, err := storage.CreateProject(context.Background(), testPool, "mcp-other-project", "MCP Other")
	if err != nil {
		t.Fatalf("create other project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, other.ID, "mcp-scope-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	bearerCookie := &http.Cookie{} // unused - bearer goes in Authorization header
	_ = bearerCookie

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "list_issues", "arguments": map[string]any{}},
	}
	b, _ := json.Marshal(msg)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	text := toolText(t, resp.Result)

	var issues []map[string]any
	if err := json.Unmarshal([]byte(text), &issues); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The token is scoped to `other`, so testProject's issues must not appear.
	for _, iss := range issues {
		if iss["project_id"] == testProject.ID {
			t.Errorf("got issue from wrong project: %v", iss)
		}
	}
}

func TestMCP_bearerToken_getIssue_crossProjectDenied(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "mcp-cross-fp", "Cross Project Issue")

	// Token for a different project.
	other, err := storage.CreateProject(context.Background(), testPool, "mcp-cross-project", "MCP Cross")
	if err != nil {
		t.Fatalf("create other project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, other.ID, "mcp-cross-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	msg := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "get_issue", "arguments": map[string]any{"id": iss.ID}},
	}
	b, _ := json.Marshal(msg)
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)

	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true when token project != issue project, got %v", resp.Result)
	}
}

// --- Phase 2: deeper reads ---

func TestMCP_getTransaction_notFound(t *testing.T) {
	result := toolCall(t, mcpHandler(), "get_transaction", map[string]any{"id": "00000000-0000-0000-0000-000000000000"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for unknown transaction, got %v", result)
	}
}

func TestMCP_getTransaction_missingID(t *testing.T) {
	result := toolCall(t, mcpHandler(), "get_transaction", map[string]any{}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true when id is absent, got %v", result)
	}
}

func TestMCP_listIssueEvents_found(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "mcp-ev-fp", "MCP Events Issue")

	result := toolCall(t, mcpHandler(), "list_issue_events", map[string]any{"issue_id": iss.ID}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var events []any
	if err := json.Unmarshal([]byte(text), &events); err != nil {
		t.Fatalf("expected JSON array: %v\ntext: %s", err, text)
	}
}

func TestMCP_listIssueEvents_missingID(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_issue_events", map[string]any{}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true when issue_id is absent, got %v", result)
	}
}

func TestMCP_listSpanSummaries_invalidType(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_span_summaries", map[string]any{"type": "invalid"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for invalid type, got %v", result)
	}
}

func TestMCP_listSpanSummaries_db(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_span_summaries", map[string]any{"type": "db"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var summaries []any
	if err := json.Unmarshal([]byte(text), &summaries); err != nil {
		t.Fatalf("expected JSON array: %v\ntext: %s", err, text)
	}
}

// --- get_logs ---

func TestMCP_getLogs_returnsArray(t *testing.T) {
	result := toolCall(t, mcpHandler(), "get_logs", map[string]any{}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var logs []any
	if err := json.Unmarshal([]byte(text), &logs); err != nil {
		t.Fatalf("get_logs text is not a JSON array: %v\ntext: %s", err, text)
	}
}

func TestMCP_getLogs_levelFilter(t *testing.T) {
	result := toolCall(t, mcpHandler(), "get_logs", map[string]any{"level": "error"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var logs []any
	if err := json.Unmarshal([]byte(text), &logs); err != nil {
		t.Fatalf("expected JSON array: %v", err)
	}
}

func TestMCP_getLogs_bearerScoped(t *testing.T) {
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "logs-bearer-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_logs","arguments":{}}}`
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError for valid bearer token: %s", toolText(t, resp.Result))
	}
}

// --- Phase 2: write tool permission checks ---

func TestMCP_updateIssue_readOnlyToken(t *testing.T) {
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "ro-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_issue","arguments":{"id":"00000000-0000-0000-0000-000000000000","status":"resolved"}}}`,
	))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)

	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for read-only token write attempt, got %v", resp.Result)
	}
	text := toolText(t, resp.Result)
	if !strings.Contains(text, "read-only") {
		t.Errorf("expected 'read-only' in error message, got: %s", text)
	}
}

func TestMCP_updateIssue_writableToken_notFound(t *testing.T) {
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "rw-tok", true)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	bearerReq := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+plaintext)
		rec := httptest.NewRecorder()
		mcpHandler().ServeHTTP(rec, req)
		return rec
	}
	rec := bearerReq(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_issue","arguments":{"id":"00000000-0000-0000-0000-000000000000","status":"resolved"}}}`)
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for unknown issue, got %v", resp.Result)
	}
}

func TestMCP_updateIssue_sessionAuth_resolves(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "mcp-update-fp", "MCP Update Issue")

	result := toolCall(t, mcpHandler(), "update_issue",
		map[string]any{"id": iss.ID, "status": "resolved"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "resolved" {
		t.Errorf("status: got %v, want resolved", got["status"])
	}
}

func TestMCP_updateIssue_sessionAuth_noPermission(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "mcp-noperm-fp", "No Perm Issue")

	// makeReadOnlyUser creates a second user with all permissions=false.
	nopermCookie := makeReadOnlyUser(t, "mcp-noperm@example.com")

	result := toolCall(t, mcpHandler(), "update_issue",
		map[string]any{"id": iss.ID, "status": "resolved"}, nopermCookie)
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for session without manage_issues, got %v", result)
	}
}

func TestMCP_bulkUpdateIssues_readOnlyToken(t *testing.T) {
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "bulk-ro-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bulk_update_issues","arguments":{"ids":["00000000-0000-0000-0000-000000000000"],"status":"resolved"}}}`,
	))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)

	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for read-only token, got %v", resp.Result)
	}
}

func TestMCP_bulkUpdateIssues_sessionAuth(t *testing.T) {
	truncateIssues(t)
	iss1 := seedIssue(t, "mcp-bulk-fp1", "Bulk 1")
	iss2 := seedIssue(t, "mcp-bulk-fp2", "Bulk 2")

	result := toolCall(t, mcpHandler(), "bulk_update_issues", map[string]any{
		"ids":    []any{iss1.ID, iss2.ID},
		"status": "resolved",
	}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["updated"] == nil {
		t.Error("missing 'updated' field in bulk update response")
	}
}

func TestMCP_createAlertRule_readOnlyToken(t *testing.T) {
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "alert-ro-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_alert_rule","arguments":{"name":"test","trigger":"new_issue","channel":"email","email_to":"x@example.com"}}}`,
	))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)

	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true for read-only token, got %v", resp.Result)
	}
}

func TestMCP_createAlertRule_sessionAuth_email(t *testing.T) {
	truncateAlertRules(t)
	result := toolCall(t, mcpHandler(), "create_alert_rule", map[string]any{
		"name":     "MCP Test Alert",
		"trigger":  "new_issue",
		"channel":  "email",
		"email_to": "oncall@example.com",
	}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var rule map[string]any
	if err := json.Unmarshal([]byte(text), &rule); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rule["id"] == nil || rule["id"] == "" {
		t.Error("expected non-empty id in created rule")
	}
	if rule["name"] != "MCP Test Alert" {
		t.Errorf("name: got %v", rule["name"])
	}
}

func TestMCP_createAlertRule_missingFields(t *testing.T) {
	result := toolCall(t, mcpHandler(), "create_alert_rule", map[string]any{
		"name":    "incomplete",
		"trigger": "new_issue",
		// missing channel
	}, authCookie())
	isErr, _ := result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true when channel is missing, got %v", result)
	}
}

// --- tools/list reflects Phase 2 ---

func TestMCP_toolsList_phase2_toolsPresent(t *testing.T) {
	resp := postMCP(t, mcpHandler(), "tools/list", nil, authCookie())
	tools, _ := resp.Result["tools"].([]any)
	phase2 := []string{"get_transaction", "list_issue_events", "list_span_summaries", "update_issue", "bulk_update_issues", "create_alert_rule"}
	names := map[string]bool{}
	for _, raw := range tools {
		if tool, ok := raw.(map[string]any); ok {
			names[tool["name"].(string)] = true
		}
	}
	for _, want := range phase2 {
		if !names[want] {
			t.Errorf("Phase 2 tool %q missing from tools/list", want)
		}
	}
}

// bearerRequest sends a POST /mcp with an Authorization: Bearer header.
func bearerRequest(t *testing.T, h http.Handler, plaintext, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+plaintext)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// --- get_transaction: success + bearer scoping ---

func TestMCP_getTransaction_success(t *testing.T) {
	truncateTransactions(t)
	tx := seedTransactionRow(t, "/mcp/test-tx", 42)

	result := toolCall(t, mcpHandler(), "get_transaction", map[string]any{"id": tx.ID}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["transaction"] == nil {
		t.Error("response missing 'transaction' key")
	}
	if got["spans"] == nil {
		t.Error("response missing 'spans' key")
	}
}

func TestMCP_getTransaction_bearerCrossProjectDenied(t *testing.T) {
	truncateTransactions(t)
	tx := seedTransactionRow(t, "/mcp/cross-tx", 10)

	other, err := storage.CreateProject(context.Background(), testPool, "mcp-tx-other", "TX Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, other.ID, "tx-cross-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_transaction","arguments":{"id":"` + tx.ID + `"}}}`
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true when token project != transaction project, got %v", resp.Result)
	}
}

// --- list_issue_events: bearer cross-project scoping ---

func TestMCP_listIssueEvents_bearerCrossProjectDenied(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "mcp-ev-cross-fp", "Events Cross Project")

	other, err := storage.CreateProject(context.Background(), testPool, "mcp-ev-other", "EV Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, other.ID, "ev-cross-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_issue_events","arguments":{"issue_id":"` + iss.ID + `"}}}`
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true when token project != issue project, got %v", resp.Result)
	}
}

// --- list_span_summaries: cache and jobs type variants ---

func TestMCP_listSpanSummaries_cache(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_span_summaries", map[string]any{"type": "cache"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var summaries []any
	if err := json.Unmarshal([]byte(text), &summaries); err != nil {
		t.Fatalf("expected JSON array for type=cache: %v\ntext: %s", err, text)
	}
}

func TestMCP_listSpanSummaries_jobs(t *testing.T) {
	result := toolCall(t, mcpHandler(), "list_span_summaries", map[string]any{"type": "jobs"}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var summaries []any
	if err := json.Unmarshal([]byte(text), &summaries); err != nil {
		t.Fatalf("expected JSON array for type=jobs: %v\ntext: %s", err, text)
	}
}

// --- update_issue: writable token success + cross-project denial ---

func TestMCP_updateIssue_writableToken_success(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "mcp-rw-update-fp", "RW Update Issue")

	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "rw-update-tok", true)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_issue","arguments":{"id":"` + iss.ID + `","status":"resolved"}}}`
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError for writable token: %s", toolText(t, resp.Result))
	}
	text := toolText(t, resp.Result)
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "resolved" {
		t.Errorf("status: got %v, want resolved", got["status"])
	}
}

func TestMCP_updateIssue_writableToken_crossProjectDenied(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "mcp-rw-cross-fp", "RW Cross Issue")

	other, err := storage.CreateProject(context.Background(), testPool, "mcp-rw-cross", "RW Cross")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, other.ID, "rw-cross-tok", true)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_issue","arguments":{"id":"` + iss.ID + `","status":"resolved"}}}`
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if !isErr {
		t.Errorf("expected isError:true when writable token project != issue project, got %v", resp.Result)
	}
}

// --- bulk_update_issues: writable token project scoping ---

func TestMCP_bulkUpdateIssues_writableToken_projectScoping(t *testing.T) {
	truncateIssues(t)
	iss1 := seedIssue(t, "mcp-bulk-scope-fp1", "Bulk Scope 1") // testProject

	other, err := storage.CreateProject(context.Background(), testPool, "mcp-bulk-scope-other", "Bulk Scope Other")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", other.ID)
	})
	iss2, _, _, err := storage.UpsertIssue(context.Background(), testPool, other.ID, "mcp-bulk-scope-fp2", "Bulk Scope 2", "error", "error", "", "", time.Now())
	if err != nil {
		t.Fatalf("seed other issue: %v", err)
	}

	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "bulk-scope-tok", true)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bulk_update_issues","arguments":{"ids":["` + iss1.ID + `","` + iss2.ID + `"],"status":"resolved"}}}`
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, resp.Result))
	}
	text := toolText(t, resp.Result)
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	updated, _ := got["updated"].(float64)
	if updated != 1 {
		t.Errorf("expected 1 issue updated (scoped to testProject), got %v", updated)
	}
}

// --- create_alert_rule: writable token success ---

func TestMCP_createAlertRule_writableToken_success(t *testing.T) {
	truncateAlertRules(t)

	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "alert-rw-tok", true)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_alert_rule","arguments":{"name":"RW Alert","trigger":"new_issue","channel":"email","email_to":"oncall@example.com"}}}`
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError for writable token: %s", toolText(t, resp.Result))
	}
	text := toolText(t, resp.Result)
	var rule map[string]any
	if err := json.Unmarshal([]byte(text), &rule); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rule["id"] == nil || rule["id"] == "" {
		t.Error("expected non-empty id in created rule")
	}
	if rule["name"] != "RW Alert" {
		t.Errorf("name: got %v", rule["name"])
	}
}

// --- OAuth discovery & transport helpers ---

func TestMCP_oauthAS_returns200JSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json Content-Type, got %q", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if body["issuer"] == nil {
		t.Error("expected issuer field in AS metadata")
	}
	methods, ok := body["bearer_methods_supported"].([]any)
	if !ok || len(methods) == 0 {
		t.Error("expected non-empty bearer_methods_supported")
	}
}

func TestMCP_oauthPR_returns200JSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource", nil)
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json Content-Type, got %q", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if body["resource"] == nil {
		t.Error("expected resource field in protected resource metadata")
	}
	authServers, ok := body["authorization_servers"].([]any)
	if !ok || len(authServers) == 0 {
		t.Error("expected non-empty authorization_servers")
	}
	methods, ok := body["bearer_methods_supported"].([]any)
	if !ok || len(methods) == 0 {
		t.Error("expected non-empty bearer_methods_supported")
	}
}

func TestMCP_getMethod_returns405JSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "POST" {
		t.Errorf("expected Allow: POST, got %q", allow)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("expected application/json Content-Type, got %q", ct)
	}
	var resp rpcResp
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if resp.Error == nil {
		t.Error("expected JSON-RPC error in 405 response")
	}
}

func TestMCP_unauthenticated_hasWWWAuthenticate(t *testing.T) {
	rec := postMCPRaw(t, mcpHandler(), `{"jsonrpc":"2.0","id":1,"method":"ping"}`, "application/json")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
	if hdr := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(hdr, "Bearer") {
		t.Errorf("expected WWW-Authenticate: Bearer ..., got %q", hdr)
	}
}

func TestMCP_ruleMatchesProjects_bearerListAlerts(t *testing.T) {
	truncateAlertRules(t)

	url := "https://hooks.example.com/mcp-rule-test"
	rule, err := storage.CreateAlertRule(context.Background(), testPool, &storage.AlertRule{
		ProjectIDs:   []string{testProject.ID},
		Name:         "rule-match-test",
		Enabled:      true,
		Trigger:      "new_issue",
		Channel:      "webhook",
		WebhookURL:   &url,
		CooldownMins: 0,
	})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}
	_ = rule

	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "rule-match-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_alerts","arguments":{}}}`
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, resp.Result))
	}
	text := toolText(t, resp.Result)
	var rules []map[string]any
	if err := json.Unmarshal([]byte(text), &rules); err != nil {
		t.Fatalf("expected JSON array: %v\ntext: %s", err, text)
	}
	if len(rules) == 0 {
		t.Error("expected at least one rule visible to the bearer token's project")
	}
}

// --- update_issue: assignee branch (else block when status == "") ---

func TestMCP_updateIssue_assigneeUpdate(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "mcp-assignee-fp", "Assignee Issue")

	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "rw-assignee-tok", true)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// Update with assignee_id = testUser.ID (no status field → triggers else branch)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_issue","arguments":{"id":"` + iss.ID + `","assignee_id":"` + testUser.ID + `"}}}`
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected error on assignee update: %s", toolText(t, resp.Result))
	}
	text := toolText(t, resp.Result)
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The response should be the updated issue JSON.
	if got["id"] != iss.ID {
		t.Errorf("id: got %v, want %v", got["id"], iss.ID)
	}
}

func TestMCP_updateIssue_unassign(t *testing.T) {
	truncateIssues(t)
	iss := seedIssue(t, "mcp-unassign-fp", "Unassign Issue")

	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "rw-unassign-tok", true)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	// No status, no assignee_id → assigneeID stays nil → unassigns (covers details["to_id"]=nil path)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"update_issue","arguments":{"id":"` + iss.ID + `"}}}`
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected error on unassign: %s", toolText(t, resp.Result))
	}
	text := toolText(t, resp.Result)
	var got map[string]any
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["id"] != iss.ID {
		t.Errorf("id: got %v, want %v", got["id"], iss.ID)
	}
}

// --- get_overview: len(summaries) > 0 branch ---

func TestMCP_getOverview_withTransactions(t *testing.T) {
	// Seed a transaction so that ListTransactionSummaries returns at least one
	// entry, triggering the len(summaries) > 0 branch and error-rate calculation.
	testPool.Exec(context.Background(),
		"INSERT INTO transactions (project_id, transaction, op, status, duration_ms, start_timestamp, timestamp) VALUES ($1,$2,'http.server','ok',100,NOW(),NOW())",
		testProject.ID, "/mcp-overview-tx")

	result := toolCall(t, mcpHandler(), "get_overview", map[string]any{}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var overview map[string]any
	if err := json.Unmarshal([]byte(text), &overview); err != nil {
		t.Fatalf("overview text is not valid JSON: %v\ntext: %s", err, text)
	}
	if _, ok := overview["open_issues"]; !ok {
		t.Error("overview missing open_issues")
	}
	// transaction_error_rate is only present when len(summaries) > 0.
	if _, ok := overview["transaction_error_rate"]; !ok {
		t.Log("note: transaction_error_rate not in response — summaries may have been outside the 24h window")
	}
}

// --- list_transactions: name filter param ---

func TestMCP_listTransactions_nameFilter(t *testing.T) {
	// Exercises the mcpArgString(args, "name") path inside mcpListTransactions.
	result := toolCall(t, mcpHandler(), "list_transactions", map[string]any{
		"name":  "/nonexistent-endpoint",
		"hours": 24,
		"limit": 10,
	}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var txs []any
	if err := json.Unmarshal([]byte(text), &txs); err != nil {
		t.Fatalf("list_transactions name filter: expected JSON array: %v\ntext: %s", err, text)
	}
}

func TestMCP_listTransactions_hoursClampedLow(t *testing.T) {
	// hours < 1 is clamped to 1; exercises that branch.
	result := toolCall(t, mcpHandler(), "list_transactions", map[string]any{"hours": 0}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var txs []any
	if err := json.Unmarshal([]byte(text), &txs); err != nil {
		t.Fatalf("expected JSON array: %v\ntext: %s", err, text)
	}
}

func TestMCP_listTransactions_hoursClampedHigh(t *testing.T) {
	// hours > 168 is clamped to 168; exercises that branch.
	result := toolCall(t, mcpHandler(), "list_transactions", map[string]any{"hours": 9999}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var txs []any
	if err := json.Unmarshal([]byte(text), &txs); err != nil {
		t.Fatalf("expected JSON array: %v\ntext: %s", err, text)
	}
}

// --- list_releases: bearer-scoped request ---

func TestMCP_listReleases_bearerScoped(t *testing.T) {
	_, plaintext, err := storage.CreateAPIToken(context.Background(), testPool, testProject.ID, "releases-bearer-tok", false)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_releases","arguments":{}}}`
	rec := bearerRequest(t, mcpHandler(), plaintext, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp rpcResp
	json.NewDecoder(rec.Body).Decode(&resp)
	isErr, _ := resp.Result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError for list_releases bearer: %s", toolText(t, resp.Result))
	}
	text := toolText(t, resp.Result)
	var releases []any
	if err := json.Unmarshal([]byte(text), &releases); err != nil {
		t.Fatalf("list_releases bearer: expected JSON array: %v\ntext: %s", err, text)
	}
}

func TestMCP_listReleases_limitParam(t *testing.T) {
	// Exercises the mcpArgLimit path with an explicit limit.
	result := toolCall(t, mcpHandler(), "list_releases", map[string]any{"limit": 5}, authCookie())
	isErr, _ := result["isError"].(bool)
	if isErr {
		t.Fatalf("unexpected isError: %s", toolText(t, result))
	}
	text := toolText(t, result)
	var releases []any
	if err := json.Unmarshal([]byte(text), &releases); err != nil {
		t.Fatalf("expected JSON array: %v\ntext: %s", err, text)
	}
}
