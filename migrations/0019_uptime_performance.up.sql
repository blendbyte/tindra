-- The Tindra runner executes each concurrent index build in a separate batch.
CREATE INDEX CONCURRENTLY uptime_checks_checked_id ON uptime_checks (checked_at, id);
