package api

import (
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestComputeCriticalPath(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	makeTx := func(durationMs int) *storage.Transaction {
		return &storage.Transaction{
			SpanID:         "tx-root",
			StartTimestamp: base,
			DurationMs:     durationMs,
		}
	}

	makeSpan := func(spanID, parentID string, startMs, durationMs int) *storage.Span {
		return &storage.Span{
			SpanID:         spanID,
			ParentSpanID:   parentID,
			StartTimestamp: base.Add(time.Duration(startMs) * time.Millisecond),
			DurationMs:     durationMs,
		}
	}

	t.Run("sequential chain", func(t *testing.T) {
		// A(0-100) → B(100-300) → C(300-400). All are sequential → all critical.
		tx := makeTx(400)
		spans := []*storage.Span{
			makeSpan("A", "tx-root", 0, 100),
			makeSpan("B", "A", 100, 200),
			makeSpan("C", "B", 300, 100),
		}
		got := computeCriticalPath(tx, spans)
		for _, id := range []string{"A", "B", "C"} {
			if !got[id] {
				t.Errorf("expected span %s to be critical", id)
			}
		}
	})

	t.Run("parallel branches - only longer branch is critical", func(t *testing.T) {
		// A(0-100) → {B(100-500 duration=400), C(100-200 duration=100)} → D(500-600).
		// B determines the end of A's children, so A→B→D is the critical path.
		tx := makeTx(600)
		spans := []*storage.Span{
			makeSpan("A", "tx-root", 0, 100),
			makeSpan("B", "A", 100, 400), // longer branch
			makeSpan("C", "A", 100, 100), // shorter parallel branch
			makeSpan("D", "B", 500, 100),
		}
		got := computeCriticalPath(tx, spans)
		for _, id := range []string{"A", "B", "D"} {
			if !got[id] {
				t.Errorf("expected span %s to be critical", id)
			}
		}
		if got["C"] {
			t.Error("span C should not be critical (parallel, finishes earlier)")
		}
	})

	t.Run("empty spans", func(t *testing.T) {
		tx := makeTx(100)
		got := computeCriticalPath(tx, nil)
		if got != nil {
			t.Errorf("expected nil for empty spans, got %v", got)
		}
	})

	t.Run("single span", func(t *testing.T) {
		tx := makeTx(200)
		spans := []*storage.Span{makeSpan("A", "tx-root", 0, 200)}
		got := computeCriticalPath(tx, spans)
		if !got["A"] {
			t.Error("single span should always be critical")
		}
	})

	t.Run("spans with no span_id are ignored", func(t *testing.T) {
		tx := makeTx(100)
		spans := []*storage.Span{
			{SpanID: "", ParentSpanID: "", StartTimestamp: base, DurationMs: 100},
		}
		got := computeCriticalPath(tx, spans)
		if got != nil {
			t.Errorf("expected nil when spans have no span_id, got %v", got)
		}
	})
}
