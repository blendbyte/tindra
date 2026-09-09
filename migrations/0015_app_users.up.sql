-- Keep identity normalization identical to Go's strings.TrimSpace and
-- UserIdentity: skip empty and scrubbed fields before trying the next field.
CREATE FUNCTION app_user_identity_field(value TEXT) RETURNS TEXT
LANGUAGE SQL IMMUTABLE PARALLEL SAFE RETURNS NULL ON NULL INPUT
RETURN NULLIF(NULLIF(btrim(value, U&'\0009\000A\000B\000C\000D\0020\0085\00A0\1680\2000\2001\2002\2003\2004\2005\2006\2007\2008\2009\200A\2028\2029\202F\205F\3000'), ''), '[Filtered]');

-- Fixed UTF-8 encoding makes this digest deterministic. Index the digest so
-- arbitrary-length SDK identities cannot exceed PostgreSQL's B-tree row limit.
CREATE FUNCTION app_user_identity_hash(value TEXT) RETURNS BYTEA
LANGUAGE SQL IMMUTABLE PARALLEL SAFE RETURNS NULL ON NULL INPUT
RETURN sha256(convert_to(value, 'UTF8'));

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
    last_seen  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX app_users_project_identity ON app_users (app_user_identity_hash(identity), project_id);

CREATE INDEX app_users_project_seen ON app_users (project_id, last_seen DESC);
CREATE INDEX app_users_seen ON app_users (last_seen DESC);
-- A single multicolumn GIN index supports the four independent substring
-- matches without changing matching across field boundaries.
CREATE INDEX app_users_search ON app_users USING gin
    (identity gin_trgm_ops, username gin_trgm_ops, email gin_trgm_ops, name gin_trgm_ops);

-- Existing error events already carry payload.user; a generated column
-- makes them filterable without a backfill of the events table itself.
ALTER TABLE events ADD COLUMN user_identity TEXT
    GENERATED ALWAYS AS (
        COALESCE(
            app_user_identity_field(payload->'user'->>'id'),
            app_user_identity_field(payload->'user'->>'username'),
            app_user_identity_field(payload->'user'->>'email')
        )
    ) STORED;

-- Let recent-issue pages stop after their limit rather than sorting every
-- matching issue when filtering a high-volume user across projects.
CREATE INDEX issues_last_seen ON issues (last_seen DESC, id DESC);

CREATE INDEX events_user_issue ON events (app_user_identity_hash(user_identity), issue_id)
    WHERE issue_id IS NOT NULL AND user_identity IS NOT NULL;

ALTER TABLE logs ADD COLUMN user_identity TEXT
    GENERATED ALWAYS AS (
        COALESCE(
            app_user_identity_field(attributes->>'user.id'),
            app_user_identity_field(attributes->>'user.username'),
            app_user_identity_field(attributes->>'user.email')
        )
    ) STORED;

CREATE INDEX logs_project_user ON logs (project_id, app_user_identity_hash(user_identity), timestamp DESC, id DESC)
    WHERE user_identity IS NOT NULL;

-- Global lists need timestamp order directly after identity. The project-first
-- indexes above/below retain efficient scoped scans, including absent users.
CREATE INDEX logs_user_time ON logs (app_user_identity_hash(user_identity), timestamp DESC, id DESC)
    WHERE user_identity IS NOT NULL;

-- Transactions never stored the user object. Columns are ingest-forward;
-- historical rows stay NULL.
ALTER TABLE transactions
    ADD COLUMN user_identity TEXT,
    ADD COLUMN user_id       TEXT,
    ADD COLUMN user_username TEXT,
    ADD COLUMN user_email    TEXT,
    ADD COLUMN user_name     TEXT;

CREATE INDEX transactions_project_user ON transactions (project_id, app_user_identity_hash(user_identity), start_timestamp DESC, id DESC)
    WHERE user_identity IS NOT NULL;

CREATE INDEX transactions_user_time ON transactions (app_user_identity_hash(user_identity), start_timestamp DESC, id DESC)
    WHERE user_identity IS NOT NULL;

-- Seed the picker from events and logs already on disk.
INSERT INTO app_users (project_id, identity, user_id, username, email, name, last_seen)
SELECT DISTINCT ON (project_id, user_identity)
    project_id,
    user_identity,
    app_user_identity_field(payload->'user'->>'id'),
    app_user_identity_field(payload->'user'->>'username'),
    app_user_identity_field(payload->'user'->>'email'),
    app_user_identity_field(payload->'user'->>'name'),
    timestamp
FROM events
WHERE user_identity IS NOT NULL
ORDER BY project_id, user_identity, timestamp DESC
ON CONFLICT (project_id, app_user_identity_hash(identity)) DO NOTHING;

INSERT INTO app_users (project_id, identity, user_id, username, email, name, last_seen)
SELECT DISTINCT ON (project_id, user_identity)
    project_id,
    user_identity,
    app_user_identity_field(attributes->>'user.id'),
    app_user_identity_field(attributes->>'user.username'),
    app_user_identity_field(attributes->>'user.email'),
    app_user_identity_field(attributes->>'user.name'),
    timestamp
FROM logs
WHERE user_identity IS NOT NULL
ORDER BY project_id, user_identity, timestamp DESC
ON CONFLICT (project_id, app_user_identity_hash(identity)) DO UPDATE SET
    user_id   = COALESCE(EXCLUDED.user_id, app_users.user_id),
    username  = COALESCE(EXCLUDED.username, app_users.username),
    email     = COALESCE(EXCLUDED.email, app_users.email),
    name      = COALESCE(EXCLUDED.name, app_users.name),
    last_seen = GREATEST(app_users.last_seen, EXCLUDED.last_seen);
