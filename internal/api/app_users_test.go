package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/ingest"
	"github.com/blendbyte/tindra/internal/storage"
)

func TestAppUsersEndpoint_listingLookupAndScope(t *testing.T) {
	ctx := context.Background()
	p1, err := storage.CreateProject(ctx, testPool, "app-users-api-a", "App users A")
	require.NoError(t, err)
	p2, err := storage.CreateProject(ctx, testPool, "app-users-api-b", "App users B")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = storage.DeleteProject(context.Background(), testPool, p1.ID)
		_, _ = storage.DeleteProject(context.Background(), testPool, p2.ID)
	})
	now := time.Now().UTC()
	ingest.UpsertAppUsers(ctx, testPool, []ingest.AppUserRow{
		{ProjectID: p1.ID, User: ingest.SentryUser{Identity: "api-alice", ID: "api-alice", Name: "Alice Picker", Email: "alice@picker.test"}, LastSeen: now},
		{ProjectID: p2.ID, User: ingest.SentryUser{Identity: "api-bob", ID: "api-bob", Name: "Bob Picker"}, LastSeen: now.Add(-time.Minute)},
	})
	_, token, err := storage.CreateAPIToken(ctx, testPool, p1.ID, "picker-scope", false)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, path string
		bearer     bool
		want       []string
	}{
		{"project list", "/api/app-users?project_id=" + p1.ID, false, []string{"api-alice"}},
		{"global search", "/api/app-users?q=Picker", false, []string{"api-alice", "api-bob"}},
		{"exact lookup", "/api/app-users?identity=api-alice", false, []string{"api-alice"}},
		{"missing identity", "/api/app-users?identity=missing-picker-user", false, []string{}},
		{"empty search result", "/api/app-users?q=missing-picker-user", false, []string{}},
		{"search takes precedence", "/api/app-users?identity=api-bob&q=alice%40picker", false, []string{"api-alice"}},
		{"bearer overrides requested project", "/api/app-users?project_id=" + p2.ID, true, []string{"api-alice"}},
		{"bearer cannot look up other project", "/api/app-users?identity=api-bob&project_id=" + p2.ID, true, []string{}},
		{"bearer cannot search other project", "/api/app-users?q=Bob%20Picker", true, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			if tc.bearer {
				req.Header.Set("Authorization", "Bearer "+token)
			} else {
				req.AddCookie(authCookie())
			}
			rec := httptest.NewRecorder()
			globalHandler().ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var users []*storage.AppUser
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &users))
			require.NotNil(t, users, "empty results must be [] rather than null")
			identities := []string{}
			for _, u := range users {
				identities = append(identities, u.Identity)
			}
			require.Equal(t, tc.want, identities)
		})
	}
	req := httptest.NewRequest(http.MethodGet, "/api/app-users", nil)
	rec := httptest.NewRecorder()
	globalHandler().ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestTransactionsEndpoint_userWindowOpsAndPagination(t *testing.T) {
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, testPool, "user-filter-api", "User filter API")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = storage.DeleteProject(context.Background(), testPool, p.ID) })
	for _, row := range []struct {
		name, op, user string
		age            time.Duration
	}{
		{"/page", "pageload", "alice", time.Minute}, {"/navigate", "navigation", "alice", 2 * time.Minute},
		{"/server", "http.server", "alice", 3 * time.Minute}, {"/old", "pageload", "alice", 48 * time.Hour},
		{"/bob", "pageload", "bob", time.Minute},
	} {
		_, err := testPool.Exec(ctx, `INSERT INTO transactions(project_id,transaction,op,status,duration_ms,start_timestamp,timestamp,user_identity)
   VALUES($1,$2,$3,'ok',10,$4,$4,$5)`, p.ID, row.name, row.op, time.Now().UTC().Add(-row.age), row.user)
		require.NoError(t, err)
	}
	type page struct {
		Transactions []storage.Transaction `json:"transactions"`
		Time         string                `json:"next_cursor_time"`
		ID           string                `json:"next_cursor_id"`
	}
	fetch := func(q url.Values) page {
		req := httptest.NewRequest(http.MethodGet, "/api/transactions?"+q.Encode(), nil)
		req.AddCookie(authCookie())
		rec := httptest.NewRecorder()
		globalHandler().ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out page
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out
	}
	q := url.Values{"project_id": {p.ID}, "user": {"alice"}, "hours": {"1"}, "op": {"pageload", "navigation"}, "limit": {"1"}}
	first := fetch(q)
	require.Len(t, first.Transactions, 1)
	require.Equal(t, "/page", first.Transactions[0].Transaction)
	require.NotEmpty(t, first.ID)
	require.NotEmpty(t, first.Time)
	q.Set("cursor_id", first.ID)
	q.Set("cursor_time", first.Time)
	second := fetch(q)
	require.Len(t, second.Transactions, 1)
	require.Equal(t, "/navigate", second.Transactions[0].Transaction)
	q.Del("cursor_id")
	q.Del("cursor_time")
	q.Set("limit", "50")
	q["op"] = []string{"pageload"}
	onlyPage := fetch(q)
	require.Len(t, onlyPage.Transactions, 1)
	require.Equal(t, "/page", onlyPage.Transactions[0].Transaction)
	// Invalid/out-of-range windows are ignored, while the 720-hour boundary is accepted.
	for _, hours := range []string{"bad", "0", "721", "720"} {
		q.Set("hours", hours)
		rows := fetch(q)
		require.Len(t, rows.Transactions, 2, "hours=%s", hours)
	}
}
