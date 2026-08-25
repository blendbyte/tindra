-- SDKs retry envelopes after a 429 or a 5xx, so the same profile can arrive
-- more than once. Nothing deduplicated it, and foldV2 folds every matching row
-- into one graph, so a redelivered chunk doubled every sample count in it.
--
-- Both indexes are partial because each id is only set for one of the two wire
-- formats. The v1 lookup already reads a single row, but a duplicate there
-- still wastes the largest storage in the schema.
CREATE UNIQUE INDEX profile_chunks_chunk_uniq
    ON profile_chunks (project_id, chunk_id)
    WHERE chunk_id IS NOT NULL;

CREATE UNIQUE INDEX profile_chunks_transaction_uniq
    ON profile_chunks (project_id, transaction_event_id)
    WHERE transaction_event_id IS NOT NULL;

-- The unique index above covers exactly what this one did, same columns and
-- same predicate, so keeping both meant maintaining two identical indexes on
-- the highest-churn table in the schema. Dropped here rather than edited out of
-- 0013 so that databases which already ran 0013 are corrected too.
DROP INDEX IF EXISTS profile_chunks_transaction;

-- Dead since the storage cap moved to ranking by received_at: nothing filters
-- or orders on start_ts alone. The overlap lookup in foldV2 leads with
-- project_id and profiler_id, which profile_chunks_profiler already serves.
DROP INDEX IF EXISTS profile_chunks_start;
