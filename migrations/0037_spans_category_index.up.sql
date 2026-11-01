-- The span pages (database, cache, jobs) each aggregate one category of ops,
-- and on real installs those are a small share of all spans (about 20% on a
-- large Laravel install, where view spans alone are 68%). One small partial
-- index per category replaces three full-width ones that every span insert and
-- retention delete had to maintain. Each span lands in at most one of them.
--
-- The predicates must match spanOpFilter in internal/storage/spans.go arm for
-- arm, or the planner cannot use them.
--
-- On a large install each build reads the whole spans table twice, and startup
-- waits until the migration is done. They can be run by hand beforehand (the
-- statements below, verbatim), without blocking ingest; the migration then
-- skips them. If a build was interrupted, drop the INVALID index first, e.g.
-- DROP INDEX CONCURRENTLY spans_db_start;
CREATE INDEX CONCURRENTLY IF NOT EXISTS spans_db_start
    ON spans (project_id, start_timestamp DESC, op text_pattern_ops)
    WHERE op LIKE 'db%';

-- tindra:next-batch

CREATE INDEX CONCURRENTLY IF NOT EXISTS spans_cache_start
    ON spans (project_id, start_timestamp DESC, op text_pattern_ops)
    WHERE op LIKE 'cache%';

-- tindra:next-batch

CREATE INDEX CONCURRENTLY IF NOT EXISTS spans_job_start
    ON spans (project_id, start_timestamp DESC, op text_pattern_ops)
    WHERE op LIKE 'task%' OR op LIKE 'queue%' OR op LIKE 'celery%' OR op LIKE 'job%';

-- tindra:next-batch

-- Replaced by the category indexes above.
DROP INDEX CONCURRENTLY IF EXISTS spans_proj_start_op;

-- tindra:next-batch

-- Replaced by the category indexes above.
DROP INDEX CONCURRENTLY IF EXISTS spans_op_start;

-- tindra:next-batch

-- Served an all-ops span view that no page or API has.
DROP INDEX CONCURRENTLY IF EXISTS spans_start_op;

-- tindra:next-batch

-- Duplicates transactions_pkey. Its main reader was the foreign key check on
-- each span insert, which the much smaller primary key serves as well.
DROP INDEX CONCURRENTLY IF EXISTS transactions_id_covering;

-- tindra:next-batch

-- transactions_project_perf leads with the same columns and serves the same
-- reads; on a large install this one was never scanned.
DROP INDEX CONCURRENTLY IF EXISTS transactions_project_start;
