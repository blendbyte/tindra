-- App users are the people using the instrumented application (from
-- sentry_sdk.set_user()), not Tindra teammates. identity is
-- COALESCE(id, username, email) — the same rule as issue user_count,
-- excluding IP so a DHCP change does not mint a new person.

CREATE TABLE app_users (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    identity   TEXT        NOT NULL,
    user_id    TEXT,
    username   TEXT,
    email      TEXT,
    name       TEXT,
    last_seen  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, identity)
);

CREATE INDEX app_users_project_seen ON app_users (project_id, last_seen DESC);

-- Existing error events already carry payload.user; a generated column
-- makes them filterable without a backfill of the events table itself.
ALTER TABLE events ADD COLUMN user_identity TEXT
    GENERATED ALWAYS AS (
        COALESCE(
            NULLIF(payload->'user'->>'id', ''),
            NULLIF(payload->'user'->>'username', ''),
            NULLIF(payload->'user'->>'email', '')
        )
    ) STORED;

CREATE INDEX events_user_issue ON events (user_identity, issue_id)
    WHERE issue_id IS NOT NULL AND user_identity IS NOT NULL;

ALTER TABLE logs ADD COLUMN user_identity TEXT
    GENERATED ALWAYS AS (
        COALESCE(
            NULLIF(attributes->>'user.id', ''),
            NULLIF(attributes->>'user.username', ''),
            NULLIF(attributes->>'user.email', '')
        )
    ) STORED;

CREATE INDEX logs_project_user ON logs (project_id, user_identity, timestamp DESC)
    WHERE user_identity IS NOT NULL;

-- Transactions never stored the user object. Columns are ingest-forward;
-- historical rows stay NULL.
ALTER TABLE transactions
    ADD COLUMN user_identity TEXT,
    ADD COLUMN user_id       TEXT,
    ADD COLUMN user_username TEXT,
    ADD COLUMN user_email    TEXT,
    ADD COLUMN user_name     TEXT;

CREATE INDEX transactions_project_user ON transactions (project_id, user_identity, start_timestamp DESC)
    WHERE user_identity IS NOT NULL;

-- Seed the picker from events and logs already on disk.
INSERT INTO app_users (project_id, identity, user_id, username, email, name, last_seen)
SELECT DISTINCT ON (project_id, user_identity)
    project_id,
    user_identity,
    NULLIF(payload->'user'->>'id', ''),
    NULLIF(payload->'user'->>'username', ''),
    NULLIF(payload->'user'->>'email', ''),
    NULLIF(payload->'user'->>'name', ''),
    timestamp
FROM events
WHERE user_identity IS NOT NULL
ORDER BY project_id, user_identity, timestamp DESC
ON CONFLICT (project_id, identity) DO NOTHING;

INSERT INTO app_users (project_id, identity, user_id, username, email, name, last_seen)
SELECT DISTINCT ON (project_id, user_identity)
    project_id,
    user_identity,
    NULLIF(attributes->>'user.id', ''),
    NULLIF(attributes->>'user.username', ''),
    NULLIF(attributes->>'user.email', ''),
    NULLIF(attributes->>'user.name', ''),
    timestamp
FROM logs
WHERE user_identity IS NOT NULL
ORDER BY project_id, user_identity, timestamp DESC
ON CONFLICT (project_id, identity) DO UPDATE SET
    user_id   = COALESCE(EXCLUDED.user_id, app_users.user_id),
    username  = COALESCE(EXCLUDED.username, app_users.username),
    email     = COALESCE(EXCLUDED.email, app_users.email),
    name      = COALESCE(EXCLUDED.name, app_users.name),
    last_seen = GREATEST(app_users.last_seen, EXCLUDED.last_seen);
