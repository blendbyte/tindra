package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/blendbyte/tindra/internal/ingest"
)

// drain runs a buffer on a cancelled context, which flushes everything queued
// and returns.
func drain(run func(context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run(ctx)
}

// NUL is valid in SDK JSON but rejected by PostgreSQL text and JSONB columns.
// Before sanitizing, each of these records was dropped as invalid_record.

func TestLogBuffer_stripsNUL(t *testing.T) {
	buf := ingest.NewLogBuffer(1)
	buf.Push(ingest.BufferedLog{
		ProjectID:   testProject.ID,
		Timestamp:   time.Now(),
		Level:       "info",
		Body:        "nul\x00log body",
		Environment: "nul\x00env",
		Attributes:  json.RawMessage(`{"a\u0000b":"x\u0000y"}`),
	})
	drain(func(ctx context.Context) { buf.Run(ctx, testPool) })

	if s := buf.Stats(); s.Persisted != 1 {
		t.Fatalf("log not persisted: %+v", s)
	}
	var env string
	var attrs map[string]any
	err := testPool.QueryRow(context.Background(),
		`SELECT environment, attributes FROM logs WHERE project_id=$1 AND body='nullog body'`,
		testProject.ID).Scan(&env, &attrs)
	if err != nil {
		t.Fatal(err)
	}
	if env != "nulenv" || attrs["ab"] != "xy" || len(attrs) != 1 {
		t.Errorf("environment=%q attributes=%v", env, attrs)
	}
}

func TestTransactionBuffer_stripsNUL(t *testing.T) {
	now := time.Now()
	buf := ingest.NewTransactionBuffer(1)
	buf.Push(ingest.BufferedTransaction{
		ProjectID:      testProject.ID,
		Transaction:    "GET /nul\x00tx",
		Op:             "http.server",
		Status:         "ok",
		StartTimestamp: now,
		Timestamp:      now,
		Release:        "nul\x00release-tx",
		Measurements:   json.RawMessage(`{"lcp\u0000":{"value":1}}`),
		Spans: []ingest.BufferedSpan{{
			SpanID:         "nul-span",
			Op:             "db",
			Description:    "SELECT\x00 1",
			Status:         "ok",
			StartTimestamp: now,
			Timestamp:      now,
			Data:           json.RawMessage(`{"k":"v\u0000"}`),
		}},
	})
	drain(func(ctx context.Context) { buf.Run(ctx, testPool) })

	if s := buf.Stats(); s.Persisted != 1 {
		t.Fatalf("transaction not persisted: %+v", s)
	}
	ctx := context.Background()
	var release string
	var measurements map[string]any
	err := testPool.QueryRow(ctx,
		`SELECT release, measurements FROM transactions WHERE project_id=$1 AND transaction='GET /nultx'`,
		testProject.ID).Scan(&release, &measurements)
	if err != nil {
		t.Fatal(err)
	}
	if release != "nulrelease-tx" || measurements["lcp"] == nil {
		t.Errorf("release=%q measurements=%v", release, measurements)
	}
	var description string
	var data map[string]any
	err = testPool.QueryRow(ctx,
		`SELECT description, data FROM spans WHERE project_id=$1 AND span_id='nul-span'`,
		testProject.ID).Scan(&description, &data)
	if err != nil {
		t.Fatal(err)
	}
	if description != "SELECT 1" || data["k"] != "v" {
		t.Errorf("description=%q data=%v", description, data)
	}
	var n int
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM releases WHERE project_id=$1 AND version='nulrelease-tx'`,
		testProject.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("release row count=%d err=%v", n, err)
	}
}

func TestBuffer_stripsNULFromTraceContext(t *testing.T) {
	id := "nul-trace-context-event"
	buf := ingest.NewBuffer(1)
	buf.Push(ingest.BufferedEvent{
		ProjectID: testProject.ID,
		EventID:   &id,
		Timestamp: time.Now(),
		Payload:   json.RawMessage(`{}`),
		TraceID:   "nul\x00trace",
		SpanID:    "nul\x00span",
	})
	drain(func(ctx context.Context) { buf.Run(ctx, testPool) })

	var traceID, spanID string
	err := testPool.QueryRow(context.Background(),
		`SELECT trace_id, span_id FROM events WHERE project_id=$1 AND event_id=$2`,
		testProject.ID, id).Scan(&traceID, &spanID)
	if err != nil {
		t.Fatal(err)
	}
	if traceID != "nultrace" || spanID != "nulspan" {
		t.Errorf("trace_id=%q span_id=%q", traceID, spanID)
	}
}

func TestProfileBuffer_stripsNUL(t *testing.T) {
	p := stubProfile(t, "v1_php_laravel.json", "profile")
	p.TransactionEventID = "nul\x00profile-tx"
	p.Environment = "nul\x00env"
	buf := ingest.NewProfileBuffer(1)
	buf.Push(p)
	drain(func(ctx context.Context) { buf.Run(ctx, testPool) })

	if s := buf.Stats(); s.Persisted != 1 {
		t.Fatalf("profile not persisted: %+v", s)
	}
	var env string
	err := testPool.QueryRow(context.Background(),
		`SELECT environment FROM profile_chunks WHERE project_id=$1 AND transaction_event_id='nulprofile-tx'`,
		testProject.ID).Scan(&env)
	if err != nil {
		t.Fatal(err)
	}
	if env != "nulenv" {
		t.Errorf("environment=%q", env)
	}
}
