package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// VitalStat holds aggregated stats for a single Web Vital over a time window.
type VitalStat struct {
	P75      float64 `json:"p75"`       // p75 value in the vital's native unit (ms or unitless)
	PassRate float64 `json:"pass_rate"` // fraction of sessions meeting the "good" threshold (0–1)
	Count    int64   `json:"count"`     // number of sessions with this vital recorded
}

// WebVitalsSummary is the full set of vitals for the summary card row.
type WebVitalsSummary struct {
	LCP  VitalStat `json:"lcp"`
	FCP  VitalStat `json:"fcp"`
	CLS  VitalStat `json:"cls"`
	INP  VitalStat `json:"inp"`
	TTFB VitalStat `json:"ttfb"`
}

// WebVitalsPage is one row in the per-page breakdown table.
type WebVitalsPage struct {
	Transaction string  `json:"transaction"`
	Sessions    int64   `json:"sessions"`
	LCPP75      float64 `json:"lcp_p75"`
	INPP75      float64 `json:"inp_p75"`
	CLSP75      float64 `json:"cls_p75"`
	PassRate    float64 `json:"pass_rate"` // fraction meeting all three CWV thresholds simultaneously
}

// Google Core Web Vitals "good" thresholds.
const (
	lcpGood  = 2500.0 // ms
	fcpGood  = 1800.0 // ms
	clsGood  = 0.1    // unitless
	inpGood  = 200.0  // ms
	ttfbGood = 800.0  // ms
)

// GetWebVitalsSummary returns aggregated p75 and pass-rate for each vital,
// restricted to browser pageload/navigation transactions in the given window.
func GetWebVitalsSummary(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, from, to time.Time, env string) (WebVitalsSummary, error) {
	var s WebVitalsSummary
	if projectIDs == nil {
		projectIDs = []string{}
	}

	args := []any{projectIDs, from, to, lcpGood, fcpGood, clsGood, inpGood, ttfbGood}
	envFilter := ""
	if env != "" {
		args = append(args, env)
		envFilter = fmt.Sprintf(" AND environment = $%d", len(args))
	}

	row := pool.QueryRow(ctx, `
		SELECT
			-- LCP
			COALESCE(PERCENTILE_CONT(0.75) WITHIN GROUP (ORDER BY (measurements->'lcp'->>'value')::float) FILTER (WHERE measurements ? 'lcp'), 0),
			COALESCE(AVG(CASE WHEN (measurements->'lcp'->>'value')::float <= $4 THEN 1.0 ELSE 0.0 END) FILTER (WHERE measurements ? 'lcp'), 0),
			COUNT(*) FILTER (WHERE measurements ? 'lcp'),
			-- FCP
			COALESCE(PERCENTILE_CONT(0.75) WITHIN GROUP (ORDER BY (measurements->'fcp'->>'value')::float) FILTER (WHERE measurements ? 'fcp'), 0),
			COALESCE(AVG(CASE WHEN (measurements->'fcp'->>'value')::float <= $5 THEN 1.0 ELSE 0.0 END) FILTER (WHERE measurements ? 'fcp'), 0),
			COUNT(*) FILTER (WHERE measurements ? 'fcp'),
			-- CLS
			COALESCE(PERCENTILE_CONT(0.75) WITHIN GROUP (ORDER BY (measurements->'cls'->>'value')::float) FILTER (WHERE measurements ? 'cls'), 0),
			COALESCE(AVG(CASE WHEN (measurements->'cls'->>'value')::float <= $6 THEN 1.0 ELSE 0.0 END) FILTER (WHERE measurements ? 'cls'), 0),
			COUNT(*) FILTER (WHERE measurements ? 'cls'),
			-- INP
			COALESCE(PERCENTILE_CONT(0.75) WITHIN GROUP (ORDER BY (measurements->'inp'->>'value')::float) FILTER (WHERE measurements ? 'inp'), 0),
			COALESCE(AVG(CASE WHEN (measurements->'inp'->>'value')::float <= $7 THEN 1.0 ELSE 0.0 END) FILTER (WHERE measurements ? 'inp'), 0),
			COUNT(*) FILTER (WHERE measurements ? 'inp'),
			-- TTFB
			COALESCE(PERCENTILE_CONT(0.75) WITHIN GROUP (ORDER BY (measurements->'ttfb'->>'value')::float) FILTER (WHERE measurements ? 'ttfb'), 0),
			COALESCE(AVG(CASE WHEN (measurements->'ttfb'->>'value')::float <= $8 THEN 1.0 ELSE 0.0 END) FILTER (WHERE measurements ? 'ttfb'), 0),
			COUNT(*) FILTER (WHERE measurements ? 'ttfb')
		FROM transactions
		WHERE (CARDINALITY($1::uuid[]) = 0 OR project_id = ANY($1::uuid[]))
		  AND op IN ('pageload', 'navigation')
		  AND start_timestamp >= $2 AND start_timestamp < $3
		  AND measurements IS NOT NULL
	`+envFilter, args...)

	err := row.Scan(
		&s.LCP.P75, &s.LCP.PassRate, &s.LCP.Count,
		&s.FCP.P75, &s.FCP.PassRate, &s.FCP.Count,
		&s.CLS.P75, &s.CLS.PassRate, &s.CLS.Count,
		&s.INP.P75, &s.INP.PassRate, &s.INP.Count,
		&s.TTFB.P75, &s.TTFB.PassRate, &s.TTFB.Count,
	)
	if err != nil {
		return s, fmt.Errorf("web vitals summary: %w", err)
	}
	return s, nil
}

