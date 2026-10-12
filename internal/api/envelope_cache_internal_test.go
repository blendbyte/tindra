package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/blendbyte/tindra/internal/storage"
)

func TestProjectKeyCache_cachesHitsAndMissesUntilInvalidated(t *testing.T) {
	pool := oauthDB(t)
	ctx := context.Background()
	p, err := storage.CreateProject(ctx, pool, "key-cache-"+uuid.NewString()[:8], "Key Cache")
	require.NoError(t, err)
	c := newProjectKeyCache(100)

	got, err := c.lookup(ctx, pool, p.PublicKey)
	require.NoError(t, err)
	require.Equal(t, p.ID, got.ID)

	// Deleted behind the cache's back: still served from the cache...
	_, err = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, p.ID)
	require.NoError(t, err)
	got, err = c.lookup(ctx, pool, p.PublicKey)
	require.NoError(t, err)
	require.NotNil(t, got, "within the TTL the cached project is used")
	// ...until a change made through this instance invalidates it.
	c.invalidate()
	got, err = c.lookup(ctx, pool, p.PublicKey)
	require.NoError(t, err)
	require.Nil(t, got)

	// Unknown keys are cached too, and the map never exceeds its bound.
	small := newProjectKeyCache(2)
	for range 5 {
		_, err := small.lookup(ctx, pool, uuid.NewString())
		require.NoError(t, err)
	}
	require.LessOrEqual(t, len(small.entries), 2)
}

func TestMonthlyCountCache_reusesRecentCount(t *testing.T) {
	pool := oauthDB(t)
	ctx := context.Background()
	c := &monthlyCountCache{}
	first, err := c.get(ctx, pool)
	require.NoError(t, err)
	c.count = first + 1000 // a cached value is returned as is within the TTL
	second, err := c.get(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, first+1000, second)
	c.at = time.Now().Add(-2 * monthlyCountTTL)
	third, err := c.get(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, first, third, "an expired value is refreshed from the database")
}

func TestObservationDedup_skipsRecentRepeats(t *testing.T) {
	d := newObservationDedup()
	now := time.Now()
	obs := func(reason string, at time.Time) []storage.SetupObservation {
		return []storage.SetupObservation{{Kind: "events", Outcome: "queued", Reason: reason, ObservedAt: at}}
	}
	require.Len(t, d.filter("p1", obs("queued", now)), 1, "first observation is written")
	require.Empty(t, d.filter("p1", obs("queued", now.Add(time.Second))), "a repeat within the window is skipped")
	require.Len(t, d.filter("p1", obs("buffer_full", now.Add(2*time.Second))), 1, "a new reason is written at once")
	require.Len(t, d.filter("p1", obs("buffer_full", now.Add(8*time.Second))), 1, "a repeat after the window is written")
	require.Len(t, d.filter("p2", obs("queued", now)), 1, "other projects are tracked separately")
}

// Clients presenting unknown keys are cut off per IP after the budget.
func TestHandleEnvelope_throttlesInvalidKeysPerIP(t *testing.T) {
	ro := &router{pool: oauthDB(t), projectKeys: newProjectKeyCache(100), unknownKeyRL: newRateLimiter(3, time.Minute)}
	send := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", strings.NewReader("{}\n"))
		req.Header.Set("X-Sentry-Auth", "Sentry sentry_version=7, sentry_key="+uuid.NewString())
		req.RemoteAddr = "198.51.100.7:1234"
		rec := httptest.NewRecorder()
		ro.handleEnvelope(rec, req)
		return rec.Code
	}
	for range 3 {
		require.Equal(t, http.StatusUnauthorized, send())
	}
	require.Equal(t, http.StatusTooManyRequests, send())
}
