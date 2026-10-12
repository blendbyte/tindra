package api_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// modernMCP sends a 2026-07-28 request with the per-request _meta and the
// mirrored headers a conforming client sets. edit can change the request first.
func modernMCP(t *testing.T, method string, params map[string]any, edit func(*http.Request)) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	params["_meta"] = map[string]any{
		"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
		"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "test", "version": "1"},
	}
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": method, "params": params})
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", method)
	if name, ok := params["name"].(string); ok {
		req.Header.Set("Mcp-Name", name)
	}
	req.AddCookie(authCookie())
	if edit != nil {
		edit(req)
	}
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode %s response %q: %v", method, rec.Body.String(), err)
	}
	return rec, resp
}

func errorCode(resp map[string]any) float64 {
	e, _ := resp["error"].(map[string]any)
	code, _ := e["code"].(float64)
	return code
}

func TestMCPModern_discover(t *testing.T) {
	rec, resp := modernMCP(t, "server/discover", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	result := resp["result"].(map[string]any)
	versions := result["supportedVersions"].([]any)
	if versions[0] != "2026-07-28" || len(versions) != 4 {
		t.Errorf("supportedVersions = %v", versions)
	}
	if result["resultType"] != "complete" || result["cacheScope"] != "public" || result["ttlMs"] == nil {
		t.Errorf("result fields: %v", result)
	}
	meta := result["_meta"].(map[string]any)
	if info := meta["io.modelcontextprotocol/serverInfo"].(map[string]any); info["name"] != "tindra" {
		t.Errorf("serverInfo = %v", info)
	}
	if _, ok := result["capabilities"].(map[string]any)["tools"]; !ok {
		t.Error("capabilities lack tools")
	}
}

func TestMCPModern_toolsListAndCall(t *testing.T) {
	_, resp := modernMCP(t, "tools/list", nil, nil)
	result := resp["result"].(map[string]any)
	if result["resultType"] != "complete" || result["cacheScope"] != "private" {
		t.Errorf("tools/list result fields: %v", result)
	}
	if len(result["tools"].([]any)) == 0 {
		t.Error("no tools listed")
	}

	rec, resp := modernMCP(t, "tools/call", map[string]any{"name": "list_issues", "arguments": map[string]any{}}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/call status %d: %s", rec.Code, rec.Body.String())
	}
	call := resp["result"].(map[string]any)
	if call["resultType"] != "complete" || call["content"] == nil {
		t.Errorf("tools/call result: %v", call)
	}
}

func TestMCPModern_acceptsBase64ToolName(t *testing.T) {
	rec, _ := modernMCP(t, "tools/call", map[string]any{"name": "list_issues", "arguments": map[string]any{}}, func(r *http.Request) {
		r.Header.Set("Mcp-Name", "=?base64?"+base64.StdEncoding.EncodeToString([]byte("list_issues"))+"?=")
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMCPModern_rejectsMismatchedOrMissingHeaders(t *testing.T) {
	cases := map[string]func(*http.Request){
		"missing version header": func(r *http.Request) { r.Header.Del("MCP-Protocol-Version") },
		"different version":      func(r *http.Request) { r.Header.Set("MCP-Protocol-Version", "2025-11-25") },
		"missing method header":  func(r *http.Request) { r.Header.Del("Mcp-Method") },
		"different method":       func(r *http.Request) { r.Header.Set("Mcp-Method", "tools/list") },
		"different tool name":    func(r *http.Request) { r.Header.Set("Mcp-Name", "delete_everything") },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			rec, resp := modernMCP(t, "tools/call", map[string]any{"name": "list_issues", "arguments": map[string]any{}}, edit)
			if rec.Code != http.StatusBadRequest || errorCode(resp) != -32020 {
				t.Errorf("got %d %v, want 400 with HeaderMismatch -32020", rec.Code, resp["error"])
			}
		})
	}
}

func TestMCPModern_unsupportedVersionListsSupported(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2099-01-01")
	req.Header.Set("Mcp-Method", "tools/list")
	req.AddCookie(authCookie())
	rec := httptest.NewRecorder()
	mcpHandler().ServeHTTP(rec, req)

	var resp struct {
		Error struct {
			Code int `json:"code"`
			Data struct {
				Supported []string `json:"supported"`
				Requested string   `json:"requested"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusBadRequest || resp.Error.Code != -32022 {
		t.Fatalf("got %d %+v, want 400 with -32022", rec.Code, resp.Error)
	}
	if resp.Error.Data.Requested != "2099-01-01" || resp.Error.Data.Supported[0] != "2026-07-28" {
		t.Errorf("data = %+v", resp.Error.Data)
	}
}

func TestMCPModern_missingCapabilitiesAndUnknownMethod(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/list")
	req.AddCookie(authCookie())
	missing := httptest.NewRecorder()
	mcpHandler().ServeHTTP(missing, req)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "-32602") {
		t.Errorf("missing clientCapabilities: got %d %s", missing.Code, missing.Body.String())
	}

	// ping and initialize do not exist in the stateless revision.
	for _, method := range []string{"ping", "initialize", "no/such/method"} {
		rec, resp := modernMCP(t, method, nil, nil)
		if rec.Code != http.StatusNotFound || errorCode(resp) != -32601 {
			t.Errorf("%s: got %d %v, want 404 with -32601", method, rec.Code, resp["error"])
		}
	}
}

func TestMCPLegacy_versionHeader(t *testing.T) {
	send := func(header string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		if header != "" {
			req.Header.Set("MCP-Protocol-Version", header)
		}
		req.AddCookie(authCookie())
		rec := httptest.NewRecorder()
		mcpHandler().ServeHTTP(rec, req)
		return rec
	}
	// Clients before 2025-06-18 send no header; later ones send the negotiated version.
	for _, header := range []string{"", "2025-03-26", "2025-11-25"} {
		if rec := send(header); rec.Code != http.StatusOK {
			t.Errorf("header %q: got %d, want 200", header, rec.Code)
		}
	}
	if rec := send("2024-01-01"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "-32022") {
		t.Errorf("unknown version: got %d %s", rec.Code, rec.Body.String())
	}
	// A modern version without _meta is a malformed modern request.
	if rec := send("2026-07-28"); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "-32602") {
		t.Errorf("modern header without _meta: got %d %s", rec.Code, rec.Body.String())
	}
}
