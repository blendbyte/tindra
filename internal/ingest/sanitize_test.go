package ingest

import (
	"encoding/json"
	"testing"
)

func TestSanitizeJSONPayload_stripsNullEscapes(t *testing.T) {
	input := json.RawMessage(`{"message":"hello\u0000world"}`)
	got := sanitizeJSONPayload(input)
	want := `{"message":"helloworld"}`
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSanitizeJSONPayload_stripsMultiple(t *testing.T) {
	input := json.RawMessage(`{"a":"\u0000","b":"x\u0000y\u0000z"}`)
	got := sanitizeJSONPayload(input)
	want := `{"a":"","b":"xyz"}`
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSanitizeJSONPayload_cleanPayloadUnchanged(t *testing.T) {
	input := json.RawMessage(`{"level":"error","message":"normal error"}`)
	got := sanitizeJSONPayload(input)
	if string(got) != string(input) {
		t.Errorf("clean payload was modified: got %q", got)
	}
}

func TestSanitizeJSONPayload_emptyPayload(t *testing.T) {
	input := json.RawMessage(`{}`)
	got := sanitizeJSONPayload(input)
	if string(got) != `{}` {
		t.Errorf("got %q, want {}", got)
	}
}

func TestSanitizeJSONPayload_preservesOtherUnicodeEscapes(t *testing.T) {
	input := json.RawMessage(`{"msg":"\u0041\u0042"}`)
	got := sanitizeJSONPayload(input)
	if string(got) != string(input) {
		t.Errorf("non-null unicode escapes should be preserved: got %q", got)
	}
}
