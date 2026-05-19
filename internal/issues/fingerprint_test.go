package issues_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/issues"
)

func TestCompute_explicitFingerprint(t *testing.T) {
	payload := json.RawMessage(`{"fingerprint":["my-custom-fingerprint"],"message":"ignored"}`)
	fp := issues.Compute(payload)
	if fp == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	// Same fingerprint list → same result
	if fp != issues.Compute(payload) {
		t.Error("fingerprint must be deterministic")
	}
}

func TestCompute_exceptionChain(t *testing.T) {
	a := json.RawMessage(`{"exception":{"values":[{"type":"ValueError","value":"bad input"}]}}`)
	b := json.RawMessage(`{"exception":{"values":[{"type":"ValueError","value":"bad input"}]}}`)
	different := json.RawMessage(`{"exception":{"values":[{"type":"TypeError","value":"bad input"}]}}`)

	fa := issues.Compute(a)
	fb := issues.Compute(b)
	fd := issues.Compute(different)

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

	if issues.Compute(a) != issues.Compute(b) {
		t.Error("same message must produce same fingerprint")
	}
	if issues.Compute(a) == issues.Compute(c) {
		t.Error("different messages must produce different fingerprints")
	}
}

func TestCompute_fallback(t *testing.T) {
	// Empty payload → stable hash
	fp := issues.Compute(json.RawMessage(`{}`))
	if fp == "" {
		t.Fatal("expected non-empty fallback fingerprint")
	}
	if issues.Compute(json.RawMessage(`{}`)) != fp {
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
	if issues.Compute(withExplicit) == issues.Compute(withoutExplicit) {
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
	if len(title) > 200 {
		t.Errorf("title too long: %d chars", len(title))
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
	if len(title) > 200 {
		t.Errorf("exception title too long: %d chars", len(title))
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
	if issues.Compute(withDefault) == issues.Compute(withoutDefault) {
		t.Error("{{ default }} expansion should change fingerprint")
	}
	// Calling twice must be deterministic.
	h1 := issues.Compute(withDefault)
	h2 := issues.Compute(withDefault)
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
	if issues.Compute(withTx) == issues.Compute(withoutTx) {
		t.Error("transaction should affect fingerprint when exception is present")
	}
}

func TestCompute_messageWithTransaction(t *testing.T) {
	withTx := json.RawMessage(`{"message":"oops","transaction":"/api/v1/users"}`)
	withoutTx := json.RawMessage(`{"message":"oops"}`)
	if issues.Compute(withTx) == issues.Compute(withoutTx) {
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
	// 201 three-byte runes — a byte-slice truncation at index 200 would land
	// inside a multibyte character and produce invalid UTF-8.
	long := strings.Repeat("あ", 201)
	payload, _ := json.Marshal(map[string]string{"message": long})
	title := issues.Title(json.RawMessage(payload))
	if r := []rune(title); len(r) > 200 {
		t.Errorf("expected at most 200 runes, got %d", len(r))
	}
	for i, b := range title {
		if b == '�' {
			t.Errorf("invalid UTF-8 replacement character at byte %d", i)
		}
	}
}

func TestCompute_invalidJSON(t *testing.T) {
	fp := issues.Compute(json.RawMessage(`not json`))
	if fp == "" {
		t.Error("expected non-empty fallback for invalid JSON")
	}
	// Should be the raw hash of the bytes.
	if fp != issues.Compute(json.RawMessage(`not json`)) {
		t.Error("invalid JSON fallback must be deterministic")
	}
}
