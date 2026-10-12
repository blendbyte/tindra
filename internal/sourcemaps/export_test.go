package sourcemaps

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewTestStore permits local HTTP fixtures in cache and source-resolution tests.
// This constructor is available only in test builds.
func NewTestStore(dataDir string, pool *pgxpool.Pool) *Store {
	store := NewStore(dataDir, pool)
	store.httpClient = &http.Client{Timeout: fetchTimeout}
	return store
}

// Parse decodes raw source map JSON and builds the segment index.
func Parse(data []byte) (*SourceMap, error) {
	return ParseContext(context.Background(), data)
}
