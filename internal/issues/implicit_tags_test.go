package issues

import (
	"encoding/json"
	"testing"
)

func tagsToMap(tags [][2]string) map[string]string {
	m := make(map[string]string, len(tags))
	for _, t := range tags {
		m[t[0]] = t[1]
	}
	return m
}

func TestExtractImplicitTags_topLevelFields(t *testing.T) {
	payload := json.RawMessage(`{
		"level": "error",
		"environment": "production",
		"transaction": "/api/users",
		"server_name": "web-01",
		"request": {"url": "https://example.com/api/users"}
	}`)

	tags := tagsToMap(extractImplicitTags(payload))

	cases := [][2]string{
		{"level", "error"},
		{"environment", "production"},
		{"transaction", "/api/users"},
		{"server_name", "web-01"},
		{"url", "https://example.com/api/users"},
	}
	for _, c := range cases {
		if got := tags[c[0]]; got != c[1] {
			t.Errorf("tag %q: got %q, want %q", c[0], got, c[1])
		}
	}
}

func TestExtractImplicitTags_contexts(t *testing.T) {
	payload := json.RawMessage(`{
		"contexts": {
			"browser": {"name": "Firefox", "version": "127.0"},
			"os":      {"name": "Linux", "version": "6.12.88", "build": "SMP Debian"},
			"runtime": {"name": "php", "version": "8.5.6"}
		}
	}`)

	tags := tagsToMap(extractImplicitTags(payload))

	cases := [][2]string{
		{"browser", "Firefox 127.0"},
		{"browser.name", "Firefox"},
		{"browser.version", "127.0"},
		{"os", "Linux 6.12.88"},
		{"os.name", "Linux"},
		{"os.version", "6.12.88"},
		{"os.build", "SMP Debian"},
		{"runtime", "php 8.5.6"},
		{"runtime.name", "php"},
		{"runtime.version", "8.5.6"},
	}
	for _, c := range cases {
		if got := tags[c[0]]; got != c[1] {
			t.Errorf("tag %q: got %q, want %q", c[0], got, c[1])
		}
	}
}

func TestExtractImplicitTags_contextNoVersion(t *testing.T) {
	payload := json.RawMessage(`{"contexts": {"client_os": {"name": "Ubuntu"}}}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if got := tags["client_os"]; got != "Ubuntu" {
		t.Errorf("client_os: got %q, want Ubuntu", got)
	}
	if got := tags["client_os.name"]; got != "Ubuntu" {
		t.Errorf("client_os.name: got %q, want Ubuntu", got)
	}
}

func TestExtractImplicitTags_mechanismHandledFalse(t *testing.T) {
	handled := false
	_ = handled
	payload := json.RawMessage(`{
		"exception": {
			"values": [{"mechanism": {"type": "generic", "handled": false}}]
		}
	}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if got := tags["mechanism"]; got != "generic" {
		t.Errorf("mechanism: got %q, want generic", got)
	}
	if got := tags["handled"]; got != "no" {
		t.Errorf("handled: got %q, want no", got)
	}
}

func TestExtractImplicitTags_mechanismHandledTrue(t *testing.T) {
	payload := json.RawMessage(`{
		"exception": {
			"values": [{"mechanism": {"type": "user", "handled": true}}]
		}
	}`)
	tags := tagsToMap(extractImplicitTags(payload))
	if got := tags["handled"]; got != "yes" {
		t.Errorf("handled: got %q, want yes", got)
	}
}

func TestExtractImplicitTags_emptyPayload(t *testing.T) {
	tags := extractImplicitTags(json.RawMessage(`{}`))
	if len(tags) != 0 {
		t.Errorf("expected no tags for empty payload, got %d", len(tags))
	}
}

func TestExtractImplicitTags_truncatesLongValues(t *testing.T) {
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'x'
	}
	payload, _ := json.Marshal(map[string]string{"transaction": string(long)})
	tags := tagsToMap(extractImplicitTags(payload))
	if got := tags["transaction"]; len(got) != tagMaxLen {
		t.Errorf("expected truncation to %d chars, got %d", tagMaxLen, len(got))
	}
}

func TestMergeImplicitTags_explicitTakesPriority(t *testing.T) {
	explicit := [][2]string{{"level", "warning"}}
	implicit := [][2]string{{"level", "error"}, {"environment", "production"}}
	merged := tagsToMap(mergeImplicitTags(explicit, implicit))
	if merged["level"] != "warning" {
		t.Errorf("explicit level should win: got %q", merged["level"])
	}
	if merged["environment"] != "production" {
		t.Errorf("implicit environment should be added: got %q", merged["environment"])
	}
}

func TestMergeImplicitTags_nilExplicit(t *testing.T) {
	implicit := [][2]string{{"level", "error"}}
	merged := mergeImplicitTags(nil, implicit)
	if len(merged) != 1 || merged[0][1] != "error" {
		t.Errorf("unexpected merged result: %v", merged)
	}
}
