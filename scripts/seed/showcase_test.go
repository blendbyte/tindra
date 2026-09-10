package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/api"
	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/issues"
	"github.com/blendbyte/tindra/internal/storage"
	"github.com/blendbyte/tindra/internal/testutil"
)

func assertWaterfall(t *testing.T, tx map[string]any) {
	t.Helper()
	parse := func(value any) time.Time {
		at, err := time.Parse(time.RFC3339Nano, value.(string))
		require.NoError(t, err)
		return at
	}
	start, end := parse(tx["start_timestamp"]), parse(tx["timestamp"])
	trace := tx["contexts"].(map[string]any)["trace"].(map[string]any)
	require.Len(t, trace["trace_id"], 32)
	bounds := map[string][2]time.Time{trace["span_id"].(string): {start, end}}
	for _, span := range tx["spans"].([]map[string]any) {
		parent, ok := bounds[span["parent_span_id"].(string)]
		require.True(t, ok, "missing parent")
		a, b := parse(span["start_timestamp"]), parse(span["timestamp"])
		require.False(t, a.Before(parent[0]), "span begins before parent")
		require.False(t, b.After(parent[1]), "span ends after parent")
		require.True(t, b.After(a), "empty span")
		bounds[span["span_id"].(string)] = [2]time.Time{a, b}
	}
}

func TestShowcaseWaterfalls(t *testing.T) {
	now := time.Now().Add(-time.Hour)
	for _, def := range projectRegistry {
		for _, tmpl := range def.txs {
			t.Run(def.name+"/"+tmpl.name, func(t *testing.T) {
				for range 8 {
					assertWaterfall(t, buildTransaction(tmpl, now, def.releases, def.environments))
				}
			})
		}
	}
	for _, tmpl := range laravelProfiles {
		tx, _ := buildV1Profile(tmpl, now, testReleases, testEnvs)
		assertWaterfall(t, tx)
	}
	for _, tmpl := range pythonProfiles {
		_, txs := buildV2Session(tmpl, now, 3, testReleases, testEnvs)
		for _, tx := range txs {
			assertWaterfall(t, tx)
		}
	}
}

func TestShowcaseTimeAndReleaseCoverage(t *testing.T) {
	for range 500 {
		before := time.Now().Add(-7 * 24 * time.Hour)
		at := trafficBiasedTime(7 * 24 * time.Hour)
		require.False(t, at.Before(before))
		require.False(t, at.After(time.Now()))
	}
	for i := range 20 {
		at := showcaseTime(i, 30)
		require.WithinDuration(t, time.Now(), at, 24*time.Hour)
		require.True(t, at.Before(time.Now()))
	}
	releases := []string{"1.0", "1.1", "1.2", "1.3"}
	for i := range releases {
		require.Equal(t, releases[3-i], releaseAt(time.Now().Add(-time.Duration(i)*42*time.Hour-time.Hour), releases))
	}
	require.Equal(t, "1.0", releaseAt(time.Now().Add(-30*24*time.Hour), releases))
	levels := map[any]bool{}
	for _, record := range buildFreshLogs(testReleases, testEnvs) {
		levels[record["level"]] = true
	}
	require.Len(t, levels, 6)
}

func TestInvestigationLinksAndEnvelope(t *testing.T) {
	for _, def := range projectRegistry {
		for i := range 4 {
			items, id := buildInvestigation(def, i)
			header, parsed, err := ingest.Parse(bytes.NewReader(buildEnvelope(items)))
			require.NoError(t, err)
			require.Equal(t, id, header.EventID)
			require.Len(t, parsed, 3)
			event := items[0].payload.(map[string]any)
			tx := items[1].payload.(map[string]any)
			require.NotEqual(t, id, tx["event_id"])
			assertWaterfall(t, tx)
			trace := tx["contexts"].(map[string]any)["trace"].(map[string]any)
			require.Equal(t, trace, event["contexts"].(map[string]any)["trace"])
			require.Equal(t, tx["user"], event["user"])
			for _, record := range items[2].payload.([]map[string]any) {
				require.Equal(t, trace["trace_id"], record["trace_id"])
				require.Equal(t, seedUsers[0].id, record["attributes"].(map[string]any)["user.id"])
			}
		}
	}
}

