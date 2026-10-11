package ingest

import (
	"encoding/json"
	"unicode/utf8"
)

// MaxIndexedFieldBytes caps short text fields that land in btree indexes
// (release, environment, trace and span IDs, levels). Postgres rejects index
// rows over roughly 2.7 KB with error 54000, which fails the whole batch.
const MaxIndexedFieldBytes = 512

// maxIDBytes bounds client-supplied identifiers. Real ones are 32 hex digits or
// a UUID, so anything longer is malformed.
const maxIDBytes = 128

// TruncateUTF8 shortens s to at most n bytes without splitting a multi-byte
// character. A cut in the middle of a character produces invalid UTF-8, which
// Postgres rejects for text columns.
func TruncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// NormalizeEventID returns id when it is a plausible event ID and nil
// otherwise. SDKs send 32 hex digits, but custom clients may not, so any short
// printable ASCII is kept. The ID feeds a unique index used for deduplication,
// so an oversized value would fail the whole batch; dropping it only loses
// deduplication for that one event.
func NormalizeEventID(id string) *string {
	if id == "" || len(id) > maxIDBytes {
		return nil
	}
	for i := 0; i < len(id); i++ {
		if id[i] < 0x21 || id[i] > 0x7e {
			return nil
		}
	}
	return &id
}

// CapEventFields truncates the payload fields that Postgres copies into indexed
// generated columns on events (release, environment). The payload is only
// rewritten when a field is over the limit.
func CapEventFields(payload json.RawMessage) json.RawMessage {
	var p struct {
		Release     *string `json:"release"`
		Environment *string `json:"environment"`
	}
	if json.Unmarshal(payload, &p) != nil {
		return payload
	}
	over := func(s *string) bool { return s != nil && len(*s) > MaxIndexedFieldBytes }
	if !over(p.Release) && !over(p.Environment) {
		return payload
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(payload, &fields) != nil {
		return payload
	}
	for key, value := range map[string]*string{"release": p.Release, "environment": p.Environment} {
		if over(value) {
			encoded, err := json.Marshal(TruncateUTF8(*value, MaxIndexedFieldBytes))
			if err != nil {
				return payload
			}
			fields[key] = encoded
		}
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return payload
	}
	return out
}
