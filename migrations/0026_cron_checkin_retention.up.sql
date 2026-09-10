CREATE INDEX CONCURRENTLY cron_checkins_completed_retention
    ON cron_checkins (COALESCE(finished_at, received_at), id)
    WHERE status IN ('ok', 'error');
