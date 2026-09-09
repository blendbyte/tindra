package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestCombinedCountMatchesSeparateTriggers(t *testing.T) {
	ctx := context.Background()
	_, err := testPool.Exec(ctx, "DELETE FROM issues WHERE project_id=$1", testProject.ID)
	require.NoError(t, err)
	since := time.Now().Add(-time.Hour)
	_, err = testPool.Exec(ctx, `INSERT INTO issues(project_id,fingerprint,title,level,kind,environment,event_count,first_seen,last_seen,regressed_at)
 SELECT $1,'combined-'||i,'Combined',CASE WHEN i%2=0 THEN 'error' ELSE 'warning' END,'error',CASE WHEN i%3=0 THEN 'production' ELSE 'staging' END,i,
 $2::timestamptz+CASE WHEN i%2=0 THEN interval '1 minute' ELSE interval '-1 minute' END,now(),
 $2::timestamptz+CASE WHEN i%5=0 THEN interval '1 minute' ELSE interval '-1 minute' END
 FROM generate_series(1,30) i`, testProject.ID, since)
	require.NoError(t, err)
	level, environment, min := "error", "production", 10
	for _, ids := range [][]string{nil, {testProject.ID}} {
		for _, filtered := range []bool{false, true} {
			rule := &storage.AlertRule{ProjectIDs: ids, CreatedAt: since.Add(-time.Hour), LastFiredAt: &since}
			if filtered {
				rule.FilterLevel = &level
				rule.FilterEnvironment = &environment
				rule.MinOccurrences = &min
			}
			expected := map[string]int{}
			for _, trigger := range []string{"new_issue", "regressed"} {
				rule.Trigger = trigger
				_, details, err := testEvaluator(nil).conditionMet(ctx, rule)
				require.NoError(t, err)
				for key, value := range details {
					expected[key] = value.(int)
				}
			}
			rule.Trigger = "new_or_regressed"
			met, details, err := testEvaluator(nil).conditionMet(ctx, rule)
			require.NoError(t, err)
			require.True(t, met)
			require.Equal(t, expected["new_issue_count"], details["new_issue_count"])
			require.Equal(t, expected["regressed_count"], details["regressed_count"])
		}
	}
}
