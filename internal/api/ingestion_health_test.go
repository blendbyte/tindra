package api

import (
	"bytes"
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestIngestionMetricsWithoutDatabase(t *testing.T) {
	ro := &router{statsAPIKey: "operator-key", buf: ingest.NewBuffer(1), logBuf: ingest.NewLogBuffer(1), profBuf: ingest.NewProfileBuffer(1)}
	ro.buf.Push(ingest.BufferedEvent{})
	ro.logBuf.Push(ingest.BufferedLog{})
	ro.logBuf.Push(ingest.BufferedLog{})
	for _, tt := range []struct {
		token  string
		status int
	}{{"", 401}, {"wrong", 401}, {"operator-key", 200}} {
		req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
		req.Header.Set("Authorization", "Bearer "+tt.token)
		rec := httptest.NewRecorder()
		ro.handleIngestionMetrics(rec, req)
		if rec.Code != tt.status {
			t.Fatalf("status=%d want=%d", rec.Code, tt.status)
		}
		if tt.status == 200 {
			for _, line := range []string{`tindra_ingest_capacity_bytes{type="profiles"} 134217728`, `tindra_ingest_capacity_bytes{type="events"} 0`, `tindra_ingest_pending{type="events"} 1`, `tindra_ingest_dropped_total{type="logs",reason="buffer_full"} 1`, `tindra_ingest_last_success_timestamp_seconds{type="events"} 0`} {
				if !strings.Contains(rec.Body.String(), line) {
					t.Errorf("missing %q in %s", line, rec.Body.String())
				}
			}
		}
	}
	ro.statsAPIKey = ""
	rec := httptest.NewRecorder()
	ro.handleIngestionMetrics(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != 404 {
		t.Fatalf("metrics without configured key: %d", rec.Code)
	}
}

func TestIngestionStatusWithoutDatabase(t *testing.T) {
	ro := &router{profBuf: ingest.NewProfileBuffer(1)}
	ro.profBuf.RecordDrop("encode_failed")
	rec := httptest.NewRecorder()
	ro.handleIngestionStatus(rec, httptest.NewRequest(http.MethodGet, "/api/instance/ingestion", nil))
	var stats map[string]ingest.Stats
	if err := json.Unmarshal(rec.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats["profiles"].Dropped["encode_failed"] != 1 || stats["profiles"].CapacityBytes != 128<<20 {
		t.Fatalf("missing drop: %s", rec.Body.String())
	}
}

func TestIngestionMetricsSkipsDebugRequestLogging(t *testing.T) {
	var output bytes.Buffer
	previous, previousWriter := slog.Default(), log.Writer()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer func() { slog.SetDefault(previous); log.SetOutput(previousWriter) }()
	ro := &router{statsAPIKey: "operator-key", buf: ingest.NewBuffer(1)}
	handler := slogRequestLogger(http.HandlerFunc(ro.handleIngestionMetrics))
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.Header.Set("Authorization", "Bearer operator-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if output.Len() != 0 {
		t.Fatalf("metrics depends on log output: %s", output.String())
	}
	// Ordinary requests retain the existing debug logging behavior.
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ordinary", nil))
	if output.Len() == 0 {
		t.Fatal("ordinary request logging disabled")
	}
}
