package api

import (
	"context"
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

func serveEnvironments(w http.ResponseWriter, r *http.Request, db environmentQuerier) {
	ids := bearerProjectIDs(r, r.URL.Query()["project_id"])
	if ids == nil {
		ids = []string{}
	}
	rows, err := db.Query(r.Context(), `SELECT DISTINCT environment FROM (
 SELECT environment FROM events WHERE (cardinality($1::uuid[]) = 0 OR project_id = ANY($1::uuid[]))
 UNION SELECT environment FROM transactions WHERE (cardinality($1::uuid[]) = 0 OR project_id = ANY($1::uuid[]))
 UNION SELECT environment FROM logs WHERE (cardinality($1::uuid[]) = 0 OR project_id = ANY($1::uuid[]))
 ) environments WHERE environment IS NOT NULL AND environment <> '' ORDER BY environment`, ids)
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