// GetWebVitalsByPage returns per-route vitals sorted by impact (sessions × CWV fail rate).
func GetWebVitalsByPage(ctx context.Context, pool *pgxpool.Pool, projectIDs []string, from, to time.Time, env string) ([]WebVitalsPage, error) {
	if projectIDs == nil {
		projectIDs = []string{}
	}

	args := []any{projectIDs, from, to, lcpGood, inpGood, clsGood}
	envFilter := ""
	if env != "" {
		args = append(args, env)
		envFilter = fmt.Sprintf(" AND environment = $%d", len(args))
	}

	rows, err := pool.Query(ctx, `
		SELECT
			transaction,
			COUNT(*) AS sessions,
			COALESCE(PERCENTILE_CONT(0.75) WITHIN GROUP (ORDER BY (measurements->'lcp'->>'value')::float) FILTER (WHERE measurements ? 'lcp'), 0) AS lcp_p75,
			COALESCE(PERCENTILE_CONT(0.75) WITHIN GROUP (ORDER BY (measurements->'inp'->>'value')::float) FILTER (WHERE measurements ? 'inp'), 0) AS inp_p75,
			COALESCE(PERCENTILE_CONT(0.75) WITHIN GROUP (ORDER BY (measurements->'cls'->>'value')::float) FILTER (WHERE measurements ? 'cls'), 0) AS cls_p75,
			COALESCE(AVG(
				CASE WHEN
					(NOT measurements ? 'lcp' OR (measurements->'lcp'->>'value')::float <= $4)
					AND (NOT measurements ? 'inp' OR (measurements->'inp'->>'value')::float <= $5)
					AND (NOT measurements ? 'cls' OR (measurements->'cls'->>'value')::float <= $6)
				THEN 1.0 ELSE 0.0 END
			) FILTER (WHERE measurements IS NOT NULL), 0) AS pass_rate
		FROM transactions
		WHERE (CARDINALITY($1::uuid[]) = 0 OR project_id = ANY($1::uuid[]))
		  AND op IN ('pageload', 'navigation')
		  AND start_timestamp >= $2 AND start_timestamp < $3
		  AND measurements IS NOT NULL
	`+envFilter+`
		GROUP BY transaction
		ORDER BY COUNT(*) * (1 - COALESCE(AVG(
			CASE WHEN
				(NOT measurements ? 'lcp' OR (measurements->'lcp'->>'value')::float <= $4)
				AND (NOT measurements ? 'inp' OR (measurements->'inp'->>'value')::float <= $5)
				AND (NOT measurements ? 'cls' OR (measurements->'cls'->>'value')::float <= $6)
			THEN 1.0 ELSE 0.0 END
		) FILTER (WHERE measurements IS NOT NULL), 0)) DESC
		LIMIT 25
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("web vitals by page: %w", err)
	}
	defer rows.Close()

	var pages []WebVitalsPage
	for rows.Next() {
		var p WebVitalsPage
		if err := rows.Scan(&p.Transaction, &p.Sessions, &p.LCPP75, &p.INPP75, &p.CLSP75, &p.PassRate); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		pages = append(pages, p)
	}
	return pages, rows.Err()
}