// Exercise real HTTP ingestion, asynchronous writers/grouping, and the migrated schema.
func TestShowcaseDatabaseAndIngestion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pool, databaseURL, cleanup := testutil.SetupDBWithDSN(ctx)
	defer cleanup()
	defer cancel()
	project, err := storage.CreateProject(ctx, pool, "showcase", "Commerce Demo")
	require.NoError(t, err)
	other, err := storage.CreateProject(ctx, pool, "untouched", "Existing project")
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO users(email,password_hash,perm_manage_users) VALUES('demo@example.com','unusable',true)`)
	require.NoError(t, err)
	buf, txbuf, logbuf, profbuf := ingest.NewBuffer(5000), ingest.NewTransactionBuffer(5000), ingest.NewLogBuffer(5000), ingest.NewProfileBuffer(100)
	var workers sync.WaitGroup
	workers.Go(func() { buf.Run(ctx, pool) })
	workers.Go(func() { txbuf.Run(ctx, pool) })
	workers.Go(func() { logbuf.Run(ctx, pool) })
	workers.Go(func() { profbuf.Run(ctx, pool) })
	workers.Go(func() { issues.NewGrouper(pool).Run(ctx) })
	defer func() { cancel(); workers.Wait() }()
	router := api.NewRouter(pool, buf, txbuf, logbuf, profbuf, nil, nil, false, "", "", "", "", 30, 0, 0, 0, 10000, 10000, nil, false, false, nil)
	server := httptest.NewServer(router)
	defer server.Close()
	target := dsn{baseURL: server.URL, projectID: project.ID, publicKey: project.PublicKey}
	require.NoError(t, seedReleases(ctx, pool, project.ID, projectRegistry["mixed"].releases))
	require.NoError(t, seedReleases(ctx, pool, project.ID, projectRegistry["mixed"].releases))
	var ids []string
	for i := range 4 {
		items, id := buildInvestigation(projectRegistry["mixed"], i)
		ids = append(ids, id)
		wait, err := send(target, buildEnvelope(items))
		require.NoError(t, err)
		require.Zero(t, wait)
	}
	require.NoError(t, seedWorkflow(ctx, pool, project.ID, ids))
	require.Zero(t, seedCronMonitors(target, pool))
	require.Zero(t, seedUptimeMonitors(pool, project.ID))
	require.Zero(t, seedAlertRules(pool, project.ID))
	var n int
	for query, want := range map[string]int{
		`SELECT count(*) FROM issue_history WHERE event_type='regressed'`:                                          1,
		`SELECT count(*) FROM issue_history WHERE event_type='status_changed' AND details->>'from'=details->>'to'`: 0,
		`SELECT count(*) FROM releases`:                                      4,
		`SELECT count(*) FROM issues WHERE assignee_id IS NOT NULL`:          4,
		`SELECT count(DISTINCT status) FROM issues`:                          4,
		`SELECT count(*) FROM issue_comments`:                                4,
		`SELECT count(*) FROM cron_monitors`:                                 7,
		`SELECT count(*) FROM uptime_monitors`:                               6,
		`SELECT count(*) FROM alert_rules WHERE enabled`:                     0,
		`SELECT count(*) FROM alert_firings WHERE next_retry_at IS NOT NULL`: 0,
		`SELECT count(*) FROM alert_rule_projects`:                           7,
		`SELECT count(*) FROM uptime_monitors WHERE next_check_at <= NOW()`:  0,
		`SELECT count(*) FROM cron_monitors WHERE next_expected_at <= NOW()`: 0,
	} {
		require.NoError(t, pool.QueryRow(ctx, query).Scan(&n))
		require.Equal(t, want, n, query)
	}
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM alert_rule_projects WHERE project_id=$1`, other.ID).Scan(&n))
	require.Zero(t, n)
	require.Eventually(t, func() bool {
		err := pool.QueryRow(ctx, `SELECT count(*) FROM logs l JOIN transactions t ON t.trace_id=l.trace_id JOIN events e ON e.trace_id=t.trace_id WHERE l.project_id=$1`, project.ID).Scan(&n)
		return err == nil && n == 12
	}, 10*time.Second, 100*time.Millisecond)

	// Run the documented CLI against a new project, covering section orchestration
	// and all default fixtures rather than only the individual builders.
	complete, err := storage.CreateProject(ctx, pool, "full-demo", "Commerce Suite")
	require.NoError(t, err)
	commandCtx, commandCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer commandCancel()
	command := exec.CommandContext(commandCtx, "go", "run", "main.go", "--db", databaseURL, fmt.Sprintf("http://%s@%s/%s", complete.PublicKey, server.Listener.Addr(), complete.ID))
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.Contains(t, string(output), "0 failures")
	require.Eventually(t, func() bool {
		var txs, events, logs, profiles int
		err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM transactions WHERE project_id=$1),(SELECT count(*) FROM events WHERE project_id=$1),(SELECT count(*) FROM logs WHERE project_id=$1),(SELECT count(*) FROM profile_chunks WHERE project_id=$1)`, complete.ID).Scan(&txs, &events, &logs, &profiles)
		return err == nil && txs > 200 && events > 100 && logs >= 400 && profiles > 0
	}, 15*time.Second, 100*time.Millisecond)

	for _, category := range []string{"db", "cache", "job"} {
		summaries, err := storage.GetSpanSummaries(ctx, pool, category, []string{complete.ID}, 24, "production", "")
		require.NoError(t, err)
		require.NotEmpty(t, summaries, category)
		if category == "cache" {
			for _, summary := range summaries {
				if summary.Op == "cache.get" {
					require.NotNil(t, summary.MissRate)
				} else {
					require.Nil(t, summary.MissRate)
				}
			}
		}
	}
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM transactions WHERE project_id=$1 AND op IN ('pageload','navigation') AND start_timestamp>NOW()-INTERVAL '24 hours' AND measurements->'lcp' IS NOT NULL`, complete.ID).Scan(&n))
	require.Positive(t, n)
	// Context cancellation must stop waiting for unavailable event grouping.
	stopped, stop := context.WithCancel(ctx)
	stop()
	require.Error(t, seedWorkflow(stopped, pool, project.ID, []string{"missing"}))
}

