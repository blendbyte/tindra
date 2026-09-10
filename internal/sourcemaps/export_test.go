package sourcemaps

import (
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
