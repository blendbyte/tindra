package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func trimForTest(t *testing.T, payload string) (map[string]any, []string) {
	t.Helper()
	raw, omitted := mcpTrimEvent(json.RawMessage(payload))
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode trimmed: %v (%s)", err, raw)
	}
	return out, omitted
}

func firstStacktrace(t *testing.T, out map[string]any, iface string) map[string]any {
	t.Helper()
	container, _ := out[iface].(map[string]any)
	values, _ := container["values"].([]any)
	if len(values) == 0 {
		t.Fatalf("%s has no values: %v", iface, out[iface])
	}
	val, _ := values[0].(map[string]any)
	st, _ := val["stacktrace"].(map[string]any)
	return st
}

func frameFunctions(st map[string]any) []string {
	frames, _ := st["frames"].([]any)
	names := make([]string, 0, len(frames))
	for _, f := range frames {
		frame, _ := f.(map[string]any)
		names = append(names, fmt.Sprint(frame["function"]))
	}
	return names
}

func TestMCPTrimEvent_keepsInAppAndCrashFrames(t *testing.T) {
	out, omitted := trimForTest(t, `{
		"exception": {"values": [{
			"type": "Error",
			"value": "boom",
			"mechanism": {"type": "onerror", "handled": false},
			"raw_stacktrace": {"frames": [{"function": "min"}]},
			"stacktrace": {"registers": {"rip": "0x1"}, "frames": [
				{"function": "main", "in_app": false},
				{"function": "handler", "in_app": true, "vars": {"secret": "x"}, "context_line": "go()"},
				{"function": "middleware", "in_app": false},
				{"function": "throwIt", "in_app": false, "vars": {"n": 1}}
			]}
		}]}
	}`)

	st := firstStacktrace(t, out, "exception")
	if got := frameFunctions(st); !slices.Equal(got, []string{"handler", "throwIt"}) {
		t.Errorf("frames: got %v, want [handler throwIt]", got)
	}
	if st["frames_omitted"] != float64(2) {
		t.Errorf("frames_omitted: got %v, want 2", st["frames_omitted"])
	}
	if _, ok := st["registers"]; ok {
		t.Error("registers should be dropped")
	}
	frames, _ := st["frames"].([]any)
	for _, f := range frames {
		if frame, _ := f.(map[string]any); frame["vars"] != nil {
			t.Errorf("vars should be dropped: %v", frame)
		}
	}
	if handler, _ := frames[0].(map[string]any); handler["context_line"] != "go()" {
		t.Errorf("context_line should be kept: %v", handler)
	}
	exc, _ := out["exception"].(map[string]any)
	val, _ := exc["values"].([]any)[0].(map[string]any)
	if val["type"] != "Error" || val["value"] != "boom" || val["mechanism"] == nil {
		t.Errorf("exception fields should be kept: %v", val)
	}
	if _, ok := val["raw_stacktrace"]; ok {
		t.Error("raw_stacktrace should be dropped")
	}
	if !slices.Equal(omitted, []string{"exception"}) {
		t.Errorf("omitted: got %v, want [exception]", omitted)
	}
}

func TestMCPTrimEvent_noInAppKeepsCrashEndFrames(t *testing.T) {
	frames := make([]string, 15)
	for i := range frames {
		frames[i] = fmt.Sprintf(`{"function":"f%d"}`, i)
	}
	out, _ := trimForTest(t, `{"exception":{"values":[{"stacktrace":{"frames":[`+strings.Join(frames, ",")+`]}}]}}`)

	st := firstStacktrace(t, out, "exception")
	got := frameFunctions(st)
	if len(got) != mcpTrimTailFrames || got[0] != "f5" || got[len(got)-1] != "f14" {
		t.Errorf("frames: got %v, want f5..f14", got)
	}
	if st["frames_omitted"] != float64(5) {
		t.Errorf("frames_omitted: got %v, want 5", st["frames_omitted"])
	}
}

func TestMCPTrimEvent_breadcrumbs(t *testing.T) {
	crumbs := make([]string, 15)
	for i := range crumbs {
		crumbs[i] = fmt.Sprintf(`{"message":"c%d"}`, i)
	}
	list := "[" + strings.Join(crumbs, ",") + "]"

	for name, payload := range map[string]string{
		"values form": `{"breadcrumbs":{"values":` + list + `}}`,
		"bare array":  `{"breadcrumbs":` + list + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			out, omitted := trimForTest(t, payload)
			bc, _ := out["breadcrumbs"].(map[string]any)
			values, _ := bc["values"].([]any)
			if len(values) != mcpTrimBreadcrumbs {
				t.Fatalf("breadcrumbs: got %d, want %d", len(values), mcpTrimBreadcrumbs)
			}
			first, _ := values[0].(map[string]any)
			last, _ := values[len(values)-1].(map[string]any)
			if first["message"] != "c5" || last["message"] != "c14" {
				t.Errorf("want the newest breadcrumbs c5..c14, got %v..%v", first["message"], last["message"])
			}
			if !slices.Equal(omitted, []string{"breadcrumbs"}) {
				t.Errorf("omitted: got %v, want [breadcrumbs]", omitted)
			}
		})
	}
}

