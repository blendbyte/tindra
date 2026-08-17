package ingest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScrubEvent_NoConfig(t *testing.T) {
	payload := json.RawMessage(`{"message":"hello","user":{"email":"alice@example.com"}}`)
	got := ScrubEvent(payload, ScrubConfig{})
	if string(got) != string(payload) {
		t.Errorf("expected unchanged payload, got %s", got)
	}
}

func TestScrubEvent_BuiltinEmail(t *testing.T) {
	payload := json.RawMessage(`{"message":"sent to alice@example.com","level":"error"}`)
	cfg := ScrubConfig{
		Patterns: []ScrubPattern{
			{Name: "email", Builtin: true, Enabled: true},
		},
	}
	got := ScrubEvent(payload, cfg)
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	if result["message"] != "sent to [Filtered]" {
		t.Errorf("expected email redacted, got %q", result["message"])
	}
	if result["level"] != "error" {
		t.Errorf("expected level unchanged, got %q", result["level"])
	}
}

func TestScrubEvent_BuiltinIP(t *testing.T) {
	payload := json.RawMessage(`{"request":{"url":"http://api/","env":{"REMOTE_ADDR":"192.168.1.42"}}}`)
	cfg := ScrubConfig{
		Patterns: []ScrubPattern{
			{Name: "ip", Builtin: true, Enabled: true},
		},
	}
	got := ScrubEvent(payload, cfg)
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	req := result["request"].(map[string]any)
	env := req["env"].(map[string]any)
	if env["REMOTE_ADDR"] != "[Filtered]" {
		t.Errorf("expected IP redacted, got %q", env["REMOTE_ADDR"])
	}
}

func TestScrubEvent_FieldPath(t *testing.T) {
	payload := json.RawMessage(`{"request":{"headers":{"Authorization":"Bearer secret","Content-Type":"application/json"}}}`)
	cfg := ScrubConfig{
		Fields: []string{"request.headers.Authorization"},
	}
	got := ScrubEvent(payload, cfg)
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	req := result["request"].(map[string]any)
	headers := req["headers"].(map[string]any)
	if headers["Authorization"] != "[Filtered]" {
		t.Errorf("expected Authorization redacted, got %q", headers["Authorization"])
	}
	if headers["Content-Type"] != "application/json" {
		t.Errorf("expected Content-Type unchanged, got %q", headers["Content-Type"])
	}
}

func TestScrubEvent_FieldPathCaseInsensitive(t *testing.T) {
	payload := json.RawMessage(`{"request":{"headers":{"authorization":"Bearer secret"}}}`)
	cfg := ScrubConfig{
		Fields: []string{"REQUEST.HEADERS.AUTHORIZATION"},
	}
	got := ScrubEvent(payload, cfg)
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	req := result["request"].(map[string]any)
	headers := req["headers"].(map[string]any)
	if headers["authorization"] != "[Filtered]" {
		t.Errorf("expected authorization redacted (case-insensitive), got %q", headers["authorization"])
	}
}

func TestScrubEvent_ArrayValues(t *testing.T) {
	payload := json.RawMessage(`{"breadcrumbs":[{"message":"user alice@example.com logged in"},{"message":"page loaded"}]}`)
	cfg := ScrubConfig{
		Patterns: []ScrubPattern{
			{Name: "email", Builtin: true, Enabled: true},
		},
	}
	got := ScrubEvent(payload, cfg)
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	crumbs := result["breadcrumbs"].([]any)
	first := crumbs[0].(map[string]any)
	if first["message"] != "user [Filtered] logged in" {
		t.Errorf("expected email in breadcrumb redacted, got %q", first["message"])
	}
	second := crumbs[1].(map[string]any)
	if second["message"] != "page loaded" {
		t.Errorf("expected second breadcrumb unchanged, got %q", second["message"])
	}
}

func TestScrubEvent_DisabledPattern(t *testing.T) {
	payload := json.RawMessage(`{"message":"alice@example.com"}`)
	cfg := ScrubConfig{
		Patterns: []ScrubPattern{
			{Name: "email", Builtin: true, Enabled: false},
		},
	}
	got := ScrubEvent(payload, cfg)
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	if result["message"] != "alice@example.com" {
		t.Errorf("expected disabled pattern to leave value intact, got %q", result["message"])
	}
}

