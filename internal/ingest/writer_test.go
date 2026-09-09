package ingest_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/ingest"
)

func TestBuffer_Run_drainOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	eventID := "drain-test-evt-1"
	buf := ingest.NewBuffer(10)
	buf.Push(ingest.BufferedEvent{
		ProjectID: testProject.ID,
		EventID:   &eventID,
		Timestamp: time.Now(),
		Payload:   json.RawMessage(`{"level":"error"}`),
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var count int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM events WHERE project_id = $1 AND event_id = $2`,
		testProject.ID, eventID,
	).Scan(&count)

	if count != 1 {
		t.Errorf("expected 1 event in DB after drain, got %d", count)
	}
}

func TestBuffer_upsertsAppUser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	eventID := "app-user-evt-1"
	buf := ingest.NewBuffer(10)
	buf.Push(ingest.BufferedEvent{
		ProjectID: testProject.ID,
		EventID:   &eventID,
		Timestamp: time.Now(),
		Payload:   json.RawMessage(`{"level":"error","user":{"id":"u-evt","username":"erin"}}`),
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()
	cancel()
	<-done

	var username string
	err := testPool.QueryRow(context.Background(), `
		SELECT COALESCE(username, '') FROM app_users WHERE project_id = $1 AND identity = 'u-evt'
	`, testProject.ID).Scan(&username)
	if err != nil {
		t.Fatalf("app_users: %v", err)
	}
	if username != "erin" {
		t.Errorf("username: got %q", username)
	}
}

func TestBuffer_Run_tickerFlush(t *testing.T) {
	ctx := t.Context()

	eventID := "ticker-evt-1"
	buf := ingest.NewBuffer(10)
	buf.Push(ingest.BufferedEvent{
		ProjectID: testProject.ID,
		EventID:   &eventID,
		Timestamp: time.Now(),
		Payload:   json.RawMessage(`{"level":"warn"}`),
	})

	go buf.Run(ctx, testPool)

	// Poll until the ticker flushes the event or the deadline passes.
	// A fixed sleep is fragile under CI load; polling is resilient.
	deadline := time.Now().Add(2 * time.Second)
	var count int
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		testPool.QueryRow(context.Background(),
			`SELECT COUNT(*) FROM events WHERE project_id = $1 AND event_id = $2`,
			testProject.ID, eventID,
		).Scan(&count)
		if count == 1 {
			return
		}
	}
	t.Errorf("expected 1 event in DB after ticker flush, got %d", count)
}

func TestBuffer_Run_deduplicates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	eventID := "dedup-evt-1"
	buf := ingest.NewBuffer(10)
	// Push the same event_id twice
	for range 2 {
		buf.Push(ingest.BufferedEvent{
			ProjectID: testProject.ID,
			EventID:   &eventID,
			Timestamp: time.Now(),
			Payload:   json.RawMessage(`{"level":"error"}`),
		})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var count int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM events WHERE project_id = $1 AND event_id = $2`,
		testProject.ID, eventID,
	).Scan(&count)

	if count != 1 {
		t.Errorf("expected exactly 1 row (dedup), got %d", count)
	}
}

func TestTransactionBuffer_Run_drainOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	now := time.Now()
	buf := ingest.NewTransactionBuffer(10)
	buf.Push(ingest.BufferedTransaction{
		ProjectID:      testProject.ID,
		TraceID:        "trace-drain-1",
		SpanID:         "span-drain-1",
		Transaction:    "/drain/test",
		Op:             "http.server",
		Status:         "ok",
		DurationMs:     10,
		StartTimestamp: now,
		Timestamp:      now.Add(10 * time.Millisecond),
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var count int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM transactions WHERE project_id = $1 AND trace_id = $2`,
		testProject.ID, "trace-drain-1",
	).Scan(&count)

	if count != 1 {
		t.Errorf("expected 1 transaction in DB after drain, got %d", count)
	}
}

// TestBuffer_Run_withRelease ensures the release-upsert path in writeBatch is
// exercised. Events carrying a "release" JSON field cause writeBatch to INSERT
// into the releases table (lines 124-143 of buffer.go).
// Also passes a non-empty TraceID to cover nullableString's `return &s` branch.
func TestBuffer_Run_withRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	eventID := "release-event-cov1"
	buf := ingest.NewBuffer(10)
	buf.Push(ingest.BufferedEvent{
		ProjectID: testProject.ID,
		EventID:   &eventID,
		Timestamp: time.Now(),
		Payload:   json.RawMessage(`{"level":"error","release":"v1.0.0-cov"}`),
		TraceID:   "trace-release-cov-1",
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var count int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM releases WHERE project_id = $1 AND version = $2`,
		testProject.ID, "v1.0.0-cov",
	).Scan(&count)
	if count < 1 {
		t.Errorf("expected release row in DB, got %d", count)
	}
}

