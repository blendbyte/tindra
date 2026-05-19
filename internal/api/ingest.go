package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

const (
	maxBodyBytes     = 20 * 1024 * 1024 // 20 MB - raw/uncompressed
	maxGzipBodyBytes = 1 * 1024 * 1024  // 1 MB - decompressed; real SDK payloads are well under this
	maxFieldLen      = 512
	maxDescLen       = 2048
	maxSpans         = 500
)

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// handleEnvelopeCORS handles OPTIONS preflight requests from browser SDKs.
func (ro *router) handleEnvelopeCORS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST")
	w.Header().Set("Access-Control-Allow-Headers", "X-Sentry-Auth, Content-Type, Content-Encoding")
	w.Header().Set("Access-Control-Max-Age", "3600")
	w.WriteHeader(http.StatusOK)
}

func (ro *router) handleEnvelope(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Spec: URL carries the project ID; public key is in the X-Sentry-Auth header.
	projectID := chi.URLParam(r, "projectID")
	publicKey := extractSentryKey(r)
	if publicKey == "" {
		http.Error(w, "missing sentry key", http.StatusUnauthorized)
		return
	}

	project, err := storage.GetByIDAndPublicKey(r.Context(), ro.pool, projectID, publicKey)
	if err != nil {
		slog.Error("project lookup", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if project == nil {
		http.Error(w, "invalid key", http.StatusUnauthorized)
		return
	}

	if lim := ro.eventLimit.Load(); lim > 0 {
		count, err := storage.CountMonthlyEvents(r.Context(), ro.pool)
		if err != nil {
			slog.Error("count monthly events", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if count >= int64(lim) {
			http.Error(w, "event limit reached", http.StatusTooManyRequests)
			return
		}
	}

	body := io.LimitReader(r.Body, maxBodyBytes)
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, "bad gzip", http.StatusBadRequest)
			return
		}
		defer gz.Close()
		body = io.LimitReader(gz, maxGzipBodyBytes)
	}

	var rawBuf bytes.Buffer
	header, items, err := ingest.Parse(io.TeeReader(body, &rawBuf))
	if err != nil {
		http.Error(w, "bad envelope", http.StatusBadRequest)
		return
	}

	var eventID *string
	if header.EventID != "" {
		id := header.EventID
		eventID = &id
	}

	scrubCfg := projectScrubConfig(project)

	for _, item := range items {
		switch item.Header.Type {
		case "event":
			ts := parseTimestamp(item.Payload)
			traceID, spanID := extractTraceContext(item.Payload)
			ev := ingest.BufferedEvent{
				ProjectID: project.ID,
				EventID:   eventID,
				Timestamp: ts,
				Payload:   ingest.ScrubEvent(json.RawMessage(item.Payload), scrubCfg),
				TraceID:   trunc(traceID, maxFieldLen),
				SpanID:    trunc(spanID, maxFieldLen),
			}
			if !ro.buf.Push(ev) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "buffer full", http.StatusTooManyRequests)
				return
			}

		case "transaction":
			if ro.txBuf == nil {
				continue
			}
			tx := parseTransaction(project.ID, item.Payload)
			if tx == nil {
				continue
			}
			ingest.ScrubTransaction(tx, scrubCfg)
			if !ro.txBuf.Push(*tx) {
				w.Header().Set("Retry-After", "1")
				http.Error(w, "buffer full", http.StatusTooManyRequests)
				return
			}

		case "log":
			if ro.logBuf == nil {
				continue
			}
			parseLogs(project.ID, item.Payload, func(l ingest.BufferedLog) {
				ro.logBuf.Push(l) // best-effort; drop silently if full
			})

		case "check_in":
			if ro.pool == nil {
				continue
			}
			handleEnvelopeCheckin(r.Context(), ro.pool, item.Payload)
		}
	}

	if project.PassthroughDSN != nil && *project.PassthroughDSN != "" {
		ingest.ForwardEnvelope(ro.passthroughClient, *project.PassthroughDSN, rawBuf.Bytes())
	}

	w.WriteHeader(http.StatusOK)
}

// extractSentryKey reads the public key from X-Sentry-Auth / Authorization headers,
// falling back to the ?sentry_key query parameter.
func extractSentryKey(r *http.Request) string {
	for _, h := range []string{"X-Sentry-Auth", "Authorization"} {
		if v := r.Header.Get(h); v != "" {
			if key := parseSentryAuthHeader(v); key != "" {
				return key
			}
		}
	}
	return r.URL.Query().Get("sentry_key")
}