func TestScrubTransaction_SpanDescription(t *testing.T) {
	tx := &BufferedTransaction{
		Spans: []BufferedSpan{
			{Description: "SELECT * WHERE email = 'alice@example.com'"},
			{Description: "GET /health"},
		},
	}
	cfg := ScrubConfig{
		Patterns: []ScrubPattern{
			{Name: "email", Builtin: true, Enabled: true},
		},
	}
	ScrubTransaction(tx, cfg)
	if tx.Spans[0].Description != "SELECT * WHERE email = '[Filtered]'" {
		t.Errorf("expected email in description redacted, got %q", tx.Spans[0].Description)
	}
	if tx.Spans[1].Description != "GET /health" {
		t.Errorf("expected second span description unchanged, got %q", tx.Spans[1].Description)
	}
}

func TestValidateScrubPatterns_empty(t *testing.T) {
	if err := ValidateScrubPatterns(nil); err != nil {
		t.Errorf("expected no error for nil, got %v", err)
	}
	if err := ValidateScrubPatterns([]ScrubPattern{}); err != nil {
		t.Errorf("expected no error for empty slice, got %v", err)
	}
}

func TestValidateScrubPatterns_atLimit(t *testing.T) {
	patterns := make([]ScrubPattern, MaxScrubPatterns)
	if err := ValidateScrubPatterns(patterns); err != nil {
		t.Errorf("expected no error at limit of %d, got %v", MaxScrubPatterns, err)
	}
}

func TestValidateScrubPatterns_tooMany(t *testing.T) {
	patterns := make([]ScrubPattern, MaxScrubPatterns+1)
	if err := ValidateScrubPatterns(patterns); err == nil {
		t.Errorf("expected error for %d patterns, got nil", MaxScrubPatterns+1)
	}
}

func TestValidateScrubPatterns_customTooLong(t *testing.T) {
	patterns := []ScrubPattern{
		{Name: "long", Pattern: strings.Repeat("a", MaxScrubPatternLen+1), Builtin: false},
	}
	if err := ValidateScrubPatterns(patterns); err == nil {
		t.Error("expected error for pattern exceeding max length, got nil")
	}
}

func TestValidateScrubPatterns_builtinSkipsLengthCheck(t *testing.T) {
	patterns := []ScrubPattern{
		{Name: "big-builtin", Pattern: strings.Repeat("a", MaxScrubPatternLen+1), Builtin: true},
	}
	if err := ValidateScrubPatterns(patterns); err != nil {
		t.Errorf("expected builtin to skip length check, got %v", err)
	}
}

func TestValidateScrubPatterns_invalidRegex(t *testing.T) {
	patterns := []ScrubPattern{
		{Name: "bad", Pattern: "[unclosed", Builtin: false},
	}
	if err := ValidateScrubPatterns(patterns); err == nil {
		t.Error("expected error for invalid regex, got nil")
	}
}

func TestValidateScrubPatterns_validCustom(t *testing.T) {
	patterns := []ScrubPattern{
		{Name: "ssn", Pattern: `\d{3}-\d{2}-\d{4}`, Builtin: false},
	}
	if err := ValidateScrubPatterns(patterns); err != nil {
		t.Errorf("expected no error for valid custom pattern, got %v", err)
	}
}

