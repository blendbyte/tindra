package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

const (
	maxBodyBytes = 20 * 1024 * 1024 // 20 MB - raw/uncompressed

	// maxGzipBodyBytes caps the decompressed size, matching the raw cap.
	// Error events are tiny, but SDK profiling items are not: a single profile
	// is hundreds of KB to megabytes before compression, and SDKs ship it in
	// the same envelope as its transaction. The previous 1 MB cap truncated
	// such a body mid-item, failing the parse for the whole envelope and
	// discarding the transaction along with the profile.
	maxGzipBodyBytes = 20 * 1024 * 1024
	maxFieldLen      = 512
	maxDescLen       = 2048
	maxSpans         = 500
)

// limitedReader reads at most a fixed number of bytes and records whether that
// cap was reached. io.LimitReader truncates silently, which downgrades "the
// body was too big" into an opaque parse failure further down the stack.
type limitedReader struct {
	r    io.Reader
	left int64 // one past the cap, so reaching zero means the cap was exceeded
	over bool
}

func newLimitedReader(r io.Reader, limit int64) *limitedReader {
	return &limitedReader{r: r, left: limit + 1}
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.left <= 0 {
		l.over = true
		return 0, io.EOF
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left <= 0 {
		l.over = true
	}
	return n, err
}

// exceeded reports whether more bytes were available than the cap allowed.
func (l *limitedReader) exceeded() bool { return l.over }

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

	publicKey := extractSentryKey(r)
	if publicKey == "" {
		http.Error(w, "missing sentry key", http.StatusUnauthorized)
		return
	}

	// Look up by public key only. The project ID in the URL is Sentry's
	// routing hint and the JS SDK truncates UUID project IDs via parseInt
	// (e.g. "4915a9dc-..." becomes "4915"), so it cannot be used for lookup.
	// The public key is unique and is the real authentication credential.
	project, err := storage.GetByPublicKey(r.Context(), ro.pool, publicKey)
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

	rawLimit := newLimitedReader(r.Body, maxBodyBytes)
	var body io.Reader = rawLimit
	sizeLimit := rawLimit
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(body)
		if err != nil {
			http.Error(w, "bad gzip", http.StatusBadRequest)
			return
		}
		defer gz.Close()
		sizeLimit = newLimitedReader(gz, maxGzipBodyBytes)
		body = sizeLimit
	}

	// The raw copy exists only to forward the envelope on. Teeing it
	// unconditionally meant every request held a second full copy of the body
	// for nothing, which got a lot more expensive once the decompressed cap
	// rose to 20 MB for profiles.
	forwarding := project.PassthroughDSN != nil && *project.PassthroughDSN != ""
	var rawBuf bytes.Buffer
	parseFrom := body
	if forwarding {
		parseFrom = io.TeeReader(body, &rawBuf)
	}

	header, items, err := ingest.Parse(parseFrom)
	// Test the size caps before the parse error. An over-sized body is cut off
	// mid-item, so it always surfaces as a parse failure; reporting that as a
	// malformed envelope sends the operator hunting for a bug in their SDK.
	if rawLimit.exceeded() || sizeLimit.exceeded() {
		http.Error(w, "envelope too large", http.StatusRequestEntityTooLarge)
		return
	}
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
			// Some SDKs put the id only in the envelope header. It is the same
			// id, and a v1 profile links to it by value, so fall back rather
			// than lose the link.
			if tx.EventID == "" && eventID != nil {
				tx.EventID = trunc(*eventID, maxFieldLen)
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
				ingest.ScrubLog(&l, scrubCfg)
				ro.logBuf.Push(l) // best-effort; drop silently if full
			})

		case "profile", "profile_chunk":
			if ro.profBuf == nil || !project.ProfilingEnabled {
				continue
			}
			prof, err := ingest.ParseProfileItem(item.Header.Type, item.Payload)
			if err != nil {
				// A malformed or over-long profile is dropped on its own. The
				// transaction it travels with is still good data.
				slog.Debug("discarding profile", "project", project.Slug,
					"type", item.Header.Type, "err", err)
				continue
			}
			ingest.ScrubProfile(prof, scrubCfg)
			// Compressed here rather than in the writer so the buffer holds
			// bounded memory even when profiles arrive faster than they drain.
			buffered, err := ingest.NewBufferedProfile(project.ID, prof)
			if err != nil {
				slog.Error("encode profile", "project", project.Slug, "err", err)
				continue
			}
			// Best effort, like logs. Failing the envelope here would ask the
			// SDK to resend one that already had its transaction accepted a few
			// items earlier, and transactions carry no uniqueness guard, so the
			// retry would duplicate the transaction to save a profile. The
			// parse-error branch above drops the profile alone for the same
			// reason.
			if !ro.profBuf.Push(buffered) {
				slog.Debug("profile buffer full, dropping profile",
					"project", project.Slug, "type", item.Header.Type)
			}

		case "check_in":
			if ro.pool == nil {
				continue
			}
			handleEnvelopeCheckin(r.Context(), ro.pool, item.Payload)
		}
	}

	if forwarding {
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

// rawLog is a single log record as it appears on the wire.
type rawLog struct {
	Timestamp  json.RawMessage            `json:"timestamp"`
	Level      string                     `json:"level"`
	Body       string                     `json:"body"`
	TraceID    string                     `json:"trace_id"`
	SpanID     string                     `json:"span_id"`
	Attributes map[string]json.RawMessage `json:"attributes"`
}

// decodeLogRecords unpacks a log envelope item payload. Three shapes are
// accepted, in order of precedence:
//
//	{"version":2,"items":[...]}  current protocol, sent by every modern SDK
//	[{...},{...}]                bare array
//	{...}                        single record, as the obsolete otel_log draft sent
func decodeLogRecords(payload []byte) []rawLog {
	payload = bytes.TrimSpace(payload)
	if len(payload) == 0 {
		return nil
	}

	if payload[0] == '[' {
		var records []rawLog
		json.Unmarshal(payload, &records) //nolint:errcheck
		return records
	}

	// A pointer distinguishes an absent "items" key from an empty batch, so a
	// legitimately empty batch does not fall through to the single-record path.
	var wrapper struct {
		Items *[]rawLog `json:"items"`
	}
	if json.Unmarshal(payload, &wrapper) == nil && wrapper.Items != nil {
		return *wrapper.Items
	}

	var single rawLog
	if json.Unmarshal(payload, &single) == nil {
		return []rawLog{single}
	}
	return nil
}

// flattenLogAttributes unwraps Sentry's typed attribute values
// ({"value":"prod","type":"string"}) into plain JSON scalars, so that stored
// attributes stay queryable and render as values rather than as JSON blobs.
// Values not in the typed form are kept as they arrived.
func flattenLogAttributes(raw map[string]json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		var typed struct {
			Value *json.RawMessage `json:"value"`
			Type  string           `json:"type"`
		}
		// Both keys are required by the spec; demanding both avoids unwrapping
		// an untyped attribute that merely happens to have a "value" key.
		if json.Unmarshal(v, &typed) == nil && typed.Value != nil && typed.Type != "" {
			v = *typed.Value
		}
		var val any
		if json.Unmarshal(v, &val) == nil {
			out[k] = val
		}
	}
	return out
}

