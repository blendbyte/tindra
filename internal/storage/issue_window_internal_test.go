package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

type windowRows struct {
	pgx.Rows
	scanErr, iterationErr error
	consumed, closed      bool
}

func (r *windowRows) Next() bool {
	if r.consumed {
		return false
	}
	r.consumed = true
	return true
}
func (r *windowRows) Scan(...any) error { return r.scanErr }
func (r *windowRows) Err() error        { return r.iterationErr }
func (r *windowRows) Close()            { r.closed = true }

type windowDB struct {
	rows pgx.Rows
	err  error
	args []any
}

func (d *windowDB) Query(_ context.Context, _ string, args ...any) (pgx.Rows, error) {
	d.args = args
	return d.rows, d.err
}
func (d *windowDB) QueryRow(context.Context, string, ...any) pgx.Row { panic("unexpected QueryRow") }

func TestWindowIssuesPropagateDatabaseFailures(t *testing.T) {
	failure := errors.New("database unavailable")
	bounds := TimeRange{From: time.Now().Add(-time.Hour), To: time.Now()}
	db := &windowDB{err: failure}
	_, err := listWindowIssues(context.Background(), db, IssueFilter{}, bounds)
	require.ErrorIs(t, err, failure)
	for _, scanning := range []bool{true, false} {
		rows := &windowRows{}
		if scanning {
			rows.scanErr = failure
		} else {
			rows.iterationErr = failure
		}
		db = &windowDB{rows: rows}
		_, err = listWindowIssues(context.Background(), db, IssueFilter{Limit: 10001}, bounds)
		require.ErrorIs(t, err, failure)
		require.True(t, rows.closed)
		require.Equal(t, 10000, db.args[len(db.args)-1])
	}
}

func TestWindowIssueIdentityPredicate(t *testing.T) {
	bounds := TimeRange{From: time.Now().Add(-time.Hour), To: time.Now()}
	sql, args := windowIssueCTE(IssueFilter{UserIdentity: "customer"}, bounds)
	require.Contains(t, sql, "user_identity = $3")
	require.Equal(t, "customer", args[2])
}
