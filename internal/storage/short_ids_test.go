package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestParseShortID(t *testing.T) {
	for _, tc := range []struct {
		in     string
		prefix string
		number int64
		ok     bool
	}{
		{"BACKEND-42", "BACKEND", 42, true},
		{" backend-42 ", "BACKEND", 42, true},
		{"MY-APP-7", "MY-APP", 7, true},
		{"APP-2-5", "APP-2", 5, true},
		{"BACKEND2-1", "BACKEND2", 1, true},
		{"BACKEND-0", "", 0, false},
		{"BACKEND-042", "", 0, false},
		{"BACKEND", "", 0, false},
		{"-42", "", 0, false},
		{"BACK END-42", "", 0, false},
		{"BACKEND-1234567890123456789", "", 0, false},
		{"", "", 0, false},
	} {
		prefix, number, ok := storage.ParseShortID(tc.in)
		require.Equal(t, tc.ok, ok, tc.in)
		require.Equal(t, tc.prefix, prefix, tc.in)
		require.Equal(t, tc.number, number, tc.in)
	}
}

func shortIDProject(t *testing.T, slug string) *storage.Project {
	t.Helper()
	p, err := storage.CreateProject(context.Background(), testPool, slug, slug)
	require.NoError(t, err)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), "DELETE FROM projects WHERE id = $1", p.ID)
	})
	return p
}

func shortIDIssue(t *testing.T, projectID, fingerprint string) *storage.Issue {
	t.Helper()
	iss, created, _, err := storage.UpsertIssue(context.Background(), testPool, projectID, fingerprint, "Error: "+fingerprint, "error", "error", "", "", time.Now())
	require.NoError(t, err)
	require.True(t, created)
	return iss
}

func TestShortIDs_numberedPerProject(t *testing.T) {
	ctx := context.Background()
	a := shortIDProject(t, "sid-alpha")
	b := shortIDProject(t, "sid-beta")

	require.Equal(t, "SID-ALPHA-1", shortIDIssue(t, a.ID, "a1").ShortID)
	require.Equal(t, "SID-ALPHA-2", shortIDIssue(t, a.ID, "a2").ShortID)
	require.Equal(t, "SID-BETA-1", shortIDIssue(t, b.ID, "b1").ShortID)

	// An event for an existing fingerprint keeps the issue's number.
	again, created, _, err := storage.UpsertIssue(ctx, testPool, a.ID, "a1", "Error: a1", "error", "error", "", "", time.Now())
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, "SID-ALPHA-1", again.ShortID)

	// A rolled-back issue gives its number back.
	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `INSERT INTO issues (project_id, fingerprint, title, level, kind, first_seen, last_seen) VALUES ($1, 'rolled', 't', 'error', 'error', now(), now())`, a.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(ctx))
	require.Equal(t, "SID-ALPHA-3", shortIDIssue(t, a.ID, "a3").ShortID)
}

func TestShortIDs_prefixFrozenAtCreation(t *testing.T) {
	ctx := context.Background()
	p := shortIDProject(t, "sid-frozen")
	shortIDIssue(t, p.ID, "f1")

	_, err := storage.UpdateProject(ctx, testPool, p.ID, "Renamed", "sid-renamed", nil, nil)
	require.NoError(t, err)
	require.Equal(t, "SID-FROZEN-2", shortIDIssue(t, p.ID, "f2").ShortID)

	// A new project taking the old slug gets a numbered prefix, so the
	// renamed project's short IDs stay unambiguous.
	q := shortIDProject(t, "sid-frozen")
	require.Equal(t, "SID-FROZEN2-1", shortIDIssue(t, q.ID, "q1").ShortID)

	id, err := storage.ResolveShortID(ctx, testPool, "SID-FROZEN-1")
	require.NoError(t, err)
	iss, err := storage.GetIssue(ctx, testPool, id)
	require.NoError(t, err)
	require.Equal(t, p.ID, iss.ProjectID)
}

// Slugs from the CLI are not validated; their prefix still parses.
func TestShortIDs_prefixCleanedFromSlug(t *testing.T) {
	p := shortIDProject(t, "sid_odd.slug--v2")
	iss := shortIDIssue(t, p.ID, "o1")
	require.Equal(t, "SID-ODD-SLUG-V2-1", iss.ShortID)
	id, err := storage.ResolveShortID(context.Background(), testPool, iss.ShortID)
	require.NoError(t, err)
	require.Equal(t, iss.ID, id)
}