// normalizeLogLevel maps the log protocol's "warn" onto the "warning" spelling
// used everywhere else in the app (event levels, UI filters, level styling).
func normalizeLogLevel(level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	switch level {
	case "":
		return "info"
	case "warn":
		return "warning"
	}
	return level
}

// parseLogs parses a Sentry log envelope item payload and calls yield for each
// valid entry. Capped at 500 per item to prevent a single noisy service from
// filling the buffer.
func parseLogs(projectID string, payload []byte, yield func(ingest.BufferedLog)) {
	records := decodeLogRecords(payload)

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
		level := normalizeLogLevel(rec.Level)

		attrMap := flattenLogAttributes(rec.Attributes)

		var attrs json.RawMessage
		if len(attrMap) > 0 {
			attrs, _ = json.Marshal(attrMap)
		}

		// Extract environment and release from attributes if present.
		env, _ := attrMap["sentry.environment"].(string)
		rel, _ := attrMap["sentry.release"].(string)

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

// traceDataString reads one value out of the free-form trace data bag.
//
// A numeric thread id is coerced rather than dropped: ingest.flexString does
// the same on the profile side, so the two would otherwise fail to match for
// any SDK that sends thread.id unquoted, and the chunk would be folded across
// every thread instead of one.
func traceDataString(data map[string]any, key string) string {
	switch v := data[key].(type) {
	case string:
		return v
	case float64:
		// JSON numbers decode as float64. Thread ids are integers, so format
		// without an exponent or a trailing .0 that would never match.
		return strconv.FormatInt(int64(v), 10)
	default:
		return ""
	}
}

func parseTransaction(projectID string, payload []byte) *ingest.BufferedTransaction {
	var p struct {
		EventID        string          `json:"event_id"`
		Transaction    string          `json:"transaction"`
		StartTimestamp json.RawMessage `json:"start_timestamp"`
		Timestamp      json.RawMessage `json:"timestamp"`
		Contexts       struct {
			Trace struct {
				TraceID string `json:"trace_id"`
				SpanID  string `json:"span_id"`
				Op      string `json:"op"`
				Status  string `json:"status"`
				// Continuous profiling puts the sampled thread here, which is
				// what selects one thread's samples out of a chunk.
				//
				// Decoded loosely on purpose: this is a free-form bag and SDKs
				// mix numbers and booleans in alongside the strings. A typed map
				// fails on the first number, and the error path below throws the
				// entire transaction away.
				Data map[string]any `json:"data"`
			} `json:"trace"`
			Profile struct {
				ProfilerID string `json:"profiler_id"`
			} `json:"profile"`
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
		EventID:        trunc(p.EventID, maxFieldLen),
		ProfilerID:     trunc(p.Contexts.Profile.ProfilerID, maxFieldLen),
		ThreadID:       trunc(traceDataString(p.Contexts.Trace.Data, "thread.id"), maxFieldLen),
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
