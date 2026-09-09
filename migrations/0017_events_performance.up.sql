-- The Tindra runner executes each concurrent index build in a separate batch.
CREATE INDEX CONCURRENTLY events_issue_received_id
    ON events (issue_id, received_at DESC, id DESC)
    WHERE issue_id IS NOT NULL;

-- tindra:next-batch

CREATE INDEX CONCURRENTLY events_received_id ON events (received_at, id);
