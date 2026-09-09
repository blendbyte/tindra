package digest

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// reportVolume shares one aggregation between the daily charts and project
// totals. Only errors use receipt-time summaries; transactions use SDK start time.
func reportVolume(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, from, to time.Time) ([]DayStat, []DayStat, []ProjectStat, error) {
	rows, err := pool.Query(ctx, `
 WITH bounds AS (
  SELECT date_trunc('hour',$2::timestamptz,'UTC') +
    CASE WHEN $2::timestamptz=date_trunc('hour',$2::timestamptz,'UTC') THEN interval '0' ELSE interval '1 hour' END AS full_start,
   date_trunc('hour',$3::timestamptz,'UTC') AS full_end
 ), error_parts AS (
  SELECT project_id,bucket AS instant,n FROM telemetry_usage,bounds
  WHERE kind='events' AND project_id=ANY($1::uuid[]) AND bucket>=full_start AND bucket<full_end
  UNION ALL
  SELECT project_id,received_at,1 FROM events,bounds
  WHERE project_id=ANY($1::uuid[]) AND received_at >= $2 AND received_at < LEAST($3,full_start)
  UNION ALL
  SELECT project_id,received_at,1 FROM events,bounds
  WHERE project_id=ANY($1::uuid[]) AND received_at >= GREATEST($2,full_start,full_end) AND received_at < $3
 ), counts AS (
  SELECT project_id,date_trunc('day',instant AT TIME ZONE 'UTC') AS day,'errors' AS kind,SUM(n)::bigint AS n
  FROM error_parts GROUP BY 1,2
  UNION ALL
  SELECT project_id,date_trunc('day',start_timestamp AT TIME ZONE 'UTC'),'transactions',COUNT(*)
  FROM transactions WHERE project_id=ANY($1::uuid[]) AND start_timestamp >= $2 AND start_timestamp < $3 GROUP BY 1,2
 )
 SELECT p.id,p.name,c.day,c.kind,COALESCE(c.n,0) FROM projects p
 LEFT JOIN counts c ON c.project_id=p.id WHERE p.id=ANY($1::uuid[])
 `, projectIDs, from, to)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("report volume: %w", err)
	}
	defer rows.Close()
	errorsByDay, txByDay := map[time.Time]int64{}, map[time.Time]int64{}
	byProject := map[string]*ProjectStat{}
	for rows.Next() {
		var id, name string
		var day *time.Time
		var kind *string
		var n int64
		if err := rows.Scan(&id, &name, &day, &kind, &n); err != nil {
			return nil, nil, nil, err
		}
		p := byProject[id]
		if p == nil {
			p = &ProjectStat{ProjectID: id, ProjectName: name}
			byProject[id] = p
		}
		if day == nil || kind == nil {
			continue
		}
		if *kind == "errors" {
			p.Errors += n
			errorsByDay[*day] += n
		} else {
			p.Transactions += n
			txByDay[*day] += n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}
	var projects []ProjectStat
	for _, p := range byProject {
		projects = append(projects, *p)
	}
	sort.Slice(projects, func(i, j int) bool {
		a, b := projects[i], projects[j]
		if a.Errors != b.Errors {
			return a.Errors > b.Errors
		}
		if a.Transactions != b.Transactions {
			return a.Transactions > b.Transactions
		}
		return a.ProjectID < b.ProjectID
	})
	days := func(counts map[time.Time]int64) []DayStat {
		var out []DayStat
		for day, n := range counts {
			out = append(out, DayStat{day, n})
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Date.Before(out[j].Date) })
		return out
	}
	return days(errorsByDay), days(txByDay), projects, nil
}
