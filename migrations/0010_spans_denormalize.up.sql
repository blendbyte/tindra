-- Denormalize project_id, environment, release onto spans so that
-- GetSpanSummaries and GetSpanTimeseries can filter without joining transactions.

ALTER TABLE spans ADD COLUMN project_id  UUID;
ALTER TABLE spans ADD COLUMN environment TEXT;
ALTER TABLE spans ADD COLUMN release     TEXT;

UPDATE spans s
SET project_id  = t.project_id,
    environment = t.environment,
    release     = t.release
FROM transactions t
WHERE s.transaction_id = t.id;

ALTER TABLE spans ALTER COLUMN project_id SET NOT NULL;

-- New composite index: project first (very selective), then time range, then op prefix.
-- Replaces spans_op_start which forced a cross-project scan before joining transactions.
CREATE INDEX spans_proj_start_op ON spans (project_id, start_timestamp DESC, op text_pattern_ops);
DROP INDEX spans_op_start;

-- Covering index so GetSpanSamples JOIN on transactions is index-only.
CREATE INDEX transactions_id_covering ON transactions (id)
    INCLUDE (project_id, environment, release, transaction, trace_id);
