package storage

import (
	"context"
	"time"
)

// TimeRange is a UTC half-open investigation interval: From <= timestamp < To.
type TimeRange struct {
	From, To time.Time
	AllTime  bool
}
type timeRangeKey struct{}

func WithTimeRange(ctx context.Context, bounds TimeRange) context.Context {
	return context.WithValue(ctx, timeRangeKey{}, bounds)
}
func InvestigationRange(ctx context.Context) (TimeRange, bool) {
	bounds, ok := ctx.Value(timeRangeKey{}).(TimeRange)
	return bounds, ok
}
func ResolveTimeRange(ctx context.Context, hours, offset int) TimeRange {
	if bounds, ok := InvestigationRange(ctx); ok {
		return bounds
	}
	if hours <= 0 || hours > 720 {
		hours = 24
	}
	to := time.Now().UTC().Add(-time.Duration(offset) * time.Hour)
	return TimeRange{From: to.Add(-time.Duration(hours) * time.Hour), To: to}
}
