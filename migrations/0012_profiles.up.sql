-- Profiling support.
--
-- Two wire formats land in one table. A v1 profile belongs to exactly one
-- transaction and carries its event id. A v2 chunk from continuous profiling
-- carries no transaction link at all: the transaction names a profiler_id and
-- a thread, and the chunks covering its time window are found by range scan.

-- Linking columns on transactions. event_id was not stored before; it is the
-- target a v1 profile points at. profiler_id and thread_id come from
-- contexts.profile and contexts.trace.data on continuous-profiling events.
ALTER TABLE transactions
    ADD COLUMN event_id    TEXT,
    ADD COLUMN profiler_id TEXT,
    ADD COLUMN thread_id   TEXT;

-- Deliberately no index on these. The read path goes transaction -> profile,
-- and a transaction row already carries both values, so an index here would
-- only pay off for the reverse lookup, which nothing performs. transactions is
-- a high-volume table and speculative indexes are not free.

-- Per-project kill switch, so a noisy service can be silenced from the UI
-- without redeploying the app to change its SDK config.
ALTER TABLE projects
    ADD COLUMN profiling_enabled BOOLEAN NOT NULL DEFAULT TRUE;

CREATE TABLE profile_chunks (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,

    -- 1 = transaction-based (v1), 2 = continuous chunk (v2).
    format      SMALLINT    NOT NULL,

    -- v1 only: the transaction event this profile belongs to.
    transaction_event_id TEXT,
    trace_id             TEXT,
    -- v2 only: the profiler session and the chunk within it.
    profiler_id TEXT,
    chunk_id    TEXT,

    -- First and last sample. Both formats normalize to absolute timestamps,
    -- which is what lets one range query serve both.
    --
    -- TIMESTAMPTZ resolves to microseconds while sample times are nanoseconds,
    -- so these are a coarse index into the blob rather than an exact copy. That
    -- is still four orders of magnitude finer than the 101 Hz sample rate, and
    -- the authoritative per-sample times live inside the payload where the
    -- flame graph reads them.
    start_ts    TIMESTAMPTZ NOT NULL,
    end_ts      TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    environment TEXT,
    release     TEXT,
    platform    TEXT,

    sample_count INTEGER    NOT NULL,
    -- Compressed size, denormalized so the retention worker can enforce a byte
    -- budget by summing a column instead of measuring TOASTed payloads.
    size_bytes   INTEGER    NOT NULL,
    -- Blob format, so a future encoding can coexist with existing rows.
    encoding     SMALLINT   NOT NULL DEFAULT 1,

    data        BYTEA       NOT NULL
);

-- v2 read path: chunks for one profiler overlapping a transaction's window.
-- Chunks are bounded at 66s upstream, so the caller can bound start_ts and let
-- this btree do the work instead of needing a GiST range index.
CREATE INDEX profile_chunks_profiler
    ON profile_chunks (project_id, profiler_id, start_ts)
    WHERE profiler_id IS NOT NULL;

-- v1 read path: straight from a transaction's event id.
CREATE INDEX profile_chunks_transaction
    ON profile_chunks (project_id, transaction_event_id)
    WHERE transaction_event_id IS NOT NULL;

-- Retention purges by age; the byte budget purges oldest-first by start_ts.
CREATE INDEX profile_chunks_received ON profile_chunks (received_at);
CREATE INDEX profile_chunks_start    ON profile_chunks (start_ts);

-- The payload is already zstd-compressed, so Postgres attempting pglz on it
-- burns CPU on every read and write for no gain. EXTERNAL stores it out of
-- line and uncompressed.
ALTER TABLE profile_chunks ALTER COLUMN data SET STORAGE EXTERNAL;

-- This is the highest-churn large-object table in the schema and deleting
-- TOASTed rows leaves the toast table needing vacuum, so autovacuum runs
-- harder here than the instance default.
ALTER TABLE profile_chunks SET (
    autovacuum_vacuum_scale_factor  = 0.02,
    autovacuum_analyze_scale_factor = 0.02
);
