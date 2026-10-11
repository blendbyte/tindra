package ingest_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestTruncateUTF8_neverSplitsCharacters(t *testing.T) {
	s := strings.Repeat("日本", 10) // 3 bytes per character
	for n := 0; n <= len(s); n++ {
		got := ingest.TruncateUTF8(s, n)
		if len(got) > n || !utf8.ValidString(got) {
			t.Fatalf("TruncateUTF8(_, %d) = %q (len %d, valid %v)", n, got, len(got), utf8.ValidString(got))
		}
	}
	if got := ingest.TruncateUTF8("short", 10); got != "short" {
		t.Errorf("short strings must be unchanged, got %q", got)
	}
}

func TestNormalizeEventID(t *testing.T) {
	for in, ok := range map[string]bool{
		"0123456789abcdef0123456789ABCDEF":     true,
		"01234567-89ab-cdef-0123-456789abcdef": true,
		"custom-client-id-42":                  true,
		"":                                     false,
		"has space":                            false,
		strings.Repeat("a", 129):               false,
		"id\x00":                               false,
		"naïve":                                false,
	} {
		if got := ingest.NormalizeEventID(in); (got != nil) != ok {
			t.Errorf("NormalizeEventID(%q) = %v, want ok=%v", in, got, ok)
		}
	}
}

func TestCapEventFields(t *testing.T) {
	small := json.RawMessage(`{"release":"1.0","environment":"prod","id":12345678901234567890}`)
	if got := ingest.CapEventFields(small); string(got) != string(small) {
		t.Errorf("payload within limits must be returned unchanged, got %s", got)
	}

	long := strings.Repeat("é", 3000)
	big := json.RawMessage(`{"release":"` + long + `","environment":"` + long + `","id":12345678901234567890,"message":"keep"}`)
	var out struct {
		Release     string          `json:"release"`
		Environment string          `json:"environment"`
		ID          json.RawMessage `json:"id"`
		Message     string          `json:"message"`
	}
	if err := json.Unmarshal(ingest.CapEventFields(big), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Release) > ingest.MaxIndexedFieldBytes || len(out.Environment) > ingest.MaxIndexedFieldBytes {
		t.Errorf("fields not capped: release %d bytes, environment %d bytes", len(out.Release), len(out.Environment))
	}
	if string(out.ID) != "12345678901234567890" || out.Message != "keep" {
		t.Errorf("other fields must be preserved exactly: id=%s message=%q", out.ID, out.Message)
	}
}

// Without the cap, a release this long fails Postgres's btree row limit and
// takes the rest of the batch with it.
func TestBuffer_cappedLongReleaseIsStored(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	buf := ingest.NewBuffer(2)
	release := strings.Repeat("x", 4000)
	payload := ingest.CapEventFields(json.RawMessage(`{"message":"long release","release":"` + release + `"}`))
	for _, id := range []string{"cap-release-1", "cap-release-2"} {
		buf.Push(ingest.BufferedEvent{ProjectID: testProject.ID, EventID: &id, Timestamp: time.Now(), Payload: payload})
	}
	cancel()
	buf.Run(ctx, testPool)
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM events WHERE project_id=$1 AND event_id LIKE 'cap-release-%'`, testProject.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected both events stored, got %d (stats %+v)", count, buf.Stats())
	}
}
