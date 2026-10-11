package api

import (
	"fmt"
	"testing"
	"time"
)

// A transaction from a device whose clock runs years ahead is shifted back as a
// unit: it ends at the server time, and its duration and span offsets survive.
func TestParseTransaction_shiftsFutureTimestampsAsAUnit(t *testing.T) {
	start := time.Now().UTC().AddDate(4, 0, 0)
	ts := func(d time.Duration) float64 { return float64(start.Add(d).UnixMilli()) / 1000 }
	payload := fmt.Sprintf(`{
		"transaction": "/checkout", "start_timestamp": %f, "timestamp": %f,
		"contexts": {"trace": {"trace_id": "t", "span_id": "s"}},
		"spans": [{"span_id": "a", "start_timestamp": %f, "timestamp": %f}]
	}`, ts(0), ts(2*time.Second), ts(500*time.Millisecond), ts(1500*time.Millisecond))

	tx := parseTransaction("project", []byte(payload))
	if tx == nil {
		t.Fatal("expected a transaction")
	}
	if tx.Timestamp.After(time.Now().Add(time.Minute)) {
		t.Errorf("end timestamp still in the future: %v", tx.Timestamp)
	}
	if tx.DurationMs < 1990 || tx.DurationMs > 2010 {
		t.Errorf("duration changed: %d ms", tx.DurationMs)
	}
	if len(tx.Spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(tx.Spans))
	}
	offset := tx.Spans[0].StartTimestamp.Sub(tx.StartTimestamp)
	if offset < 490*time.Millisecond || offset > 510*time.Millisecond {
		t.Errorf("span offset changed: %v", offset)
	}
}
