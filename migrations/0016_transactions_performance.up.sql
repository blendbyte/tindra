-- The Tindra runner executes each concurrent index build in a separate batch.
CREATE INDEX CONCURRENTLY transactions_start_id
    ON transactions (start_timestamp DESC, id DESC);

-- tindra:next-batch

CREATE INDEX CONCURRENTLY transactions_project_release
    ON transactions (project_id, release) WHERE release IS NOT NULL;

-- tindra:next-batch

CREATE INDEX CONCURRENTLY perf_events_transaction ON perf_events (transaction_id);

-- tindra:next-batch

CREATE INDEX CONCURRENTLY transactions_received_id ON transactions (received_at, id);
