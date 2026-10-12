package issues_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/issues"
)

// fingerprintOf returns just the current fingerprint.
func fingerprintOf(raw json.RawMessage) string {
	fp, _ := issues.Compute(raw)
	return fp
}

func TestCompute_explicitFingerprint(t *testing.T) {
	payload := json.RawMessage(`{"fingerprint":["my-custom-fingerprint"],"message":"ignored"}`)
	fp := fingerprintOf(payload)
	if fp == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	// Same fingerprint list → same result
	if fp != fingerprintOf(payload) {
		t.Error("fingerprint must be deterministic")
	}
}

func TestCompute_exceptionChain(t *testing.T) {
	a := json.RawMessage(`{"exception":{"values":[{"type":"ValueError","value":"bad input"}]}}`)
	b := json.RawMessage(`{"exception":{"values":[{"type":"ValueError","value":"bad input"}]}}`)
	different := json.RawMessage(`{"exception":{"values":[{"type":"TypeError","value":"bad input"}]}}`)

	fa := fingerprintOf(a)
	fb := fingerprintOf(b)
	fd := fingerprintOf(different)

	if fa != fb {
		t.Error("same exception chain must produce same fingerprint")
	}
	if fa == fd {
		t.Error("different exception types must produce different fingerprints")
	}
}

func TestCompute_message(t *testing.T) {
	a := json.RawMessage(`{"message":"connection refused"}`)
	b := json.RawMessage(`{"message":"connection refused"}`)
	c := json.RawMessage(`{"message":"timeout"}`)

	if fingerprintOf(a) != fingerprintOf(b) {
		t.Error("same message must produce same fingerprint")
	}
	if fingerprintOf(a) == fingerprintOf(c) {
		t.Error("different messages must produce different fingerprints")
	}
}

func TestCompute_fallback(t *testing.T) {
	// Empty payload → stable hash
	fp := fingerprintOf(json.RawMessage(`{}`))
	if fp == "" {
		t.Fatal("expected non-empty fallback fingerprint")
	}
	if fingerprintOf(json.RawMessage(`{}`)) != fp {
		t.Error("fallback must be deterministic")
	}
}

func TestCompute_explicitTakesPriorityOverException(t *testing.T) {
	withExplicit := json.RawMessage(`{
		"fingerprint":["explicit"],
		"exception":{"values":[{"type":"ValueError","value":"x"}]}
	}`)
	withoutExplicit := json.RawMessage(`{
		"exception":{"values":[{"type":"ValueError","value":"x"}]}
	}`)
	if fingerprintOf(withExplicit) == fingerprintOf(withoutExplicit) {
		t.Error("explicit fingerprint must override exception chain")
	}
}

func TestTitle_exception(t *testing.T) {
	payload := json.RawMessage(`{"exception":{"values":[{"type":"ValueError","value":"bad input"}]}}`)
	title := issues.Title(payload)
	if !strings.Contains(title, "ValueError") {
		t.Errorf("expected ValueError in title, got %q", title)
	}
	if !strings.Contains(title, "bad input") {
		t.Errorf("expected 'bad input' in title, got %q", title)
	}
}

func TestTitle_exceptionNoValue(t *testing.T) {
	payload := json.RawMessage(`{"exception":{"values":[{"type":"SomeError"}]}}`)
	title := issues.Title(payload)
	if title != "SomeError" {
		t.Errorf("expected 'SomeError', got %q", title)
	}
}

func TestTitle_message(t *testing.T) {
	payload := json.RawMessage(`{"message":"something went wrong"}`)
	title := issues.Title(payload)
	if title != "something went wrong" {
		t.Errorf("expected message as title, got %q", title)
	}
}

func TestTitle_truncates(t *testing.T) {
	long := strings.Repeat("a", 300)
	payload, _ := json.Marshal(map[string]string{"message": long})
	title := issues.Title(payload)
	if r := []rune(title); len(r) > 200 {
		t.Errorf("title too long: %d runes", len(r))
	}
	if !strings.HasSuffix(title, "\u2026") {
		t.Errorf("expected truncated title to end in an ellipsis, got %q", title)
	}
}

