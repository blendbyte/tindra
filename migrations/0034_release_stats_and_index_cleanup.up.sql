-- Release lists compute count, p50 and error rate over every transaction of
-- each release. Covering duration_ms and status lets that read the index alone.
CREATE INDEX CONCURRENTLY transactions_project_release_stats
    ON transactions (project_id, release) INCLUDE (duration_ms, status)
    WHERE release IS NOT NULL;

-- tindra:next-batch

DROP INDEX CONCURRENTLY IF EXISTS transactions_project_release;

-- tindra:next-batch

-- Duplicates the leading columns of the UNIQUE (project_id, release, url) index.
DROP INDEX CONCURRENTLY IF EXISTS sourcemaps_project_release;

-- tindra:next-batch

-- Cannot serve ListDueAlertRules, whose cooldown condition is an expression
-- over two columns; the table is small enough that it was never used.
DROP INDEX CONCURRENTLY IF EXISTS alert_rules_enabled;
