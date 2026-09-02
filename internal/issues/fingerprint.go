package issues

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type eventPayload struct {
	Fingerprint []string `json:"fingerprint"`
	Exception   struct {
		Values []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"values"`
	} `json:"exception"`
	Message     string `json:"message"`
	Transaction string `json:"transaction"`
}

// Compute returns a stable fingerprint for a Sentry event payload.
// Priority: explicit SDK fingerprint (with {{ default }} expansion) → last exception → message → raw hash.
func Compute(raw json.RawMessage) string {
	var p eventPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return sha256hex(raw)
	}
	if len(p.Fingerprint) > 0 {
		def := defaultKey(p)
		parts := make([]string, len(p.Fingerprint))
		for i, part := range p.Fingerprint {
			if part == "{{ default }}" {
				parts[i] = def
			} else {
				parts[i] = part
			}
		}
		return sha256hex([]byte(strings.Join(parts, "|")))
	}
	if key := defaultKey(p); key != "" {
		return sha256hex([]byte(key))
	}
	return sha256hex(raw)
}

// defaultKey returns the ungrouped key string built from the last (innermost) exception or message.
// Returns empty string if neither is present.
func defaultKey(p eventPayload) string {
	if len(p.Exception.Values) > 0 {
		last := p.Exception.Values[len(p.Exception.Values)-1]
		key := last.Type + "|" + last.Value
		if p.Transaction != "" {
			key += "|" + p.Transaction
		}
		return key
	}
	if p.Message != "" {
		if p.Transaction != "" {
			return p.Message + "|" + p.Transaction
		}
		return p.Message
	}
	return ""
}

// Title extracts a short human-readable title from a Sentry event payload.
// Uses the last (innermost) exception for consistency with Compute.
func Title(raw json.RawMessage) string {
	var p struct {
		Exception struct {
			Values []struct {
				Type  string `json:"type"`
				Value string `json:"value"`
			} `json:"values"`
		} `json:"exception"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "Unknown error"
	}
	if len(p.Exception.Values) > 0 {
		last := p.Exception.Values[len(p.Exception.Values)-1]
		if last.Value != "" {
			return truncRunes(fmt.Sprintf("%s: %s", last.Type, last.Value), 200)
		}
		return last.Type
	}
	if p.Message != "" {
		return truncRunes(p.Message, 200)
	}
	return "Unknown error"
}

// truncRunes truncates s to at most max runes, preserving UTF-8 boundaries.
// Truncated values end in an ellipsis so the cut is visible to readers.
func truncRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "\u2026"
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