func TestTitle_unknown(t *testing.T) {
	title := issues.Title(json.RawMessage(`{}`))
	if title != "Unknown error" {
		t.Errorf("expected 'Unknown error', got %q", title)
	}
}

func TestTitle_exceptionValueTruncates(t *testing.T) {
	// Exception value that, combined with type, exceeds 200 chars.
	long := strings.Repeat("x", 200)
	payload, _ := json.Marshal(map[string]any{
		"exception": map[string]any{
			"values": []map[string]string{
				{"type": "LongError", "value": long},
			},
		},
	})
	title := issues.Title(json.RawMessage(payload))
	if r := []rune(title); len(r) > 200 {
		t.Errorf("exception title too long: %d runes", len(r))
	}
	if !strings.HasPrefix(title, "LongError: ") {
		t.Errorf("expected exception type prefix, got %q", title)
	}
	if !strings.HasSuffix(title, "\u2026") {
		t.Errorf("expected truncated title to end in an ellipsis, got %q", title)
	}
}

func TestTitle_exactLengthNotEllipsised(t *testing.T) {
	// A title exactly at the limit must not be marked as truncated.
	payload, _ := json.Marshal(map[string]string{"message": strings.Repeat("a", 200)})
	title := issues.Title(json.RawMessage(payload))
	if strings.HasSuffix(title, "\u2026") {
		t.Errorf("title at the limit must not be ellipsised, got %q", title)
	}
	if r := []rune(title); len(r) != 200 {
		t.Errorf("expected 200 runes, got %d", len(r))
	}
}

func TestCompute_defaultExpansion(t *testing.T) {
	// "{{ default }}" expands to the exception-based default key.
	withDefault := json.RawMessage(`{
		"fingerprint":["{{ default }}","extra"],
		"exception":{"values":[{"type":"ValueError","value":"x"}]}
	}`)
	withoutDefault := json.RawMessage(`{
		"fingerprint":["extra"],
		"exception":{"values":[{"type":"ValueError","value":"x"}]}
	}`)
	// Hashes must differ (extra info injected via {{ default }}).
	if fingerprintOf(withDefault) == fingerprintOf(withoutDefault) {
		t.Error("{{ default }} expansion should change fingerprint")
	}
	// Calling twice must be deterministic.
	h1 := fingerprintOf(withDefault)
	h2 := fingerprintOf(withDefault)
	if h1 != h2 {
		t.Error("{{ default }} expansion must be deterministic")
	}
}

func TestCompute_exceptionWithTransaction(t *testing.T) {
	// Exception + transaction → transaction appended to default key.
	withTx := json.RawMessage(`{
		"exception":{"values":[{"type":"ValueError","value":"x"}]},
		"transaction":"/api/v1/users"
	}`)
	withoutTx := json.RawMessage(`{
		"exception":{"values":[{"type":"ValueError","value":"x"}]}
	}`)
	if fingerprintOf(withTx) == fingerprintOf(withoutTx) {
		t.Error("transaction should affect fingerprint when exception is present")
	}
}

func TestCompute_messageWithTransaction(t *testing.T) {
	withTx := json.RawMessage(`{"message":"oops","transaction":"/api/v1/users"}`)
	withoutTx := json.RawMessage(`{"message":"oops"}`)
	if fingerprintOf(withTx) == fingerprintOf(withoutTx) {
		t.Error("transaction should affect fingerprint when message is present")
	}
}

func TestTitle_invalidJSON(t *testing.T) {
	title := issues.Title(json.RawMessage(`not json`))
	if title != "Unknown error" {
		t.Errorf("expected 'Unknown error' for invalid JSON, got %q", title)
	}
}