func TestMCPTrimEvent_requestContextsAndDroppedSections(t *testing.T) {
	out, omitted := trimForTest(t, `{
		"level": "error",
		"release": "v1",
		"user": {"id": "u-1"},
		"tags": {"region": "eu"},
		"request": {"url": "https://example.com/a", "method": "POST", "headers": {"Cookie": "x"}, "data": {"card": "4111"}},
		"contexts": {
			"browser": {"name": "Safari", "version": "17.4", "type": "browser"},
			"trace": {"trace_id": "abc", "span_id": "def", "data": {"big": true}},
			"app": {"app_memory": 1024}
		},
		"modules": {"django": "5.2"},
		"extra": {"payload": "large"},
		"sdk": {"name": "sentry.python"},
		"debug_meta": {"images": []}
	}`)

	if req, _ := out["request"].(map[string]any); len(req) != 2 || req["url"] != "https://example.com/a" || req["method"] != "POST" {
		t.Errorf("request: got %v, want url and method only", out["request"])
	}
	contexts, _ := out["contexts"].(map[string]any)
	if browser, _ := contexts["browser"].(map[string]any); len(browser) != 2 || browser["name"] != "Safari" {
		t.Errorf("browser context: got %v", contexts["browser"])
	}
	if trace, _ := contexts["trace"].(map[string]any); len(trace) != 2 || trace["trace_id"] != "abc" {
		t.Errorf("trace context: got %v", contexts["trace"])
	}
	if _, ok := contexts["app"]; ok {
		t.Error("a context with no kept fields should be left out")
	}
	for _, key := range []string{"level", "release", "user", "tags"} {
		if out[key] == nil {
			t.Errorf("%s should be kept", key)
		}
	}
	for _, key := range []string{"modules", "extra", "sdk", "debug_meta"} {
		if _, ok := out[key]; ok {
			t.Errorf("%s should be dropped", key)
		}
	}
	want := []string{"contexts", "debug_meta", "extra", "modules", "request", "sdk"}
	if !slices.Equal(omitted, want) {
		t.Errorf("omitted: got %v, want %v", omitted, want)
	}
}

func TestMCPTrimEvent_threadsWithoutException(t *testing.T) {
	thread := func(id int, flags string) string {
		return fmt.Sprintf(`{"id":%d%s,"stacktrace":{"frames":[{"function":"t%d","in_app":true,"vars":{"a":1}}]}}`, id, flags, id)
	}
	cases := []struct {
		name    string
		threads []string
		wantID  float64
	}{
		{"crashed wins", []string{thread(1, `,"current":true`), thread(2, `,"crashed":true`)}, 2},
		{"current fallback", []string{thread(1, ""), thread(2, `,"current":true`)}, 2},
		{"first fallback", []string{thread(1, ""), thread(2, "")}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, omitted := trimForTest(t, `{"threads":{"values":[`+strings.Join(tc.threads, ",")+`]}}`)
			threads, _ := out["threads"].(map[string]any)
			values, _ := threads["values"].([]any)
			if len(values) != 1 {
				t.Fatalf("threads: got %d, want 1", len(values))
			}
			if th, _ := values[0].(map[string]any); th["id"] != tc.wantID {
				t.Errorf("thread id: got %v, want %v", th["id"], tc.wantID)
			}
			if !slices.Equal(omitted, []string{"threads"}) {
				t.Errorf("omitted: got %v, want [threads]", omitted)
			}
		})
	}

	// With an exception, threads are left out entirely.
	out, omitted := trimForTest(t, `{"exception":{"values":[{"type":"E"}]},"threads":{"values":[`+thread(1, "")+`]}}`)
	if _, ok := out["threads"]; ok {
		t.Error("threads should be dropped when an exception is present")
	}
	if !slices.Equal(omitted, []string{"threads"}) {
		t.Errorf("omitted: got %v, want [threads]", omitted)
	}
}

func TestMCPTrimEvent_smallEventUnchanged(t *testing.T) {
	out, omitted := trimForTest(t, `{"message":"hi","level":"info","request":{"url":"https://example.com","method":"GET"},"breadcrumbs":{"values":[{"message":"a"}]}}`)
	if len(omitted) != 0 {
		t.Errorf("omitted: got %v, want none", omitted)
	}
	if out["message"] != "hi" {
		t.Errorf("message: got %v", out["message"])
	}
}

func TestMCPTrimEvent_nonObjectUnchanged(t *testing.T) {
	for _, payload := range []string{`[1,2]`, `null`, `not json`} {
		raw, omitted := mcpTrimEvent(json.RawMessage(payload))
		if string(raw) != payload || omitted != nil {
			t.Errorf("%s: got %s %v, want it unchanged", payload, raw, omitted)
		}
	}
}
