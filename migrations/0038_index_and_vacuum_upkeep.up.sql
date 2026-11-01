-- Cheap upkeep for large installs; no table is rewritten, and every
-- statement can run again after a failed attempt.

-- No query filters on log attributes with a JSONB operator, and a GIN entry
-- per log was a large share of log insert time and WAL.
DROP INDEX CONCURRENTLY IF EXISTS logs_attributes;

-- tindra:next-batch

-- The alert evaluator asks for issues regressed since its last tick every 15
-- seconds; without this it reads the whole issues table each time.
CREATE INDEX CONCURRENTLY IF NOT EXISTS issues_regressed_at ON issues (regressed_at)
    WHERE regressed_at IS NOT NULL;

-- tindra:next-batch

-- At the default scale factor of 0.2, a large append-mostly table is vacuumed
-- only after a fifth of it has changed: every few days for events, so its
-- visibility map goes stale and index-only scans fall back to the heap. These
-- settings take a lock that does not block reads or writes.
ALTER TABLE events SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_vacuum_insert_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.02);
ALTER TABLE event_tags SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_vacuum_insert_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.02);
ALTER TABLE transactions SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_vacuum_insert_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.02);
ALTER TABLE spans SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_vacuum_insert_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.02);
ALTER TABLE logs SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_vacuum_insert_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.02);
-- Every event updates its issue, so issues churn far faster than they grow.
ALTER TABLE issues SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_analyze_scale_factor = 0.02);
