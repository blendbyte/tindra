-- The Tindra runner executes each concurrent index build in a separate batch.
CREATE INDEX CONCURRENTLY logs_timestamp_id
    ON logs (timestamp DESC, id DESC);

-- tindra:next-batch

CREATE INDEX CONCURRENTLY logs_received_id ON logs (received_at, id);