func TestScrubTransaction_SpanData(t *testing.T) {
	tx := &BufferedTransaction{
		Spans: []BufferedSpan{
			{Data: json.RawMessage(`{"db.user":"alice@example.com"}`)},
		},
	}
	cfg := ScrubConfig{
		Patterns: []ScrubPattern{
			{Name: "email", Builtin: true, Enabled: true},
		},
	}
	ScrubTransaction(tx, cfg)
	var data map[string]any
	if err := json.Unmarshal(tx.Spans[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["db.user"] != "[Filtered]" {
		t.Errorf("expected email in span data redacted, got %q", data["db.user"])
	}
}

func TestScrubEvent_UnknownBuiltin_ignored(t *testing.T) {
	// An unknown builtin name must be skipped entirely — it must not fall
	// through to treating p.Pattern as a user-supplied regex.
	payload := json.RawMessage(`{"message":"alice@example.com"}`)
	cfg := ScrubConfig{
		Patterns: []ScrubPattern{
			{Name: "notabuiltin", Builtin: true, Enabled: true, Pattern: `\S+@\S+`},
		},
	}
	got := ScrubEvent(payload, cfg)
	var result map[string]any
	if err := json.Unmarshal(got, &result); err != nil {
		t.Fatal(err)
	}
	if result["message"] != "alice@example.com" {
		t.Errorf("unknown builtin should be skipped, got %q", result["message"])
	}
}

func TestScrubLog_NoConfig(t *testing.T) {
	l := BufferedLog{
		Body:       "login from alice@example.com",
		Attributes: json.RawMessage(`{"user.email":"alice@example.com"}`),
	}
	ScrubLog(&l, ScrubConfig{})

	if l.Body != "login from alice@example.com" {
		t.Errorf("expected body unchanged, got %q", l.Body)
	}
	if string(l.Attributes) != `{"user.email":"alice@example.com"}` {
		t.Errorf("expected attributes unchanged, got %s", l.Attributes)
	}
}

func TestScrubLog_PatternsOnBodyAndAttributes(t *testing.T) {
	l := BufferedLog{
		Body: "login from alice@example.com at 192.168.1.20",
		Attributes: json.RawMessage(
			`{"sentry.message.parameter.0":"bob@example.com","client.address":"10.0.0.4","user.id":42}`),
	}
	cfg := ScrubConfig{
		Patterns: []ScrubPattern{
			{Name: "email", Builtin: true, Enabled: true},
			{Name: "ip", Builtin: true, Enabled: true},
		},
	}
	ScrubLog(&l, cfg)

	if strings.Contains(l.Body, "alice@example.com") {
		t.Errorf("expected email redacted in body, got %q", l.Body)
	}
	if strings.Contains(l.Body, "192.168.1.20") {
		t.Errorf("expected ip redacted in body, got %q", l.Body)
	}

	var attrs map[string]any
	if err := json.Unmarshal(l.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	if attrs["sentry.message.parameter.0"] != "[Filtered]" {
		t.Errorf("expected interpolated parameter redacted, got %q", attrs["sentry.message.parameter.0"])
	}
	if attrs["client.address"] != "[Filtered]" {
		t.Errorf("expected ip attribute redacted, got %q", attrs["client.address"])
	}
	if attrs["user.id"] != float64(42) {
		t.Errorf("expected non-matching attribute untouched, got %#v", attrs["user.id"])
	}
}

func TestScrubLog_FieldBlockingOnAttributeNames(t *testing.T) {
	l := BufferedLog{
		Body:       "password reset requested",
		Attributes: json.RawMessage(`{"user.password":"hunter2","user.id":"u-1"}`),
	}
	// Attribute paths are relative to the attributes object, so the bare
	// attribute name is what gets configured.
	cfg := ScrubConfig{Fields: []string{"user.password"}}
	ScrubLog(&l, cfg)

	var attrs map[string]any
	if err := json.Unmarshal(l.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	if attrs["user.password"] != "[Filtered]" {
		t.Errorf("expected blocked attribute redacted, got %q", attrs["user.password"])
	}
	if attrs["user.id"] != "u-1" {
		t.Errorf("expected other attribute untouched, got %q", attrs["user.id"])
	}
	if l.Body != "password reset requested" {
		t.Errorf("field blocking must not touch the body, got %q", l.Body)
	}
}

func TestScrubLog_NilAttributes(t *testing.T) {
	l := BufferedLog{Body: "reach me at alice@example.com"}
	cfg := ScrubConfig{
		Patterns: []ScrubPattern{{Name: "email", Builtin: true, Enabled: true}},
	}
	ScrubLog(&l, cfg)

	if strings.Contains(l.Body, "alice@example.com") {
		t.Errorf("expected email redacted, got %q", l.Body)
	}
	if l.Attributes != nil {
		t.Errorf("expected nil attributes to stay nil, got %s", l.Attributes)
	}
}

func TestScrubLog_DisabledPatternIgnored(t *testing.T) {
	l := BufferedLog{Body: "reach me at alice@example.com"}
	cfg := ScrubConfig{
		Patterns: []ScrubPattern{{Name: "email", Builtin: true, Enabled: false}},
	}
	ScrubLog(&l, cfg)

	if l.Body != "reach me at alice@example.com" {
		t.Errorf("disabled pattern must not scrub, got %q", l.Body)
	}
}
