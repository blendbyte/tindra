-- When a push device's current run of failed deliveries started. A device is
-- dropped only after failing for a while, not after a burst of retries, and
-- registering it again starts the clock over.
ALTER TABLE push_subscriptions ADD COLUMN failing_since TIMESTAMPTZ;

-- The push service health check looks for any recent successful delivery;
-- this lets it read the newest successes first and stop at the first match.
CREATE INDEX push_subscriptions_last_success ON push_subscriptions (last_success_at DESC)
    WHERE last_success_at IS NOT NULL;