// TestTransactionBuffer_Run_withRelease ensures the release-upsert and nilJSON
// paths in writeTxBatch are exercised. A transaction with a non-empty Release,
// non-null Measurements, and at least one Span covers txbuffer.go lines 176-196
// (the release-upsert path, which is only reached after span insertion) and
// nilJSON's `return b` branch (line 210).
func TestTransactionBuffer_Run_withRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	now := time.Now()
	buf := ingest.NewTransactionBuffer(10)
	buf.Push(ingest.BufferedTransaction{
		ProjectID:      testProject.ID,
		TraceID:        "trace-release-tx-1",
		SpanID:         "span-release-tx-root",
		Transaction:    "/release/test",
		Op:             "http.server",
		Status:         "ok",
		DurationMs:     5,
		StartTimestamp: now,
		Timestamp:      now.Add(5 * time.Millisecond),
		Release:        "v2.0.0-cov",
		Measurements:   json.RawMessage(`{"lcp":{"value":200,"unit":"millisecond"}}`),
		Spans: []ingest.BufferedSpan{
			{
				SpanID:         "span-release-tx-child",
				ParentSpanID:   "span-release-tx-root",
				Op:             "db.query",
				Description:    "SELECT 1",
				StartTimestamp: now,
				Timestamp:      now.Add(2 * time.Millisecond),
				DurationMs:     2,
				Status:         "ok",
			},
		},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var count int
	testPool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM releases WHERE project_id = $1 AND version = $2`,
		testProject.ID, "v2.0.0-cov",
	).Scan(&count)
	if count < 1 {
		t.Errorf("expected release row in DB for tx release, got %d", count)
	}
}

func TestTransactionBuffer_Run_withSpans(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	now := time.Now()
	buf := ingest.NewTransactionBuffer(10)
	buf.Push(ingest.BufferedTransaction{
		ProjectID:      testProject.ID,
		TraceID:        "trace-spans-1",
		SpanID:         "span-root-1",
		Transaction:    "/spans/test",
		Op:             "http.server",
		Status:         "ok",
		DurationMs:     20,
		StartTimestamp: now,
		Timestamp:      now.Add(20 * time.Millisecond),
		Spans: []ingest.BufferedSpan{
			{
				SpanID:         "span-child-1",
				ParentSpanID:   "span-root-1",
				Op:             "db.query",
				Description:    "SELECT 1",
				StartTimestamp: now,
				Timestamp:      now.Add(5 * time.Millisecond),
				DurationMs:     5,
				Status:         "ok",
			},
		},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf.Run(ctx, testPool)
	}()

	cancel()
	<-done

	var spanCount int
	testPool.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM spans s
		JOIN transactions t ON t.id = s.transaction_id
		WHERE t.project_id = $1 AND t.trace_id = $2
	`, testProject.ID, "trace-spans-1").Scan(&spanCount)

	if spanCount != 1 {
		t.Errorf("expected 1 span in DB, got %d", spanCount)
	}
}

