package ingest

import (
	"encoding/json"
	"time"
)

// ParseSentryTimestamp parses a timestamp from a Sentry payload field.
// Sentry SDKs send either a float Unix epoch (1609459200.123) or an RFC3339
// string ("2021-01-01T00:00:00.000Z") depending on the language SDK.
func ParseSentryTimestamp(raw json.RawMessage) time.Time {
	if len(raw) == 0 {
		return time.Now().UTC()
	}
	// JSON number → Unix epoch seconds (float), used by Python, Ruby SDKs
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil && f > 0 {
		sec := int64(f)
		nsec := int64((f - float64(sec)) * 1e9)
		return time.Unix(sec, nsec).UTC()
	}
	// JSON string → RFC3339, used by JS, Go, PHP SDKs
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t.UTC()
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

// MaxFutureSkew is how far ahead of the server clock a client timestamp may be.
// Beyond that the client clock is wrong, and keeping the value would pin
// issues as "last seen" in the future and sort them first indefinitely.
const MaxFutureSkew = time.Minute

// ClampFuture returns now when t is more than MaxFutureSkew ahead of it.
func ClampFuture(t, now time.Time) time.Time {
	if t.Sub(now) > MaxFutureSkew {
		return now
	}
	return t
}

// FutureShift returns how far to move a group of related timestamps (a
// transaction and its spans) so that end is not in the future, or zero when
// end is within MaxFutureSkew. Shifting the whole group keeps durations and
// span offsets intact, which clamping each timestamp separately would not.
func FutureShift(end, now time.Time) time.Duration {
	if end.Sub(now) > MaxFutureSkew {
		return now.Sub(end)
	}
	return 0
}