// A deleted project's prefix stays claimed, so its old short IDs never lead
// to a new project's issues.
func TestShortIDs_deletedPrefixNotReused(t *testing.T) {
	ctx := context.Background()
	old, err := storage.CreateProject(ctx, testPool, "sid-gone", "Gone")
	require.NoError(t, err)
	gone := shortIDIssue(t, old.ID, "g1")
	_, err = testPool.Exec(ctx, "DELETE FROM projects WHERE id = $1", old.ID)
	require.NoError(t, err)

	p := shortIDProject(t, "sid-gone")
	require.Equal(t, "SID-GONE2-1", shortIDIssue(t, p.ID, "g2").ShortID)
	id, err := storage.ResolveShortID(ctx, testPool, gone.ShortID)
	require.NoError(t, err)
	require.Empty(t, id)
}

func TestResolveShortID(t *testing.T) {
	ctx := context.Background()
	p := shortIDProject(t, "sid-resolve")
	iss := shortIDIssue(t, p.ID, "r1")

	for _, ref := range []string{"SID-RESOLVE-1", "sid-resolve-1", " SID-RESOLVE-1 "} {
		id, err := storage.ResolveShortID(ctx, testPool, ref)
		require.NoError(t, err)
		require.Equal(t, iss.ID, id, ref)
	}
	for _, ref := range []string{"SID-RESOLVE-2", "SID-NOPE-1", "not a short id", iss.ID} {
		id, err := storage.ResolveShortID(ctx, testPool, ref)
		require.NoError(t, err)
		require.Empty(t, id, ref)
	}
}

// A merged issue is deleted, but its short ID leads to the issue it went
// into, also after that issue is merged again.
func TestResolveShortID_followsMerges(t *testing.T) {
	ctx := context.Background()
	p := shortIDProject(t, "sid-merge")
	first := shortIDIssue(t, p.ID, "m1")
	second := shortIDIssue(t, p.ID, "m2")
	third := shortIDIssue(t, p.ID, "m3")

	_, err := storage.MergeIssues(ctx, testPool, second.ID, []string{first.ID})
	require.NoError(t, err)
	_, err = storage.MergeIssues(ctx, testPool, third.ID, []string{second.ID})
	require.NoError(t, err)

	for _, ref := range []string{first.ShortID, second.ShortID, third.ShortID} {
		id, err := storage.ResolveShortID(ctx, testPool, ref)
		require.NoError(t, err)
		require.Equal(t, third.ID, id, ref)
	}

	// Deleting the issue drops the aliases with it.
	_, err = testPool.Exec(ctx, `DELETE FROM issues WHERE id = $1`, third.ID)
	require.NoError(t, err)
	id, err := storage.ResolveShortID(ctx, testPool, first.ShortID)
	require.NoError(t, err)
	require.Empty(t, id)
}

// Every list the UI and alerts read carries the short ID.
func TestShortIDs_inIssueLists(t *testing.T) {
	ctx := context.Background()
	p := shortIDProject(t, "sid-lists")
	iss := shortIDIssue(t, p.ID, "l1")
	_, err := testPool.Exec(ctx, `
		INSERT INTO events (project_id, timestamp, received_at, payload, fingerprint, issue_id)
		VALUES ($1, now(), now(), '{}'::jsonb, 'l1', $2)`, p.ID, iss.ID)
	require.NoError(t, err)

	got, err := storage.GetIssue(ctx, testPool, iss.ID)
	require.NoError(t, err)
	require.Equal(t, "SID-LISTS-1", got.ShortID)

	list, err := storage.ListIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{p.ID}})
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "SID-LISTS-1", list[0].ShortID)

	all, err := storage.ListAllIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{p.ID}})
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Equal(t, "SID-LISTS-1", all[0].ShortID)

	windowed := storage.WithTimeRange(ctx, storage.TimeRange{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})
	win, err := storage.ListAllIssues(windowed, testPool, storage.IssueFilter{ProjectIDs: []string{p.ID}})
	require.NoError(t, err)
	require.Len(t, win, 1)
	require.Equal(t, "SID-LISTS-1", win[0].ShortID)

	dash, err := storage.ListDashboardIssues(ctx, testPool, []string{p.ID})
	require.NoError(t, err)
	require.Len(t, dash, 1)
	require.Equal(t, "SID-LISTS-1", dash[0].ShortID)

	attention, err := storage.ListAttentionIssues(ctx, testPool, storage.IssueFilter{ProjectIDs: []string{p.ID}}, time.Now().Add(-time.Hour), time.Now().Add(time.Hour), nil, 5)
	require.NoError(t, err)
	require.Len(t, attention.New, 1)
	require.Equal(t, "SID-LISTS-1", attention.New[0].ShortID)
}
