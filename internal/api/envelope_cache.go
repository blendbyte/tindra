package api

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/blendbyte/tindra/internal/storage"
)

// The envelope endpoint is the hottest path in Tindra. These small in-process
// caches keep it from doing several database round trips per request. All of
// them are nil-safe: a nil cache simply goes to the database every time.

// projectKeyCacheTTL bounds how long a project's settings (scrubbing,
// passthrough, profiling) may lag behind a change made on another instance.
// Changes made through this instance clear the cache immediately.
const projectKeyCacheTTL = 10 * time.Second

// projectKeyCache maps public keys to projects, including unknown keys (nil),
// so repeated requests with the same key do not each query the database.
type projectKeyCache struct {
	mu      sync.Mutex
	entries map[string]projectKeyEntry
	max     int
}

type projectKeyEntry struct {
	project *storage.Project // nil for an unknown key
	expires time.Time
}

func newProjectKeyCache(max int) *projectKeyCache {
	return &projectKeyCache{entries: map[string]projectKeyEntry{}, max: max}
}

// lookup returns the project for publicKey, from the cache when fresh.
func (c *projectKeyCache) lookup(ctx context.Context, pool *pgxpool.Pool, publicKey string) (*storage.Project, error) {
	if c != nil {
		c.mu.Lock()
		e, ok := c.entries[publicKey]
		c.mu.Unlock()
		if ok && time.Now().Before(e.expires) {
			return e.project, nil
		}
	}
	project, err := storage.GetByPublicKey(ctx, pool, publicKey)
	if err != nil || c == nil {
		return project, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		// Unknown keys are attacker-chosen, so never let them grow the map
		// without bound: drop everything rather than track eviction order.
		clear(c.entries)
	}
	c.entries[publicKey] = projectKeyEntry{project: project, expires: time.Now().Add(projectKeyCacheTTL)}
	return project, nil
}

// invalidate forgets every cached project, after a project was changed.
func (c *projectKeyCache) invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

// monthlyCountTTL is how stale the instance-wide monthly event count used for
// EVENT_LIMIT may be. Enforcement may overshoot by about this long.
const monthlyCountTTL = 10 * time.Second

type monthlyCountCache struct {
	mu    sync.Mutex
	count int64
	at    time.Time
}

func (c *monthlyCountCache) get(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	if c == nil {
		return storage.CountMonthlyEvents(ctx, pool)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < monthlyCountTTL {
		return c.count, nil
	}
	count, err := storage.CountMonthlyEvents(ctx, pool)
	if err != nil {
		return 0, err
	}
	c.count, c.at = count, time.Now()
	return count, nil
}

// observationDedupWindow matches RecordSetupObservations, which ignores a
// repeat of the same reason within five seconds anyway.
const observationDedupWindow = 5 * time.Second

// observationDedup drops setup observations this instance wrote recently with
// the same reason, so steady traffic does not write one row per envelope.
type observationDedup struct {
	mu   sync.Mutex
	last map[string]observationMark
}

type observationMark struct {
	reason string
	at     time.Time
}

func newObservationDedup() *observationDedup {
	return &observationDedup{last: map[string]observationMark{}}
}

// filter returns the observations worth writing and records them as written.
func (d *observationDedup) filter(projectID string, observations []storage.SetupObservation) []storage.SetupObservation {
	if d == nil {
		return observations
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	out := observations[:0:0]
	for _, o := range observations {
		key := projectID + "|" + o.Kind + "|" + o.Outcome
		if m, ok := d.last[key]; ok && m.reason == o.Reason && o.ObservedAt.Sub(m.at) < observationDedupWindow {
			continue
		}
		d.last[key] = observationMark{reason: o.Reason, at: o.ObservedAt}
		out = append(out, o)
	}
	return out
}