func TestSeedDSNValidation(t *testing.T) {
	for _, raw := range []string{"file:///tmp/db", "https://host/project", "https://key@/project", "https://key@host/project?q=x", "https://key@host/project#fragment"} {
		_, err := parseDSN(raw)
		require.Error(t, err, raw)
	}
	parsed, err := parseDSN("http://key@localhost:8080/project")
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8080/api/project/envelope/", parsed.envelopeURL())
}

func TestSendResponses(t *testing.T) {
	for _, status := range []int{200, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
			}))
			defer server.Close()
			wait, err := send(dsn{baseURL: server.URL, projectID: "demo"}, []byte("{}\n"))
			if status == 500 {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if status == 429 {
				require.Equal(t, 60*time.Second, wait)
			}
		})
	}
}

// Keep envelope fixtures serializable, including optional attributes.
func TestSeedFixtureJSON(t *testing.T) {
	for _, def := range projectRegistry {
		for _, tmpl := range def.issues {
			_, err := json.Marshal(buildErrorEvent(tmpl, time.Now(), def.releases, def.environments))
			require.NoError(t, err)
		}
	}
}

func TestBrowserVitalsStayWithinNavigation(t *testing.T) {
	for range 200 {
		tx := buildTransaction(jsTxs[0], time.Now().Add(-time.Minute), testReleases, testEnvs)
		measurements := tx["measurements"].(map[string]any)
		value := func(key string) float64 { return measurements[key].(map[string]any)["value"].(float64) }
		require.LessOrEqual(t, value("ttfb"), value("fcp"))
		require.LessOrEqual(t, value("fcp"), value("lcp"))
		start, _ := time.Parse(time.RFC3339Nano, tx["start_timestamp"].(string))
		end, _ := time.Parse(time.RFC3339Nano, tx["timestamp"].(string))
		require.GreaterOrEqual(t, float64(end.Sub(start))/float64(time.Millisecond)+0.001, value("lcp"))
	}
}

func TestSpanMetadataMatchesOperation(t *testing.T) {
	for _, op := range []string{"cache.put", "cache.delete"} {
		data := spanData(op, "orders:recent")
		require.NotContains(t, data, "cache.hit")
		require.Equal(t, "orders:recent", data["cache.key"])
	}
	require.Equal(t, false, spanData("cache.get", "dashboard (miss)")["cache.hit"])
	require.IsType(t, true, spanData("cache.get", "orders")["cache.hit"])
	require.Equal(t, "mysql", spanData("db.query", "SELECT `id` FROM `orders`")["db.system"])
	require.Equal(t, "postgresql", spanData("db.query", "SELECT id FROM orders")["db.system"])
}
