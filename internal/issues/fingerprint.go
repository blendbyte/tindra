package issues

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Grouping versions.
//
// v1 keys an exception by its type, value and transaction. Values such as
// "Order 123 not found" therefore create one issue per ID.
//
// v2 keys an exception by its type and the module/function of its in-app stack
// frames (no line numbers, so a deploy does not split issues), which is close
// to what Sentry does. Events without usable frames get the v1 key, so v1 and
// v2 only differ when frames are present.
//
// Existing issues were created with v1 keys. Compute returns both, and the
// grouper keeps routing events to an existing v1 issue so the switch to v2 does
// not duplicate issues that already exist.

// eventPayload is the one decode of an event that grouping needs. Scalars use
// tolerant types and nested objects stay raw until used, so a single oddly
// shaped field cannot make the whole payload undecodable (which would change
// its fingerprint).
type eventPayload struct {
	Fingerprint json.RawMessage `json:"fingerprint"`
	Platform    looseString     `json:"platform"`
	Exception   json.RawMessage `json:"exception"`
	Message     messageField    `json:"message"`
	LogEntry    messageField    `json:"logentry"`
	Transaction looseString     `json:"transaction"`

	// Metadata and implicit-tag sources.
	Level       looseString     `json:"level"`
	Environment looseString     `json:"environment"`
	Release     looseString     `json:"release"`
	ServerName  looseString     `json:"server_name"`
	Tags        json.RawMessage `json:"tags"`
	Request     json.RawMessage `json:"request"`
	Contexts    json.RawMessage `json:"contexts"`
}

type exceptionValue struct {
	Type       looseString `json:"type"`
	Value      looseString `json:"value"`
	Stacktrace *struct {
		Frames []stackFrame `json:"frames"`
	} `json:"stacktrace"`
	Mechanism json.RawMessage `json:"mechanism"`
}

// decodeEvent decodes raw once; ok is false only for malformed JSON.
func decodeEvent(raw json.RawMessage) (p eventPayload, ok bool) {
	return p, json.Unmarshal(raw, &p) == nil
}

// fingerprintParts returns the SDK-supplied fingerprint, tolerating non-string
// entries; nil when absent or not a list.
func (p eventPayload) fingerprintParts() []looseString {
	var parts []looseString
	if len(p.Fingerprint) > 0 && json.Unmarshal(p.Fingerprint, &parts) == nil {
		return parts
	}
	return nil
}

type stackFrame struct {
	Function looseString `json:"function"`
	Module   looseString `json:"module"`
	InApp    *bool       `json:"in_app"`
}

// looseString decodes any JSON scalar as text, so one oddly typed field (a
// numeric value, a null) cannot make the whole payload undecodable.
type looseString string

func (s *looseString) UnmarshalJSON(b []byte) error {
	var str string
	if json.Unmarshal(b, &str) == nil {
		*s = looseString(str)
		return nil
	}
	if bytes.Equal(b, []byte("null")) {
		*s = ""
		return nil
	}
	*s = looseString(b)
	return nil
}

// messageField accepts both message shapes SDKs send: a plain string, or an
// object {"formatted": "...", "message": "template %s", "params": [...]}
// (Java, .NET, PHP, and Python's logentry).
type messageField struct {
	Formatted string
	Template  string
}

func (m *messageField) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		m.Formatted, m.Template = s, s
		return nil
	}
	var obj struct {
		Formatted looseString `json:"formatted"`
		Message   looseString `json:"message"`
	}
	if json.Unmarshal(b, &obj) == nil {
		m.Formatted, m.Template = string(obj.Formatted), string(obj.Message)
	}
	return nil
}

// groupKey is the template when present, so "User %s logged in" groups every
// user together; display uses the formatted text.
func (m messageField) groupKey() string {
	if m.Template != "" {
		return m.Template
	}
	return m.Formatted
}

func (m messageField) display() string {
	if m.Formatted != "" {
		return m.Formatted
	}
	return m.Template
}

