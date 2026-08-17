package ingest

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

const scrubPlaceholder = "[Filtered]"

const (
	MaxScrubPatterns   = 20
	MaxScrubPatternLen = 200
)

// ValidateScrubPatterns rejects a pattern list that would be unsafe to store:
// too many entries, a custom pattern that exceeds MaxScrubPatternLen, or one
// that does not compile. Builtin patterns are always trusted and skipped.
func ValidateScrubPatterns(patterns []ScrubPattern) error {
	if len(patterns) > MaxScrubPatterns {
		return fmt.Errorf("too many scrub patterns: maximum %d", MaxScrubPatterns)
	}
	for _, p := range patterns {
		if p.Builtin {
			continue
		}
		if len(p.Pattern) > MaxScrubPatternLen {
			return fmt.Errorf("pattern %q exceeds maximum length of %d characters", p.Name, MaxScrubPatternLen)
		}
		if _, err := regexp.Compile(p.Pattern); err != nil {
			return fmt.Errorf("pattern %q is not a valid regular expression: %v", p.Name, err)
		}
	}
	return nil
}

// builtinPatterns maps a well-known name to its regex. The frontend sends
// {name: "email", builtin: true} and we resolve the pattern here, keeping
// the regex out of user-supplied data.
var builtinPatterns = map[string]string{
	"email": `[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`,
	"ip":    `\b(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\b`,
}

type ScrubPattern struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
	Builtin bool   `json:"builtin"`
	Enabled bool   `json:"enabled"`
}

type ScrubConfig struct {
	Fields   []string       `json:"fields"`
	Patterns []ScrubPattern `json:"patterns"`
}

var reCache sync.Map // map[string]*regexp.Regexp

func compiledRe(pattern string) (*regexp.Regexp, error) {
	if v, ok := reCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	actual, _ := reCache.LoadOrStore(pattern, re)
	return actual.(*regexp.Regexp), nil
}

// ScrubEvent applies PII scrubbing to a raw event JSON payload and returns
// the scrubbed copy. The original slice is not modified.
func ScrubEvent(payload json.RawMessage, cfg ScrubConfig) json.RawMessage {
	fields, regexps := buildScrubber(cfg)
	if len(fields) == 0 && len(regexps) == 0 {
		return payload
	}
	var v any
	if err := json.Unmarshal(payload, &v); err != nil {
		return payload
	}
	scrubValue(v, fields, regexps, "")
	out, err := json.Marshal(v)
	if err != nil {
		return payload
	}
	return out
}

// ScrubTransaction applies PII scrubbing to the mutable parts of a transaction:
// span Data blobs and span Description strings. The transaction is modified in place.
func ScrubTransaction(tx *BufferedTransaction, cfg ScrubConfig) {
	_, regexps := buildScrubber(cfg)
	// Field-path blocking doesn't apply to the already-decomposed transaction
	// struct, but pattern scrubbing applies to free-text strings and data blobs.
	if len(regexps) == 0 {
		return
	}
	for i := range tx.Spans {
		tx.Spans[i].Description = scrubString(tx.Spans[i].Description, regexps)
		if len(tx.Spans[i].Data) > 0 {
			tx.Spans[i].Data = ScrubEvent(tx.Spans[i].Data, cfg)
		}
	}
}

// ScrubLog applies PII scrubbing to a log record: pattern scrubbing on the
// body, and both pattern scrubbing and field-path blocking on attributes.
// Attribute paths are relative to the attributes object, so an attribute named
// "user.email" is blocked by configuring "user.email". This mirrors how span
// Data blobs are scrubbed on transactions. The log is modified in place.
//
// Level, trace and span IDs are left alone, as is the environment and release
// pair lifted out of attributes during parsing: none of them carry free text.
func ScrubLog(l *BufferedLog, cfg ScrubConfig) {
	if len(cfg.Fields) == 0 && len(cfg.Patterns) == 0 {
		return
	}
	fields, regexps := buildScrubber(cfg)
	if len(fields) == 0 && len(regexps) == 0 {
		return
	}
	l.Body = scrubString(l.Body, regexps)
	if len(l.Attributes) > 0 {
		l.Attributes = ScrubEvent(l.Attributes, cfg)
	}
}

func buildScrubber(cfg ScrubConfig) (fields map[string]struct{}, regexps []*regexp.Regexp) {
	fields = make(map[string]struct{}, len(cfg.Fields))
	for _, f := range cfg.Fields {
		fields[strings.ToLower(f)] = struct{}{}
	}
	for _, p := range cfg.Patterns {
		if !p.Enabled {
			continue
		}
		pat := p.Pattern
		if p.Builtin {
			bp, ok := builtinPatterns[p.Name]
			if !ok {
				continue
			}
			pat = bp
		}
		if pat == "" {
			continue
		}
		if re, err := compiledRe(pat); err == nil {
			regexps = append(regexps, re)
		}
	}
	return
}

func scrubValue(v any, fields map[string]struct{}, regexps []*regexp.Regexp, path string) any {
	switch val := v.(type) {
	case map[string]any:
		for k, child := range val {
			childPath := k
			if path != "" {
				childPath = path + "." + k
			}
			if _, blocked := fields[strings.ToLower(childPath)]; blocked {
				val[k] = scrubPlaceholder
			} else {
				val[k] = scrubValue(child, fields, regexps, childPath)
			}
		}
		return val
	case []any:
		for i, item := range val {
			val[i] = scrubValue(item, fields, regexps, path)
		}
		return val
	case string:
		return scrubString(val, regexps)
	default:
		return v
	}
}

func scrubString(s string, regexps []*regexp.Regexp) string {
	for _, re := range regexps {
		s = re.ReplaceAllString(s, scrubPlaceholder)
	}
	return s
}
