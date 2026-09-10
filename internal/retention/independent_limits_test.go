package retention_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/retention"
)

func TestRowLimitsWithoutAgeRetention(t *testing.T) {
	for _, table := range []string{"logs", "transactions"} {
		for _, profile := range []string{"disabled", "age", "storage", "both"} {
			t.Run(table+"/"+profile, func(t *testing.T) {
				seedCapRows(t, table, 3)
				clearProfiles(t)
				// All rows are old enough to expose accidental age-policy activation.
				_, err := testPool.Exec(t.Context(), "UPDATE "+table+" SET received_at=NOW()-interval '400 days'")
				require.NoError(t, err)
				insertProfile(t, 30, 1<<20)
				insertProfile(t, 1, 1<<20)
				logs, tx, days, mb := 0, 0, 0, 0
				if table == "logs" {
					logs = 1
				} else {
					tx = 1
				}
				if profile == "age" || profile == "both" {
					days = 7
				}
				if profile == "storage" || profile == "both" {
					mb = 1
				}
				w := retention.NewWorker(testPool, 0).WithRowLimits(logs, tx).WithProfileLimits(days, mb)
				require.False(t, w.RunOnce(t.Context()), "small purges must not exhaust the pass budget")
				require.Equal(t, 1, capCount(t, table))
				var id string
				require.NoError(t, testPool.QueryRow(t.Context(), "SELECT id::text FROM "+table).Scan(&id))
				require.Equal(t, "00000000-0000-0000-0000-000000000003", id, "keep the newest row despite its age")
				wantProfiles := 1
				if profile == "disabled" {
					wantProfiles = 2
				}
				require.Equal(t, wantProfiles, countProfiles(t))
				require.False(t, w.RunOnce(t.Context()))
				require.Equal(t, 1, capCount(t, table), "repeated passes must preserve rows within the cap")
			})
		}
	}
}

func TestRowOnlyWorkerRunsImmediately(t *testing.T) {
	for _, table := range []string{"logs", "transactions"} {
		t.Run(table, func(t *testing.T) {
			seedCapRows(t, table, 3)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			trace := &capTrace{cancel: cancel}
			logs, tx := 0, 0
			if table == "logs" {
				logs = 1
			} else {
				tx = 1
			}
			retention.NewWorker(capPool(t, trace), 0).WithRowLimits(logs, tx).Run(ctx)
			require.Equal(t, []int64{2}, trace.batches, fmt.Sprintf("%s cap must start the retention loop", table))
			require.Equal(t, 1, capCount(t, table))
		})
	}
}

func TestRowCapsApplyOnlyToConfiguredTables(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		logs, transactions         int
		wantLogs, wantTransactions int
	}{
		{"logs only", 1, 0, 1, 3},
		{"transactions only", 0, 2, 3, 2},
		{"both", 1, 2, 1, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedCapRows(t, "logs", 3)
			_, err := testPool.Exec(t.Context(), `
				INSERT INTO transactions(project_id,start_timestamp,timestamp,received_at,transaction,duration_ms)
				SELECT $1,NOW()-interval '400 days',NOW()-interval '400 days',
				NOW()-interval '400 days','/independent-cap',1 FROM generate_series(1,3)`, testProject.ID)
			require.NoError(t, err)
			require.False(t, retention.NewWorker(testPool, 0).WithRowLimits(tc.logs, tc.transactions).RunOnce(t.Context()))
			require.Equal(t, tc.wantLogs, capCount(t, "logs"))
			require.Equal(t, tc.wantTransactions, capCount(t, "transactions"))
		})
	}
}
