-- Match cron error evaluation and payload enrichment, including legacy pings
-- without a completion timestamp.
CREATE INDEX CONCURRENTLY idx_cron_checkins_error_completion
    ON cron_checkins (COALESCE(finished_at, received_at), monitor_id)
    WHERE status = 'error';
