package ingest_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestParseSentryTimestamp_float(t *testing.T) {
	// Unix epoch 1609459200 = 2021-01-01T00:00:00Z
	raw := json.RawMessage(`1609459200.0`)
	got := ingest.ParseSentryTimestamp(raw)
	want := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseSentryTimestamp_floatWithFraction(t *testing.T) {
	raw := json.RawMessage(`1609459200.5`)
	got := ingest.ParseSentryTimestamp(raw)
	if got.UnixMilli() != 1609459200500 {
		t.Errorf("fractional seconds not preserved: got %d ms", got.UnixMilli())
	}
}

func TestParseSentryTimestamp_string(t *testing.T) {
	raw := json.RawMessage(`"2021-01-01T00:00:00Z"`)
	got := ingest.ParseSentryTimestamp(raw)
	want := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseSentryTimestamp_stringWithNano(t *testing.T) {
	raw := json.RawMessage(`"2021-01-01T00:00:00.123456Z"`)
	got := ingest.ParseSentryTimestamp(raw)
	if got.Nanosecond() != 123456000 {
		t.Errorf("nanoseconds not preserved: got %d", got.Nanosecond())
	}
}

func TestParseSentryTimestamp_empty(t *testing.T) {
	before := time.Now()
	got := ingest.ParseSentryTimestamp(nil)
	after := time.Now()
	if got.Before(before) || got.After(after) {
		t.Errorf("empty timestamp should fall back to now, got %v", got)
	}
}

func TestParseSentryTimestamp_deterministic(t *testing.T) {
	raw := json.RawMessage(`"2024-06-15T12:30:45.678Z"`)
	a := ingest.ParseSentryTimestamp(raw)
	b := ingest.ParseSentryTimestamp(raw)
	if !a.Equal(b) {
		t.Error("same input must produce same output")
	}
}

func TestParseSentryTimestamp_invalidString_fallsToNow(t *testing.T) {
	before := time.Now().Add(-time.Second)
	got := ingest.ParseSentryTimestamp(json.RawMessage(`"not-a-timestamp"`))
	after := time.Now().Add(time.Second)
	if got.Before(before) || got.After(after) {
		t.Errorf("invalid string should fall back to now, got %v", got)
	}
}

func TestParseSentryTimestamp_negativeFloat_fallsToString(t *testing.T) {
	// Negative float → f > 0 check fails → tries string parse → "not valid" → falls to Now
	before := time.Now().Add(-time.Second)
	got := ingest.ParseSentryTimestamp(json.RawMessage(`-1.0`))
	after := time.Now().Add(time.Second)
	if got.Before(before) || got.After(after) {
		t.Errorf("negative float should fall back to now, got %v", got)
	}
}

func TestClampFuture(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	if got := ingest.ClampFuture(now.Add(30*time.Second), now); !got.Equal(now.Add(30 * time.Second)) {
		t.Errorf("small skew must be kept, got %v", got)
	}
	if got := ingest.ClampFuture(now.AddDate(4, 0, 0), now); !got.Equal(now) {
		t.Errorf("a far-future timestamp must clamp to now, got %v", got)
	}
	past := now.Add(-time.Hour)
	if got := ingest.ClampFuture(past, now); !got.Equal(past) {
		t.Errorf("past timestamps must be unchanged, got %v", got)
	}
}

func TestFutureShift(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	if got := ingest.FutureShift(now.Add(30*time.Second), now); got != 0 {
		t.Errorf("no shift within the allowed skew, got %v", got)
	}
	end := now.Add(2 * time.Hour)
	if got := ingest.FutureShift(end, now); !end.Add(got).Equal(now) {
		t.Errorf("shift must move end to now, got %v", got)
	}
}
