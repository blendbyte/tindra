package storage_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestIssueCountIdentityParity(t *testing.T) {
	truncateProjects(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "identity-count", "Identity")
	require.NoError(t, err)
	issue, _, _, err := storage.UpsertIssue(ctx, testPool, p.ID, "identity-count", "Identity", "error", "", "", "", time.Now())
	require.NoError(t, err)
	payloads := []string{`{}`, `{"user":null}`, `{"user":{}}`, `{"user":"scalar"}`, `{"user":[]}`,
		`{"user":{"id":"same","username":"ignored","email":"ignored"}}`, `{"user":{"id":"same"}}`,
		`{"user":{"id":"","username":"must-not-fall-back"}}`, `{"user":{"id":null,"username":"username","email":"ignored"}}`,
		`{"user":{"email":"email"}}`, `{"user":{"ip_address":"ip"}}`, `{"user":{"id":42}}`, `{"user":{"id":true}}`}
	longIdentity, err := json.Marshal(map[string]any{"user": map[string]string{"id": strings.Repeat("long-identity-", 1000)}})
	require.NoError(t, err)
	payloads = append(payloads, string(longIdentity))
	for _, payload := range payloads {
		_, err = testPool.Exec(ctx, `INSERT INTO events(project_id,issue_id,timestamp,payload) VALUES($1,$2,now(),$3)`, p.ID, issue.ID, json.RawMessage(payload))
		require.NoError(t, err)
	}
	check := func() {
		var old int
		require.NoError(t, testPool.QueryRow(ctx, `SELECT count(DISTINCT COALESCE(payload->'user'->>'id',payload->'user'->>'username',payload->'user'->>'email',payload->'user'->>'ip_address')) FROM events WHERE issue_id=$1`, issue.ID).Scan(&old))
		detail, err := storage.GetIssue(ctx, testPool, issue.ID)
		require.NoError(t, err)
		require.EqualValues(t, old, detail.UserCount)
		list, err := storage.ListAllIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{p.ID}})
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.EqualValues(t, old, list[0].UserCount)
	}
	check()
	_, err = testPool.Exec(ctx, `UPDATE events SET payload='{"user":{"email":"scrubbed"}}' WHERE project_id=$1`, p.ID)
	require.NoError(t, err)
	check()
	_, err = testPool.Exec(ctx, `DELETE FROM events WHERE project_id=$1`, p.ID)
	require.NoError(t, err)
	check()
}
