-- The logs list resolves each row's trace_id to the transaction that shares it
-- so the UI can deep-link a log line to its trace waterfall. That lateral
-- lookup (and the existing GetTransactionByTraceID used by the issue trace
-- preview) filters transactions on trace_id alone, which had no supporting
-- index and therefore sequentially scanned the whole table once per log row.
--
-- Index trace_id with received_at so the "most recent transaction for this
-- trace" lookup is a single index seek. Partial on NOT NULL because trace_id is
-- nullable and untraced transactions are never looked up this way.
CREATE INDEX transactions_trace_id ON transactions (trace_id, received_at DESC)
    WHERE trace_id IS NOT NULL;
