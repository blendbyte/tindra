package api

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/blendbyte/tindra/internal/storage"
)

// Preserve older SDK and deep-link parameter names while accepting canonical
// environment and explicit UTC bounds on investigation requests.
func investigationContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Has("environment") {
			q.Set("env", q.Get("environment"))
		} else if q.Has("env") {
			q.Set("environment", q.Get("env"))
		}
		r.URL.RawQuery = q.Encode()
		if q.Get("all_time") == "1" && (r.URL.Path == "/api/issues" || r.URL.Path == "/api/issues/export") {
			to, err := time.Parse(time.RFC3339Nano, q.Get("as_of"))
			if err != nil || !to.After(time.Time{}) || q.Has("from") || q.Has("to") {
				http.Error(w, "Provide a valid as_of timestamp for all-time issues", http.StatusBadRequest)
				return
			}
			r = r.WithContext(storage.WithTimeRange(r.Context(), storage.TimeRange{From: time.Time{}, To: to.UTC(), AllTime: true}))
		}
		if q.Has("from") || q.Has("to") {
			from, e1 := time.Parse(time.RFC3339Nano, q.Get("from"))
			to, e2 := time.Parse(time.RFC3339Nano, q.Get("to"))
			max := 90 * 24 * time.Hour
			if e1 != nil || e2 != nil || !from.Before(to) || to.Sub(from) > max {
				http.Error(w, "Provide valid from/to timestamps within the supported range", http.StatusBadRequest)
				return
			}
			r = r.WithContext(storage.WithTimeRange(r.Context(), storage.TimeRange{From: from.UTC(), To: to.UTC()}))
		}
		next.ServeHTTP(w, r)
	})
}

func (ro *router) handleEnvironments(w http.ResponseWriter, r *http.Request) {
	serveEnvironments(w, r, ro.pool)
}

type environmentQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// environmentsQuery walks each table's (project_id, environment) partial index
// one distinct value at a time per project, so a refresh costs one index
// descent per environment instead of reading every indexed row. Projects come
// from the projects table so the walk never depends on skip scan, and unknown
// ids simply match nothing. The environment predicates repeat the partial index
// predicate so the planner can use it.
var environmentsQuery = `WITH RECURSIVE scope AS MATERIALIZED (
 SELECT id FROM projects WHERE cardinality($1::uuid[]) = 0 OR id = ANY($1::uuid[])
)` + environmentWalk("events") + environmentWalk("transactions") + environmentWalk("logs") + `
SELECT environment FROM events_environments WHERE environment IS NOT NULL
UNION SELECT environment FROM transactions_environments WHERE environment IS NOT NULL
UNION SELECT environment FROM logs_environments WHERE environment IS NOT NULL
ORDER BY environment`

// environmentWalk yields every environment of table per scoped project, ending
// each project's walk with a NULL row once no larger value remains.
func environmentWalk(table string) string {
	next := `(SELECT environment FROM ` + table + ` WHERE project_id = %s AND environment IS NOT NULL AND environment <> ''%s ORDER BY environment LIMIT 1)`
	return `, ` + table + `_environments(project_id, environment) AS (
 SELECT s.id, ` + fmt.Sprintf(next, "s.id", "") + ` FROM scope s
 UNION ALL SELECT w.project_id, ` + fmt.Sprintf(next, "w.project_id", " AND environment > w.environment") + `
 FROM ` + table + `_environments w WHERE w.environment IS NOT NULL
)`
}

func serveEnvironments(w http.ResponseWriter, r *http.Request, db environmentQuerier) {
	ids := bearerProjectIDs(r, r.URL.Query()["project_id"])
	if ids == nil {
		ids = []string{}
	}
	rows, err := db.Query(r.Context(), environmentsQuery, ids)
	if err != nil {
		http.Error(w, "Could not load environments", 500)
		return
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var env string
		if err := rows.Scan(&env); err != nil {
			http.Error(w, "Could not load environments", 500)
			return
		}
		result = append(result, env)
	}
	if rows.Err() != nil {
		http.Error(w, "Could not load environments", 500)
		return
	}
	writeJSON(w, result)
}