func TestTitle_truncatesAtRuneBoundary(t *testing.T) {
	// 201 three-byte runes: a byte-slice truncation at index 200 would land
	// inside a multibyte character and produce invalid UTF-8.
	long := strings.Repeat("あ", 201)
	payload, _ := json.Marshal(map[string]string{"message": long})
	title := issues.Title(json.RawMessage(payload))
	if r := []rune(title); len(r) > 200 {
		t.Errorf("expected at most 200 runes, got %d", len(r))
	}
	if !strings.HasSuffix(title, "\u2026") {
		t.Errorf("expected truncated title to end in an ellipsis, got %q", title)
	}
	for i, b := range title {
		if b == '�' {
			t.Errorf("invalid UTF-8 replacement character at byte %d", i)
		}
	}
}

func TestCompute_invalidJSON(t *testing.T) {
	fp := fingerprintOf(json.RawMessage(`not json`))
	if fp == "" {
		t.Error("expected non-empty fallback for invalid JSON")
	}
	if fp != fingerprintOf(json.RawMessage(`not json`)) {
		t.Error("invalid JSON fallback must be deterministic")
	}
}

func TestCompute_objectMessageGroupsByTemplate(t *testing.T) {
	// Java, .NET and PHP send message as an object; Python logging uses logentry.
	a := json.RawMessage(`{"event_id":"a1","timestamp":1,"message":{"formatted":"User 1 logged in","message":"User %s logged in","params":["1"]}}`)
	b := json.RawMessage(`{"event_id":"b2","timestamp":2,"message":{"formatted":"User 2 logged in","message":"User %s logged in","params":["2"]}}`)
	if fingerprintOf(a) != fingerprintOf(b) {
		t.Error("object messages with the same template must group together")
	}
	if got := issues.Title(a); got != "User 1 logged in" {
		t.Errorf("title should use the formatted message, got %q", got)
	}

	logentry := json.RawMessage(`{"logentry":{"formatted":"disk 91% full","message":"disk %s full"}}`)
	if got := issues.Title(logentry); got != "disk 91% full" {
		t.Errorf("title should use logentry.formatted, got %q", got)
	}
	if fingerprintOf(logentry) == fingerprintOf(json.RawMessage(`{"logentry":{"message":"other %s"}}`)) {
		t.Error("different logentry templates must not group together")
	}
}

func TestCompute_neverHashesRawPayload(t *testing.T) {
	// Event ID and timestamp differ on every occurrence; they must not split issues.
	a := json.RawMessage(`{"event_id":"a1","timestamp":1,"transaction":"/checkout"}`)
	b := json.RawMessage(`{"event_id":"b2","timestamp":2,"transaction":"/checkout"}`)
	if fingerprintOf(a) != fingerprintOf(b) {
		t.Error("events without exception or message must group per transaction")
	}
}

func TestCompute_tolerantOfOddlyTypedFields(t *testing.T) {
	// A numeric value or fingerprint entry must not make the payload undecodable.
	a := json.RawMessage(`{"exception":{"values":[{"type":"E","value":42}]},"fingerprint":[1,"{{default}}"]}`)
	b := json.RawMessage(`{"exception":{"values":[{"type":"E","value":43}]},"fingerprint":[1,"{{default}}"]}`)
	if fingerprintOf(a) == fingerprintOf(b) {
		t.Error("values should still distinguish issues without frames")
	}
	if fingerprintOf(a) == fingerprintOf(json.RawMessage(`not json`)) {
		t.Error("an oddly typed field must not fall back to the unparseable fingerprint")
	}
	// The bare-list exception form is accepted too.
	list := json.RawMessage(`{"exception":[{"type":"ValueError","value":"x"}]}`)
	if issues.Title(list) != "ValueError: x" {
		t.Errorf("bare exception list title = %q", issues.Title(list))
	}
}

func frameEvent(platform, value string, frames string) json.RawMessage {
	return json.RawMessage(`{"platform":"` + platform + `","exception":{"values":[{"type":"NotFound","value":"` + value + `","stacktrace":{"frames":[` + frames + `]}}]}}`)
}