// parseSentryAuthHeader parses "Sentry sentry_version=7, sentry_key=<key>, ..."
// and returns the sentry_key value.
func parseSentryAuthHeader(v string) string {
	const prefix = "Sentry "
	if !strings.HasPrefix(v, prefix) {
		return ""
	}
	for _, part := range strings.Split(v[len(prefix):], ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 && strings.TrimSpace(kv[0]) == "sentry_key" {
			return strings.TrimSpace(kv[1])
		}
	}
	return ""
}

func parseTimestamp(payload []byte) time.Time {
	var partial struct {
		Timestamp json.RawMessage `json:"timestamp"`
	}
	if err := json.Unmarshal(payload, &partial); err != nil || len(partial.Timestamp) == 0 {
		return time.Now().UTC()
	}
	return ingest.ParseSentryTimestamp(partial.Timestamp)
}

// extractTraceContext pulls trace_id and span_id from a Sentry event's contexts.trace block.
func extractTraceContext(payload []byte) (traceID, spanID string) {
	var p struct {
		Contexts struct {
			Trace struct {
				TraceID string `json:"trace_id"`
				SpanID  string `json:"span_id"`
			} `json:"trace"`
		} `json:"contexts"`
	}
	if json.Unmarshal(payload, &p) == nil {
		traceID = p.Contexts.Trace.TraceID
		spanID = p.Contexts.Trace.SpanID
	}
	return
}

func projectScrubConfig(p *storage.Project) ingest.ScrubConfig {
	cfg := ingest.ScrubConfig{Fields: p.ScrubFields}
	if len(p.ScrubPatterns) > 0 {
		_ = json.Unmarshal(p.ScrubPatterns, &cfg.Patterns) // malformed patterns are silently skipped
	}
	return cfg
}

// parseLogs parses a Sentry log envelope item payload (a JSON array of log
// records) and calls yield for each valid entry. Capped at 500 per item to
// prevent a single noisy service from filling the buffer.
func parseLogs(projectID string, payload []byte, yield func(ingest.BufferedLog)) {
	type rawLog struct {
		Timestamp  json.RawMessage        `json:"timestamp"`
		Level      string                 `json:"level"`
		Body       string                 `json:"body"`
		TraceID    string                 `json:"trace_id"`
		SpanID     string                 `json:"span_id"`
		Attributes map[string]interface{} `json:"attributes"`
	}

	var records []rawLog
	// Payload is either a JSON array or a single object.
	if len(payload) > 0 && payload[0] == '[' {
		json.Unmarshal(payload, &records) //nolint:errcheck
	} else {
		var single rawLog
		if json.Unmarshal(payload, &single) == nil {
			records = []rawLog{single}
		}
	}

	const cap = 500
	if len(records) > cap {
		records = records[:cap]
	}

	for _, rec := range records {
		if rec.Body == "" {
			continue
		}
		ts := ingest.ParseSentryTimestamp(rec.Timestamp)
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		level := rec.Level
		if level == "" {
			level = "info"
		}

		var attrs json.RawMessage
		if len(rec.Attributes) > 0 {
			attrs, _ = json.Marshal(rec.Attributes)
		}

		// Extract environment and release from attributes if present.
		env, _ := rec.Attributes["sentry.environment"].(string)
		rel, _ := rec.Attributes["sentry.release"].(string)

		yield(ingest.BufferedLog{
			ProjectID:   projectID,
			Timestamp:   ts,
			Level:       trunc(level, maxFieldLen),
			Body:        trunc(rec.Body, ingest.MaxLogBody),
			TraceID:     trunc(rec.TraceID, maxFieldLen),
			SpanID:      trunc(rec.SpanID, maxFieldLen),
			Environment: trunc(env, maxFieldLen),
			Release:     trunc(rel, maxFieldLen),
			Attributes:  attrs,
		})
	}
}

