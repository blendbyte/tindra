package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
)

// itemsPayload is the shape every current SDK sends: a versioned object
// wrapping the batch under "items", with typed attribute values.
const itemsPayload = `{
  "version": 2,
  "ingest_settings": {"infer_ip": "auto"},
  "items": [
    {
      "timestamp": 1544719860.0,
      "trace_id": "5b8efff798038103d269b633813fc60c",
      "span_id": "b0e6f15b45c36b12",
      "level": "warn",
      "body": "User John has logged in!",
      "severity_number": 13,
      "attributes": {
        "sentry.environment": {"value": "production", "type": "string"},
        "sentry.release": {"value": "1.2.3", "type": "string"},
        "user.id": {"value": 42, "type": "integer"},
        "cache.hit": {"value": true, "type": "boolean"}
      }
    }
  ]
}`

func TestParseLogs_payloadShapes(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    int
	}{
		{
			name:    "items wrapper",
			payload: itemsPayload,
			want:    1,
		},
		{
			name:    "items wrapper with several records",
			payload: `{"version":2,"items":[{"level":"info","body":"one"},{"level":"info","body":"two"}]}`,
			want:    2,
		},
		{
			name:    "bare array",
			payload: `[{"timestamp":1700000000.0,"level":"info","body":"hello","trace_id":"abc123"}]`,
			want:    1,
		},
		{
			name:    "single object",
			payload: `{"timestamp":1700000001.0,"level":"error","body":"single log object"}`,
			want:    1,
		},
		{
			name:    "empty items batch",
			payload: `{"version":2,"items":[]}`,
			want:    0,
		},
		{
			name:    "record without body is skipped",
			payload: `{"version":2,"items":[{"level":"info"},{"level":"info","body":"kept"}]}`,
			want:    1,
		},
		{
			name:    "empty payload",
			payload: ``,
			want:    0,
		},
		{
			name:    "malformed json",
			payload: `{"version":2,"items":`,
			want:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := api.ParseLogsForTest("proj", []byte(tc.payload))
			if len(got) != tc.want {
				t.Fatalf("expected %d records, got %d (%+v)", tc.want, len(got), got)
			}
		})
	}
}

func TestParseLogs_itemsWrapperFields(t *testing.T) {
	got := api.ParseLogsForTest("proj", []byte(itemsPayload))
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	l := got[0]

	if l.ProjectID != "proj" {
		t.Errorf("project id: got %q", l.ProjectID)
	}
	if l.Body != "User John has logged in!" {
		t.Errorf("body: got %q", l.Body)
	}
	if l.Level != "warning" {
		t.Errorf("level: expected warn to normalize to warning, got %q", l.Level)
	}
	if l.TraceID != "5b8efff798038103d269b633813fc60c" {
		t.Errorf("trace id: got %q", l.TraceID)
	}
	if l.SpanID != "b0e6f15b45c36b12" {
		t.Errorf("span id: got %q", l.SpanID)
	}
	if l.Environment != "production" {
		t.Errorf("environment: expected it to come from the typed attribute, got %q", l.Environment)
	}
	if l.Release != "1.2.3" {
		t.Errorf("release: expected it to come from the typed attribute, got %q", l.Release)
	}
	if want := time.Unix(1544719860, 0).UTC(); !l.Timestamp.Equal(want) {
		t.Errorf("timestamp: expected %v, got %v", want, l.Timestamp)
	}

	var attrs map[string]any
	if err := json.Unmarshal(l.Attributes, &attrs); err != nil {
		t.Fatalf("unmarshal attributes: %v", err)
	}
	if attrs["sentry.environment"] != "production" {
		t.Errorf("attributes: expected flattened string, got %#v", attrs["sentry.environment"])
	}
	if attrs["user.id"] != float64(42) {
		t.Errorf("attributes: expected flattened number, got %#v", attrs["user.id"])
	}
	if attrs["cache.hit"] != true {
		t.Errorf("attributes: expected flattened bool, got %#v", attrs["cache.hit"])
	}
}

func TestParseLogs_untypedAttributesArePreserved(t *testing.T) {
	payload := `{"version":2,"items":[{"level":"info","body":"x","attributes":{
		"sentry.environment":"staging",
		"plain.number":7,
		"nested":{"a":1}
	}}]}`

	got := api.ParseLogsForTest("proj", []byte(payload))
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	if got[0].Environment != "staging" {
		t.Errorf("environment: got %q", got[0].Environment)
	}

	var attrs map[string]any
	if err := json.Unmarshal(got[0].Attributes, &attrs); err != nil {
		t.Fatalf("unmarshal attributes: %v", err)
	}
	if attrs["plain.number"] != float64(7) {
		t.Errorf("plain.number: got %#v", attrs["plain.number"])
	}
	nested, ok := attrs["nested"].(map[string]any)
	if !ok || nested["a"] != float64(1) {
		t.Errorf("nested object should survive untouched, got %#v", attrs["nested"])
	}
}

func TestParseLogs_levelDefaultsAndNormalization(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "info"},
		{"warn", "warning"},
		{"WARN", "warning"},
		{"warning", "warning"},
		{"Error", "error"},
		{"fatal", "fatal"},
		{"trace", "trace"},
	}

	for _, tc := range tests {
		payload := fmt.Sprintf(`{"version":2,"items":[{"level":%q,"body":"x"}]}`, tc.in)
		got := api.ParseLogsForTest("proj", []byte(payload))
		if len(got) != 1 {
			t.Fatalf("level %q: expected 1 record, got %d", tc.in, len(got))
		}
		if got[0].Level != tc.want {
			t.Errorf("level %q: expected %q, got %q", tc.in, tc.want, got[0].Level)
		}
	}
}

func TestParseLogs_missingTimestampDefaultsToNow(t *testing.T) {
	before := time.Now().UTC().Add(-time.Second)
	got := api.ParseLogsForTest("proj", []byte(`{"version":2,"items":[{"level":"info","body":"x"}]}`))
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	if got[0].Timestamp.Before(before) {
		t.Errorf("expected timestamp to default to now, got %v", got[0].Timestamp)
	}
}

func TestParseLogs_capsRecordsPerItem(t *testing.T) {
	var records []string
	for i := 0; i < 600; i++ {
		records = append(records, fmt.Sprintf(`{"level":"info","body":"log %d"}`, i))
	}
	payload := `{"version":2,"items":[` + strings.Join(records, ",") + `]}`

	got := api.ParseLogsForTest("proj", []byte(payload))
	if len(got) != 500 {
		t.Fatalf("expected the 500-record cap to apply, got %d", len(got))
	}
}

func TestParseLogs_truncatesLongFields(t *testing.T) {
	long := strings.Repeat("a", ingest.MaxLogBody+100)
	payload := fmt.Sprintf(`{"version":2,"items":[{"level":"info","body":%q,"trace_id":%q}]}`,
		long, strings.Repeat("b", 1000))

	got := api.ParseLogsForTest("proj", []byte(payload))
	if len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	if len(got[0].Body) != ingest.MaxLogBody {
		t.Errorf("body: expected truncation to %d, got %d", ingest.MaxLogBody, len(got[0].Body))
	}
	if len(got[0].TraceID) != 512 {
		t.Errorf("trace id: expected truncation to 512, got %d", len(got[0].TraceID))
	}
}
