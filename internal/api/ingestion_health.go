package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/blendbyte/tindra/internal/ingest"
)

func (ro *router) ingestionStats() map[string]ingest.Stats {
	stats := make(map[string]ingest.Stats)
	if ro.buf != nil {
		stats["events"] = ro.buf.Stats()
	}
	if ro.txBuf != nil {
		stats["transactions"] = ro.txBuf.Stats()
	}
	if ro.logBuf != nil {
		stats["logs"] = ro.logBuf.Stats()
	}
	if ro.profBuf != nil {
		stats["profiles"] = ro.profBuf.Stats()
	}
	return stats
}

func (ro *router) handleIngestionStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, ro.ingestionStats())
}

// Metrics uses the existing operator credential, never session or database auth,
// so a database outage cannot hide ingestion failures from monitoring.
func (ro *router) handleIngestionMetrics(w http.ResponseWriter, r *http.Request) {
	if ro.statsAPIKey == "" {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if subtle.ConstantTimeCompare([]byte(token), []byte(ro.statsAPIKey)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	stats := ro.ingestionStats()
	metrics := []struct {
		name, kind string
		value      func(ingest.Stats) any
	}{
		{"accepted_total", "counter", func(s ingest.Stats) any { return s.Accepted }},
		{"persisted_total", "counter", func(s ingest.Stats) any { return s.Persisted }},
		{"rejected_total", "counter", func(s ingest.Stats) any { return s.Rejected }},
		{"unknown_commit_items_total", "counter", func(s ingest.Stats) any { return s.Unknown }},
		{"failed_writes_total", "counter", func(s ingest.Stats) any { return s.FailedWrites }},
		{"retries_total", "counter", func(s ingest.Stats) any { return s.Retries }},
		{"pending", "gauge", func(s ingest.Stats) any { return s.Pending }},
		{"queued", "gauge", func(s ingest.Stats) any { return s.Queued }},
		{"capacity", "gauge", func(s ingest.Stats) any { return s.Capacity }},
		{"pending_bytes", "gauge", func(s ingest.Stats) any { return s.PendingBytes }},
		{"capacity_bytes", "gauge", func(s ingest.Stats) any { return s.CapacityBytes }},
		{"oldest_pending_seconds", "gauge", func(s ingest.Stats) any { return s.OldestPendingSeconds }},
		{"last_success_timestamp_seconds", "gauge", func(s ingest.Stats) any {
			if s.LastSuccess == nil {
				return 0
			}
			return s.LastSuccess.Unix()
		}},
		{"degraded", "gauge", func(s ingest.Stats) any {
			if s.Degraded {
				return 1
			}
			return 0
		}},
	}
	for _, m := range metrics {
		fmt.Fprintf(w, "# TYPE tindra_ingest_%s %s\n", m.name, m.kind)
		for _, name := range []string{"events", "transactions", "logs", "profiles"} {
			if s, ok := stats[name]; ok {
				fmt.Fprintf(w, "tindra_ingest_%s{type=%q} %v\n", m.name, name, m.value(s))
			}
		}
	}
	fmt.Fprintln(w, "# TYPE tindra_ingest_dropped_total counter")
	for _, name := range []string{"events", "transactions", "logs", "profiles"} {
		if s, ok := stats[name]; ok {
			for _, reason := range []string{"buffer_full", "buffer_bytes", "shutdown", "write_failed", "deadline", "invalid_record", "encode_failed"} {
				fmt.Fprintf(w, "tindra_ingest_dropped_total{type=%q,reason=%q} %d\n", name, reason, s.Dropped[reason])
			}
		}
	}
}