const appFrames = `{"module":"vendor.lib","function":"call","in_app":false,"lineno":1},` +
	`{"module":"app.orders","function":"load","in_app":true,"lineno":10},` +
	`{"module":"app.orders","function":"find","in_app":true,"lineno":20}`

func TestCompute_v2GroupsByInAppFrames(t *testing.T) {
	a := frameEvent("python", "Order 123 not found", appFrames)
	b := frameEvent("python", "Order 456 not found", appFrames)
	if fingerprintOf(a) != fingerprintOf(b) {
		t.Error("same exception type and frames must group regardless of the value")
	}
	moved := frameEvent("python", "Order 123 not found", strings.ReplaceAll(appFrames, `"lineno":20`, `"lineno":99`))
	if fingerprintOf(a) != fingerprintOf(moved) {
		t.Error("line numbers must not affect grouping")
	}
	other := frameEvent("python", "Order 123 not found", strings.ReplaceAll(appFrames, `"find"`, `"lookup"`))
	if fingerprintOf(a) == fingerprintOf(other) {
		t.Error("different in-app frames must not group together")
	}
	vendorOnly := frameEvent("python", "Order 123 not found", strings.ReplaceAll(appFrames, `"vendor.lib"`, `"vendor.other"`))
	if fingerprintOf(a) != fingerprintOf(vendorOnly) {
		t.Error("non-app frames must not affect grouping when in-app frames exist")
	}
}

func TestCompute_v2ReturnsLegacyFingerprint(t *testing.T) {
	withFrames := frameEvent("python", "Order 123 not found", appFrames)
	fp, legacy := issues.Compute(withFrames)
	v1, _ := issues.Compute(json.RawMessage(`{"exception":{"values":[{"type":"NotFound","value":"Order 123 not found"}]}}`))
	if legacy != v1 {
		t.Error("legacy fingerprint must equal the v1 key so existing issues keep matching")
	}
	if fp == legacy {
		t.Error("frame-based fingerprint should differ from the legacy one")
	}

	_, noFramesLegacy := issues.Compute(json.RawMessage(`{"exception":{"values":[{"type":"E","value":"x"}]}}`))
	if noFramesLegacy != "" {
		t.Error("events without frames have no separate legacy fingerprint")
	}
}

func TestCompute_v2SkipsBrowserJavaScript(t *testing.T) {
	// Minified browser function names change every deploy, so keep v1 grouping.
	a := frameEvent("javascript", "x is undefined", `{"function":"e","in_app":true},{"function":"t","in_app":true}`)
	b := frameEvent("javascript", "x is undefined", `{"function":"n","in_app":true},{"function":"r","in_app":true}`)
	if fingerprintOf(a) != fingerprintOf(b) {
		t.Error("browser JavaScript must group by type and value, not minified frames")
	}
	if _, legacy := issues.Compute(a); legacy != "" {
		t.Error("browser JavaScript has no separate legacy fingerprint")
	}
}

func TestCompute_v2CollapsesRecursion(t *testing.T) {
	shallow := frameEvent("go", "boom", `{"module":"main","function":"walk","in_app":true}`)
	deep := frameEvent("go", "boom", `{"module":"main","function":"walk","in_app":true},{"module":"main","function":"walk","in_app":true}`)
	if fingerprintOf(shallow) != fingerprintOf(deep) {
		t.Error("recursion depth must not split issues")
	}
}

// One malformed nested field must not change the fingerprint: nested objects
// are decoded lazily, so a string "request" or array "contexts" is ignored.
func TestCompute_oddNestedFieldsDoNotAffectFingerprint(t *testing.T) {
	clean := json.RawMessage(`{"exception":{"values":[{"type":"E","value":"x"}]},"level":"error"}`)
	odd := json.RawMessage(`{"exception":{"values":[{"type":"E","value":"x","mechanism":"not-an-object"}]},"level":40,"request":"GET /","contexts":[1,2],"tags":"oops"}`)
	if fingerprintOf(clean) != fingerprintOf(odd) {
		t.Error("odd nested fields changed the fingerprint")
	}
}
