package issues

import "encoding/json"

var NormalizeSQL = normalizeSQL

// Raw-payload entry points for tests. The grouper decodes each payload once
// and calls computeFrom, titleFrom and implicitTagsFrom directly.

// Compute returns the fingerprint for a Sentry event payload, plus the legacy
// v1 fingerprint when it differs (empty otherwise). The grouper prefers an
// existing issue under the legacy fingerprint.
// Priority: explicit SDK fingerprint (with {{ default }} expansion), then the
// innermost exception, then the message, then the transaction.
func Compute(raw json.RawMessage) (fingerprint, legacy string) {
	p, ok := decodeEvent(raw)
	return computeFrom(p, ok)
}

// Title extracts a short human-readable title from a Sentry event payload.
// Uses the last (innermost) exception for consistency with Compute.
func Title(raw json.RawMessage) string {
	p, ok := decodeEvent(raw)
	return titleFrom(p, ok)
}

// extractImplicitTags derives the standard set of tags that Sentry synthesises
// from well-known event fields: level, environment, transaction, server_name,
// request URL, contexts (browser/os/runtime/device/client_os), and the
// exception mechanism (handled, mechanism type).
//
// These are merged with any explicit SDK-supplied tags, with explicit tags
// taking priority on key conflicts.
func extractImplicitTags(payload json.RawMessage) [][2]string {
	p, ok := decodeEvent(payload)
	if !ok {
		return nil
	}
	return implicitTagsFrom(p)
}