func parseTransaction(projectID string, payload []byte) *ingest.BufferedTransaction {
	var p struct {
		Transaction    string          `json:"transaction"`
		StartTimestamp json.RawMessage `json:"start_timestamp"`
		Timestamp      json.RawMessage `json:"timestamp"`
		Contexts       struct {
			Trace struct {
				TraceID string `json:"trace_id"`
				SpanID  string `json:"span_id"`
				Op      string `json:"op"`
				Status  string `json:"status"`
			} `json:"trace"`
		} `json:"contexts"`
		Spans []struct {
			SpanID         string          `json:"span_id"`
			ParentSpanID   string          `json:"parent_span_id"`
			Op             string          `json:"op"`
			Description    string          `json:"description"`
			StartTimestamp json.RawMessage `json:"start_timestamp"`
			Timestamp      json.RawMessage `json:"timestamp"`
			Status         string          `json:"status"`
			Data           json.RawMessage `json:"data"`
		} `json:"spans"`
		Measurements json.RawMessage `json:"measurements"`
		Environment  string          `json:"environment"`
		Release      string          `json:"release"`
		Platform     string          `json:"platform"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.Transaction == "" {
		return nil
	}

	startTS := ingest.ParseSentryTimestamp(p.StartTimestamp)
	endTS := ingest.ParseSentryTimestamp(p.Timestamp)
	durationMs := int(endTS.Sub(startTS).Milliseconds())
	if durationMs < 0 {
		durationMs = 0
	}

	status := p.Contexts.Trace.Status
	if status == "" {
		status = "ok"
	}

	rawSpans := p.Spans
	if len(rawSpans) > maxSpans {
		rawSpans = rawSpans[:maxSpans]
	}
	spans := make([]ingest.BufferedSpan, 0, len(rawSpans))
	for _, sp := range rawSpans {
		spStart := ingest.ParseSentryTimestamp(sp.StartTimestamp)
		spEnd := ingest.ParseSentryTimestamp(sp.Timestamp)
		spDuration := int(spEnd.Sub(spStart).Milliseconds())
		if spDuration < 0 {
			spDuration = 0
		}
		spans = append(spans, ingest.BufferedSpan{
			SpanID:         trunc(sp.SpanID, maxFieldLen),
			ParentSpanID:   trunc(sp.ParentSpanID, maxFieldLen),
			Op:             trunc(sp.Op, maxFieldLen),
			Description:    trunc(sp.Description, maxDescLen),
			StartTimestamp: spStart,
			Timestamp:      spEnd,
			DurationMs:     spDuration,
			Status:         trunc(sp.Status, maxFieldLen),
			Data:           sp.Data,
		})
	}

	return &ingest.BufferedTransaction{
		ProjectID:      projectID,
		TraceID:        trunc(p.Contexts.Trace.TraceID, maxFieldLen),
		SpanID:         trunc(p.Contexts.Trace.SpanID, maxFieldLen),
		Transaction:    trunc(p.Transaction, maxFieldLen),
		Op:             trunc(p.Contexts.Trace.Op, maxFieldLen),
		Status:         trunc(status, maxFieldLen),
		DurationMs:     durationMs,
		StartTimestamp: startTS,
		Timestamp:      endTS,
		Measurements:   p.Measurements,
		Environment:    trunc(p.Environment, maxFieldLen),
		Release:        trunc(p.Release, maxFieldLen),
		Platform:       trunc(p.Platform, maxFieldLen),
		Spans:          spans,
	}
}

// handleEnvelopeCheckin processes a check_in envelope item.
// The monitor_slug field is expected to be the monitor UUID.
func handleEnvelopeCheckin(ctx context.Context, pool *pgxpool.Pool, payload []byte) {
	var ci struct {
		CheckinID   string   `json:"check_in_id"`
		MonitorSlug string   `json:"monitor_slug"` // contains monitor UUID
		Status      string   `json:"status"`       // in_progress, ok, error
		Duration    *float64 `json:"duration"`     // seconds
		Environment *string  `json:"environment"`
	}
	if err := json.Unmarshal(payload, &ci); err != nil || ci.MonitorSlug == "" {
		return
	}

	m, err := storage.GetCronMonitor(ctx, pool, ci.MonitorSlug)
	if err != nil || m == nil || m.Status == "paused" {
		return
	}

	var durationMs *int
	if ci.Duration != nil && *ci.Duration >= 0 {
		ms := int(*ci.Duration * 1000)
		durationMs = &ms
	}

	switch ci.Status {
	case "in_progress":
		now := time.Now().UTC()
		_, _ = storage.RecordCheckin(ctx, pool, m.ID, &storage.CronCheckin{
			Status:      "in_progress",
			Environment: ci.Environment,
			StartedAt:   &now,
		})
	case "ok", "error":
		if ci.CheckinID != "" {
			// Try to finish an existing in_progress check-in first.
			updated, _ := storage.FinishCheckin(ctx, pool, m.ID, ci.CheckinID, ci.Status, durationMs)
			if updated != nil {
				return
			}
		}
		// Single-shot or no matching in_progress: record terminal check-in directly.
		now := time.Now().UTC()
		_, _ = storage.RecordCheckin(ctx, pool, m.ID, &storage.CronCheckin{
			Status:      ci.Status,
			DurationMs:  durationMs,
			Environment: ci.Environment,
			FinishedAt:  &now,
		})
	}
}