// exceptions decodes both the {"values": [...]} and the bare-list form.
func (p eventPayload) exceptions() []exceptionValue {
	if len(p.Exception) == 0 {
		return nil
	}
	var wrapped struct {
		Values []exceptionValue `json:"values"`
	}
	if json.Unmarshal(p.Exception, &wrapped) == nil && len(wrapped.Values) > 0 {
		return wrapped.Values
	}
	var list []exceptionValue
	if json.Unmarshal(p.Exception, &list) == nil {
		return list
	}
	return nil
}

func (p eventPayload) message() messageField {
	if p.Message.groupKey() != "" {
		return p.Message
	}
	return p.LogEntry
}

func computeFrom(p eventPayload, ok bool) (fingerprint, legacy string) {
	if !ok {
		// Never hash the raw payload: it contains the event ID and timestamp,
		// so every occurrence would become its own issue.
		return sha256hex([]byte("unparseable")), ""
	}
	fingerprint = fingerprintFor(p, defaultKeyV2(p))
	if v1 := fingerprintFor(p, defaultKeyV1(p)); v1 != fingerprint {
		legacy = v1
	}
	return fingerprint, legacy
}

func fingerprintFor(p eventPayload, def string) string {
	if custom := p.fingerprintParts(); len(custom) > 0 {
		parts := make([]string, len(custom))
		for i, part := range custom {
			switch part {
			case "{{ default }}", "{{default}}":
				parts[i] = def
			default:
				parts[i] = string(part)
			}
		}
		return sha256hex([]byte(strings.Join(parts, "|")))
	}
	return sha256hex([]byte(def))
}

// defaultKeyV1 is the original grouping key: the innermost exception's type and
// value, or the message, each with the transaction appended.
func defaultKeyV1(p eventPayload) string {
	if values := p.exceptions(); len(values) > 0 {
		last := values[len(values)-1]
		key := string(last.Type) + "|" + string(last.Value)
		if p.Transaction != "" {
			key += "|" + string(p.Transaction)
		}
		return key
	}
	if msg := p.message().groupKey(); msg != "" {
		if p.Transaction != "" {
			return msg + "|" + string(p.Transaction)
		}
		return msg
	}
	// Events with neither used to hash the raw payload (one issue per event);
	// group them per transaction instead.
	return "unlabeled|" + string(p.Transaction)
}

// defaultKeyV2 keys exceptions by type and in-app frames when those exist.
func defaultKeyV2(p eventPayload) string {
	values := p.exceptions()
	if len(values) == 0 || p.Platform == "javascript" {
		// Browser stack frames carry minified names that change every deploy.
		return defaultKeyV1(p)
	}
	last := values[len(values)-1]
	frames := frameKeys(last)
	if len(frames) == 0 {
		return defaultKeyV1(p)
	}
	return "v2|" + string(last.Type) + "|" + strings.Join(frames, ">")
}

// frameKeys returns module.function for the exception's in-app frames, or for
// frames of unknown origin when none are marked in-app. Consecutive repeats
// (recursion) collapse so recursion depth does not split issues.
func frameKeys(e exceptionValue) []string {
	if e.Stacktrace == nil {
		return nil
	}
	pick := func(include func(stackFrame) bool) []string {
		var keys []string
		for _, f := range e.Stacktrace.Frames {
			fn := string(f.Function)
			if !include(f) || fn == "" || fn == "?" || fn == "<anonymous>" {
				continue
			}
			key := fn
			if f.Module != "" {
				key = string(f.Module) + "." + fn
			}
			if len(keys) > 0 && keys[len(keys)-1] == key {
				continue
			}
			keys = append(keys, key)
		}
		return keys
	}
	if keys := pick(func(f stackFrame) bool { return f.InApp != nil && *f.InApp }); len(keys) > 0 {
		return keys
	}
	return pick(func(f stackFrame) bool { return f.InApp == nil })
}

func titleFrom(p eventPayload, ok bool) string {
	if !ok {
		return "Unknown error"
	}
	if values := p.exceptions(); len(values) > 0 {
		last := values[len(values)-1]
		if last.Value != "" {
			return truncRunes(fmt.Sprintf("%s: %s", last.Type, last.Value), 200)
		}
		if last.Type != "" {
			return string(last.Type)
		}
	}
	if msg := p.message().display(); msg != "" {
		return truncRunes(msg, 200)
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
	return string(r[:max-1]) + "…"
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