func TestBufferInvalidRecordPreservesHealthyEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	buf := ingest.NewBuffer(3)
	for i, payload := range []string{`{"message":"healthy"}`, `{"broken":`, `{"message":"also healthy"}`} {
		id := []string{"isolation-good-1", "isolation-bad", "isolation-good-2"}[i]
		buf.Push(ingest.BufferedEvent{ProjectID: testProject.ID, EventID: &id, Timestamp: time.Now(), Payload: json.RawMessage(payload)})
	}
	cancel()
	buf.Run(ctx, testPool)
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM events WHERE project_id=$1 AND event_id LIKE 'isolation-%'`, testProject.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	s := buf.Stats()
	if count != 2 || s.Persisted != 2 || s.Dropped["invalid_record"] != 1 || s.Pending != 0 {
		t.Fatalf("count=%d stats=%+v", count, s)
	}
}

func TestTransactionBufferSpanFailureRollsBackParent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	buf := ingest.NewTransactionBuffer(2)
	now := time.Now()
	for _, bad := range []bool{true, false} {
		tx := ingest.BufferedTransaction{ProjectID: testProject.ID, Transaction: "atomic-span-test", StartTimestamp: now, Timestamp: now, Status: "ok", Op: "http.server"}
		if bad {
			tx.Spans = []ingest.BufferedSpan{{SpanID: "bad-json", StartTimestamp: now, Timestamp: now, Data: json.RawMessage(`{"broken":`)}}
		}
		buf.Push(tx)
	}
	hookItems := 0
	buf.Hook = func(_ context.Context, _ *pgxpool.Pool, txs []ingest.BufferedTransaction, ids []string) {
		hookItems += len(txs)
		if len(ids) != len(txs) {
			t.Errorf("hook ids=%v", ids)
		}
	}
	cancel()
	buf.Run(ctx, testPool)
	var count int
	if err := testPool.QueryRow(context.Background(), `SELECT COUNT(*) FROM transactions WHERE project_id=$1 AND transaction='atomic-span-test'`, testProject.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if s := buf.Stats(); count != 1 || s.Persisted != 1 || s.Dropped["invalid_record"] != 1 || hookItems != 1 {
		t.Fatalf("count=%d hook=%d stats=%+v", count, hookItems, s)
	}
}

func TestLogBufferRetriesRolledBackBatchWithoutDuplicates(t *testing.T) {
	ctx := context.Background()
	_, err := testPool.Exec(ctx, `
 CREATE SEQUENCE ingest_retry_test_seq;
 CREATE FUNCTION ingest_retry_test() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN
  IF NEW.body = 'retry-transient-test' AND nextval('ingest_retry_test_seq') = 1 THEN
   RAISE EXCEPTION 'test serialization failure' USING ERRCODE = '40001';
  END IF;
  RETURN NEW;
 END;
 $$;
 CREATE TRIGGER ingest_retry_test_trigger BEFORE INSERT ON logs FOR EACH ROW EXECUTE FUNCTION ingest_retry_test();
 `)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, err := testPool.Exec(ctx, `DROP TRIGGER ingest_retry_test_trigger ON logs; DROP FUNCTION ingest_retry_test(); DROP SEQUENCE ingest_retry_test_seq;`)
		if err != nil {
			t.Error(err)
		}
	})
	buf := ingest.NewLogBuffer(2)
	for _, body := range []string{"retry-healthy-test", "retry-transient-test"} {
		buf.Push(ingest.BufferedLog{ProjectID: testProject.ID, Timestamp: time.Now(), Level: "info", Body: body})
	}
	runCtx, cancel := context.WithCancel(ctx)
	cancel()
	buf.Run(runCtx, testPool)
	var count int
	if err := testPool.QueryRow(ctx, `SELECT COUNT(*) FROM logs WHERE project_id=$1 AND body IN ('retry-healthy-test','retry-transient-test')`, testProject.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if s := buf.Stats(); count != 2 || s.Persisted != 2 || s.Retries != 1 || s.FailedWrites != 1 {
		t.Fatalf("count=%d stats=%+v", count, s)
	}
}

func TestBufferSanitizedPayloadUsedByEnrichment(t *testing.T) {
	for _, tt := range []struct{ name, payload, identity, release string }{
		{"user", `{"user":{"id":"nul\u0000user","name":"A\u0000B"}}`, "nuluser", ""},
		{"literal", `{"user":{"id":"literal\\u0000user"},"release":"literal\\u0000release"}`, `literal\u0000user`, `literal\u0000release`},
		{"release", `{"release":"nul\u0000release"}`, "", "nulrelease"},
		{"both", `{"user":{"id":"both\u0000user"},"release":"both\u0000release"}`, "bothuser", "bothrelease"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			buf := ingest.NewBuffer(1)
			id := "sanitized-enrichment-" + tt.name
			payload := json.RawMessage(tt.payload)
			if !buf.Push(ingest.BufferedEvent{ProjectID: testProject.ID, EventID: &id, Timestamp: time.Now(), Payload: payload}) {
				t.Fatal("push rejected")
			}
			if string(payload) != tt.payload {
				t.Fatal("Push modified caller's payload")
			}
			cancel()
			buf.Run(ctx, testPool)
			var identity, release string
			err := testPool.QueryRow(context.Background(), `SELECT COALESCE(user_identity,''),COALESCE(release,'') FROM events WHERE project_id=$1 AND event_id=$2`, testProject.ID, id).Scan(&identity, &release)
			if err != nil {
				t.Fatal(err)
			}
			if identity != tt.identity || release != tt.release {
				t.Fatalf("stored identity=%q release=%q", identity, release)
			}
			if identity != "" {
				var count int
				if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM app_users WHERE project_id=$1 AND identity=$2`, testProject.ID, identity).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("app-user rows=%d", count)
				}
			}
			if release != "" {
				var count int
				if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM releases WHERE project_id=$1 AND version=$2`, testProject.ID, release).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("release rows=%d", count)
				}
			}
			if s := buf.Stats(); s.Persisted != 1 || s.FailedWrites != 0 || s.Pending != 0 {
				t.Fatalf("sanitized event failed: %+v", s)
			}
		})
	}
}
