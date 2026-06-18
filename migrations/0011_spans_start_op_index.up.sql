-- Migration 0010 dropped spans_op_start (op, start_timestamp) and replaced it
-- with spans_proj_start_op (project_id first). That index is efficient when a
-- project filter is active, but category-specific span queries (db, cache, job)
-- filter by op prefix with no project_id predicate, so Postgres cannot use the
-- leading key and ends up scanning every span in the time window instead of
-- only the relevant category rows.
--
-- Restore the op-first index so category queries seek directly to matching ops
-- before applying the time range, reading only the relevant subset of spans.
CREATE INDEX spans_op_start ON spans (op text_pattern_ops, start_timestamp DESC);

-- Secondary index for the all-spans view (no op filter): leads on time so
-- Postgres can do a single range seek rather than fanning out across all
-- project_id values in spans_proj_start_op.
CREATE INDEX spans_start_op ON spans (start_timestamp DESC, op text_pattern_ops);
