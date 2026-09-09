BEGIN;
SET LOCAL lock_timeout = '5s';
LOCK TABLE events, transactions, profile_chunks IN SHARE ROW EXCLUSIVE MODE;

-- Milestones are independent of usage buckets and retention. One row per kind
-- and project, updated once per insert statement rather than once per event.
CREATE TABLE project_setup_receipts (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('events', 'transactions', 'profile_chunks')),
    first_received_at timestamptz NOT NULL,
    last_received_at timestamptz NOT NULL,
    latest_id uuid NOT NULL,
    PRIMARY KEY (project_id, kind)
);

CREATE TABLE project_setup_checks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT now() + interval '24 hours',
    received_at timestamptz,
    event_id uuid
);
CREATE INDEX project_setup_checks_project ON project_setup_checks(project_id, created_at DESC);

-- Only the latest queued and rejected observation per kind is retained.
CREATE TABLE project_setup_observations (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind text NOT NULL CHECK (kind IN ('envelope', 'events', 'transactions', 'profile_chunks')),
    outcome text NOT NULL CHECK (outcome IN ('queued', 'rejected')),
    reason text NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (project_id, kind, outcome)
);

CREATE FUNCTION track_project_setup() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO project_setup_receipts(project_id, kind, first_received_at, last_received_at, latest_id)
    SELECT DISTINCT ON (project_id) project_id, TG_TABLE_NAME,
           min(received_at) OVER (PARTITION BY project_id), received_at, id
    FROM new_rows ORDER BY project_id, received_at DESC, id DESC
    ON CONFLICT (project_id, kind) DO UPDATE SET
        first_received_at = least(project_setup_receipts.first_received_at, EXCLUDED.first_received_at),
        last_received_at = greatest(project_setup_receipts.last_received_at, EXCLUDED.last_received_at),
        latest_id = CASE WHEN EXCLUDED.last_received_at >= project_setup_receipts.last_received_at
                         THEN EXCLUDED.latest_id ELSE project_setup_receipts.latest_id END;

    IF TG_TABLE_NAME = 'events' THEN
        UPDATE project_setup_checks c SET received_at = e.received_at, event_id = e.id
        FROM new_rows e WHERE c.project_id = e.project_id
          AND e.payload #>> '{tags,tindra_setup}' = c.id::text
          AND c.received_at IS NULL AND e.received_at >= c.created_at AND e.received_at < c.expires_at;
    END IF;
    RETURN NULL;
END;
$$;

DO $$
DECLARE relation text;
BEGIN
    FOREACH relation IN ARRAY ARRAY['events', 'transactions', 'profile_chunks'] LOOP
        EXECUTE format('INSERT INTO project_setup_receipts SELECT DISTINCT ON (project_id) project_id, %L, min(received_at) OVER (PARTITION BY project_id), received_at, id FROM %I ORDER BY project_id, received_at DESC, id DESC', relation, relation);
        EXECUTE format('CREATE TRIGGER setup_insert AFTER INSERT ON %I REFERENCING NEW TABLE AS new_rows FOR EACH STATEMENT EXECUTE FUNCTION track_project_setup()', relation);
    END LOOP;
END;
$$;
COMMIT;

-- tindra:next-batch

-- Setup diagnostics inspect a bounded recent sample within one project.
CREATE INDEX CONCURRENTLY transactions_project_received ON transactions(project_id, received_at DESC, id DESC);
