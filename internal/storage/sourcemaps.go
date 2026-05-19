package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Sourcemap struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	Release     string    `json:"release"`
	URL         string    `json:"url"`
	ContentHash string    `json:"content_hash"`
	SizeBytes   int       `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

const sourcemapCols = `id, project_id, release, url, content_hash, size_bytes, created_at`

func scanSourcemap(scan func(...any) error) (*Sourcemap, error) {
	var s Sourcemap
	err := scan(&s.ID, &s.ProjectID, &s.Release, &s.URL, &s.ContentHash, &s.SizeBytes, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// UpsertSourcemap inserts or replaces the sourcemap record for (project, release, url).
func UpsertSourcemap(ctx context.Context, pool *pgxpool.Pool, s *Sourcemap) (*Sourcemap, error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO sourcemaps (project_id, release, url, content_hash, size_bytes)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (project_id, release, url) DO UPDATE
			SET content_hash = EXCLUDED.content_hash,
			    size_bytes    = EXCLUDED.size_bytes
		RETURNING `+sourcemapCols,
		s.ProjectID, s.Release, s.URL, s.ContentHash, s.SizeBytes,
	)
	created, err := scanSourcemap(row.Scan)
	if err != nil {
		return nil, fmt.Errorf("upsert: %w", err)
	}
	return created, nil
}

func GetSourcemap(ctx context.Context, pool *pgxpool.Pool, projectID, release, url string) (*Sourcemap, error) {
	row := pool.QueryRow(ctx,
		`SELECT `+sourcemapCols+` FROM sourcemaps WHERE project_id = $1 AND release = $2 AND url = $3`,
		projectID, release, url,
	)
	s, err := scanSourcemap(row.Scan)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	return s, nil
}

func ListSourcemaps(ctx context.Context, pool *pgxpool.Pool, projectID, release string) ([]*Sourcemap, error) {
	query := `SELECT ` + sourcemapCols + ` FROM sourcemaps WHERE project_id = $1`
	args := []any{projectID}
	if release != "" {
		query += ` AND release = $2`
		args = append(args, release)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var out []*Sourcemap
	for rows.Next() {
		s, err := scanSourcemap(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSourcemap removes the DB record and returns the content_hash of the
// deleted row so the caller can decide whether to remove the file on disk.
// Returns ("", nil) when the record was not found.
func DeleteSourcemap(ctx context.Context, pool *pgxpool.Pool, id, projectID string) (contentHash string, err error) {
	err = pool.QueryRow(ctx,
		`DELETE FROM sourcemaps WHERE id = $1 AND project_id = $2 RETURNING content_hash`,
		id, projectID,
	).Scan(&contentHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("delete: %w", err)
	}
	return contentHash, nil
}

// CountSourcemapsByHash returns the number of remaining records in the project
// that reference the given content hash. Used to decide if the file can be removed.
func CountSourcemapsByHash(ctx context.Context, pool *pgxpool.Pool, projectID, contentHash string) (int, error) {
	var n int
	err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM sourcemaps WHERE project_id = $1 AND content_hash = $2`,
		projectID, contentHash,
	).Scan(&n)
	return n, err
}
