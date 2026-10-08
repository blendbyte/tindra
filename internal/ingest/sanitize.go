package ingest

import (
	"bytes"
	"encoding/json"
	"strings"
)

// PostgreSQL rejects NUL in text columns (invalid byte sequence, 22021) and the
// \u0000 escape in JSONB (unsupported Unicode escape sequence, 22P05). Both are
// valid in the JSON an SDK sends, so ingest strips them before queueing, as
// Sentry does. Otherwise the record is rejected at flush and lost.

// stripNUL removes NUL bytes from a string bound for a text column.
func stripNUL(s string) string {
	if strings.IndexByte(s, 0) < 0 {
		return s
	}
	return strings.ReplaceAll(s, "\x00", "")
}

// sanitizeJSONPayload removes Unicode null escapes rejected by PostgreSQL JSONB.
func sanitizeJSONPayload(p json.RawMessage) json.RawMessage {
	const nullEscape = `\u0000`
	if !bytes.Contains(p, []byte(nullEscape)) {
		return p
	}
	var cleaned json.RawMessage
	start := 0
	for i := 0; i < len(p); i++ {
		if p[i] != '\\' {
			continue
		}
		if bytes.HasPrefix(p[i:], []byte(nullEscape)) {
			if cleaned == nil {
				cleaned = make(json.RawMessage, 0, len(p))
			}
			cleaned = append(cleaned, p[start:i]...)
			i += len(nullEscape) - 1
			start = i + 1
		} else {
			// Skip the escaped byte. In particular, \\u0000 is literal text,
			// whereas \\\u0000 is an escaped backslash followed by a null escape.
			i++
		}
	}
	if cleaned == nil {
		return p
	}
	return append(cleaned, p[start:]...)
}

// sanitizeLog covers attribute keys as well as values, since both are
// arbitrary SDK input.
func sanitizeLog(l BufferedLog) BufferedLog {
	l.Level = stripNUL(l.Level)
	l.Body = stripNUL(l.Body)
	l.TraceID = stripNUL(l.TraceID)
	l.SpanID = stripNUL(l.SpanID)
	l.Environment = stripNUL(l.Environment)
	l.Release = stripNUL(l.Release)
	l.Attributes = sanitizeJSONPayload(l.Attributes)
	return l
}

// sanitizeTransaction copies the spans it cleans, so the caller's slice is
// never modified.
func sanitizeTransaction(tx BufferedTransaction) BufferedTransaction {
	tx.EventID = stripNUL(tx.EventID)
	tx.ProfilerID = stripNUL(tx.ProfilerID)
	tx.ThreadID = stripNUL(tx.ThreadID)
	tx.TraceID = stripNUL(tx.TraceID)
	tx.SpanID = stripNUL(tx.SpanID)
	tx.Transaction = stripNUL(tx.Transaction)
	tx.Op = stripNUL(tx.Op)
	tx.Status = stripNUL(tx.Status)
	tx.Environment = stripNUL(tx.Environment)
	tx.Release = stripNUL(tx.Release)
	tx.Platform = stripNUL(tx.Platform)
	tx.Measurements = sanitizeJSONPayload(tx.Measurements)
	tx.UserIdentity = stripNUL(tx.UserIdentity)
	tx.UserID = stripNUL(tx.UserID)
	tx.UserUsername = stripNUL(tx.UserUsername)
	tx.UserEmail = stripNUL(tx.UserEmail)
	tx.UserName = stripNUL(tx.UserName)
	if len(tx.Spans) > 0 {
		spans := make([]BufferedSpan, len(tx.Spans))
		for i, sp := range tx.Spans {
			sp.SpanID = stripNUL(sp.SpanID)
			sp.ParentSpanID = stripNUL(sp.ParentSpanID)
			sp.Op = stripNUL(sp.Op)
			sp.Description = stripNUL(sp.Description)
			sp.Status = stripNUL(sp.Status)
			sp.Data = sanitizeJSONPayload(sp.Data)
			spans[i] = sp
		}
		tx.Spans = spans
	}
	return tx
}

// sanitizeProfile leaves Data alone: it is an encoded blob stored as bytea.
func sanitizeProfile(p BufferedProfile) BufferedProfile {
	p.TransactionEventID = stripNUL(p.TransactionEventID)
	p.ChunkID = stripNUL(p.ChunkID)
	p.ProfilerID = stripNUL(p.ProfilerID)
	p.TraceID = stripNUL(p.TraceID)
	p.Environment = stripNUL(p.Environment)
	p.Release = stripNUL(p.Release)
	p.Platform = stripNUL(p.Platform)
	return p
}
