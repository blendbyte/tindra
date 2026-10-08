package ingest

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
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

func TestSanitizeJSONPayload_preservesEscapedBackslashes(t *testing.T) {
	for _, tt := range []struct{ name, input, want string }{
		{"literal", `{"message":"\\u0000"}`, `{"message":"\\u0000"}`},
		{"backslash then null", `{"message":"\\\u0000"}`, `{"message":"\\"}`},
		{"mixed", `{"message":"\\u0000 and \u0000","user":{"id":"id\u0000"}}`, `{"message":"\\u0000 and ","user":{"id":"id"}}`},
		{"escaped quote", `{"message":"\"\u0000"}`, `{"message":"\""}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			input := json.RawMessage(tt.input)
			got := sanitizeJSONPayload(input)
			if string(got) != tt.want || !json.Valid(got) {
				t.Fatalf("got %s, want %s", got, tt.want)
			}
			if string(input) != tt.input {
				t.Fatal("modified input")
			}
			if string(sanitizeJSONPayload(got)) != tt.want {
				t.Fatal("sanitization is not idempotent")
			}
		})
	}
}

func TestStripNUL(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"", ""},
		{"clean", "clean"},
		{"\x00", ""},
		{"a\x00b\x00c", "abc"},
		{`literal\u0000`, `literal\u0000`},
	} {
		if got := stripNUL(tt.in); got != tt.want {
			t.Errorf("stripNUL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSanitizeLog(t *testing.T) {
	got := sanitizeLog(BufferedLog{
		Level:       "info\x00",
		Body:        "nul\x00body",
		TraceID:     "trace\x00",
		SpanID:      "span\x00",
		Environment: "prod\x00",
		Release:     "1.0\x00",
		Attributes:  json.RawMessage(`{"a\u0000b":"x\u0000y","list":["\u0000"],"n":1}`),
	})
	want := BufferedLog{
		Level:       "info",
		Body:        "nulbody",
		TraceID:     "trace",
		SpanID:      "span",
		Environment: "prod",
		Release:     "1.0",
		Attributes:  json.RawMessage(`{"ab":"xy","list":[""],"n":1}`),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v (attributes %s), want %+v (attributes %s)", got, got.Attributes, want, want.Attributes)
	}
}

func TestSanitizeTransaction(t *testing.T) {
	spans := []BufferedSpan{{
		SpanID:       "s\x00",
		ParentSpanID: "p\x00",
		Op:           "db\x00",
		Description:  "SELECT\x00 1",
		Status:       "ok\x00",
		Data:         json.RawMessage(`{"k\u0000":"v\u0000"}`),
	}}
	got := sanitizeTransaction(BufferedTransaction{
		EventID:      "e\x00",
		ProfilerID:   "pr\x00",
		ThreadID:     "th\x00",
		TraceID:      "tr\x00",
		SpanID:       "sp\x00",
		Transaction:  "GET /x\x00",
		Op:           "http\x00",
		Status:       "ok\x00",
		Environment:  "env\x00",
		Release:      "rel\x00",
		Platform:     "go\x00",
		Measurements: json.RawMessage(`{"lcp\u0000":{"value":1}}`),
		Spans:        spans,
		UserIdentity: "id:u\x00",
		UserID:       "u\x00",
		UserUsername: "name\x00",
		UserEmail:    "a@b\x00",
		UserName:     "A\x00",
	})

	for name, v := range map[string]string{
		"EventID": got.EventID, "ProfilerID": got.ProfilerID, "ThreadID": got.ThreadID,
		"TraceID": got.TraceID, "SpanID": got.SpanID, "Transaction": got.Transaction,
		"Op": got.Op, "Status": got.Status, "Environment": got.Environment,
		"Release": got.Release, "Platform": got.Platform, "UserIdentity": got.UserIdentity,
		"UserID": got.UserID, "UserUsername": got.UserUsername, "UserEmail": got.UserEmail,
		"UserName": got.UserName, "span SpanID": got.Spans[0].SpanID,
		"span ParentSpanID": got.Spans[0].ParentSpanID, "span Op": got.Spans[0].Op,
		"span Description": got.Spans[0].Description, "span Status": got.Spans[0].Status,
		"Measurements": string(got.Measurements), "span Data": string(got.Spans[0].Data),
	} {
		if strings.Contains(v, "\x00") || strings.Contains(v, `\u0000`) {
			t.Errorf("%s still contains NUL: %q", name, v)
		}
	}
	if got.Transaction != "GET /x" || got.Spans[0].Description != "SELECT 1" {
		t.Errorf("unexpected values: transaction=%q description=%q", got.Transaction, got.Spans[0].Description)
	}
	if string(got.Spans[0].Data) != `{"k":"v"}` {
		t.Errorf("span data = %s", got.Spans[0].Data)
	}
	if spans[0].Description != "SELECT\x00 1" {
		t.Error("caller's spans were modified")
	}
}

func TestSanitizeProfile(t *testing.T) {
	data := []byte{0, 1, 0}
	got := sanitizeProfile(BufferedProfile{
		TransactionEventID: "tx\x00",
		ChunkID:            "c\x00",
		ProfilerID:         "p\x00",
		TraceID:            "t\x00",
		Environment:        "env\x00",
		Release:            "rel\x00",
		Platform:           "go\x00",
		Data:               data,
	})
	if got.TransactionEventID != "tx" || got.ChunkID != "c" || got.ProfilerID != "p" || got.TraceID != "t" ||
		got.Environment != "env" || got.Release != "rel" || got.Platform != "go" {
		t.Errorf("NUL left in profile metadata: %+v", got)
	}
	if !bytes.Equal(got.Data, []byte{0, 1, 0}) {
		t.Errorf("encoded data must stay untouched, got %v", got.Data)
	}
}
