CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE projects (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    slug            TEXT        NOT NULL UNIQUE,
    name            TEXT        NOT NULL,
    public_key      TEXT        NOT NULL UNIQUE,
    passthrough_dsn TEXT,
    scrub_fields    TEXT[]      NOT NULL DEFAULT '{}',
    scrub_patterns  JSONB       NOT NULL DEFAULT '[]',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE users (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email                TEXT        NOT NULL UNIQUE,
    name                 TEXT        NOT NULL DEFAULT '',
    password_hash        TEXT        NOT NULL,
    failed_attempts      INT         NOT NULL DEFAULT 0,
    locked_until         TIMESTAMPTZ,
    mfa_secret           TEXT,
    mfa_enabled          BOOLEAN     NOT NULL DEFAULT false,
    perm_manage_projects BOOLEAN     NOT NULL DEFAULT FALSE,
    perm_manage_users    BOOLEAN     NOT NULL DEFAULT FALSE,
    perm_manage_alerts   BOOLEAN     NOT NULL DEFAULT FALSE,
    perm_manage_issues   BOOLEAN     NOT NULL DEFAULT FALSE,
    weekly_digest        BOOLEAN     NOT NULL DEFAULT TRUE,
    digest_last_sent_at  TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE sessions (
    token_hash TEXT        PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX sessions_user ON sessions (user_id);

CREATE TABLE mfa_challenges (
    token      TEXT        PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX mfa_challenges_user ON mfa_challenges (user_id);

CREATE TABLE oauth_identities (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider   TEXT        NOT NULL,
    sub        TEXT        NOT NULL,
    email      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, sub)
);

CREATE INDEX oauth_identities_user ON oauth_identities (user_id);

CREATE TABLE oauth_states (
    token      TEXT        PRIMARY KEY,
    provider   TEXT        NOT NULL,
    verifier   TEXT        NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE user_invites (
    token       TEXT        PRIMARY KEY,
    email       TEXT        NOT NULL,
    name        TEXT,
    inviter_id  UUID        REFERENCES users(id) ON DELETE SET NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE password_reset_tokens (
    token      TEXT        PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ
);

CREATE INDEX password_reset_tokens_user ON password_reset_tokens (user_id);

CREATE TABLE issues (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id         UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    fingerprint        TEXT        NOT NULL,
    title              TEXT        NOT NULL,
    level              TEXT        NOT NULL DEFAULT 'error',
    kind               TEXT        NOT NULL DEFAULT 'error',
    first_seen         TIMESTAMPTZ NOT NULL,
    last_seen          TIMESTAMPTZ NOT NULL,
    event_count        BIGINT      NOT NULL DEFAULT 1,
    status             TEXT        NOT NULL DEFAULT 'open',
    assignee_id        UUID        REFERENCES users(id) ON DELETE SET NULL,
    environment        TEXT,
    ignore_until       TIMESTAMPTZ,
    ignore_count_limit INT,
    ignore_count       INT         NOT NULL DEFAULT 0,
    first_release      TEXT,
    CONSTRAINT issues_status_check CHECK (status IN ('open', 'resolved', 'ignored', 'regressed'))
);

CREATE INDEX issues_project_last_seen     ON issues (project_id, last_seen DESC);
CREATE INDEX issues_project_status        ON issues (project_id, status, last_seen DESC);
CREATE INDEX issues_project_first_release ON issues (project_id, first_release)
    WHERE first_release IS NOT NULL;
CREATE INDEX issues_project_first_seen    ON issues (project_id, first_seen DESC);
CREATE INDEX issues_assignee              ON issues (assignee_id) WHERE assignee_id IS NOT NULL;

CREATE TABLE issue_fingerprints (
    project_id  UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    fingerprint TEXT NOT NULL,
    issue_id    UUID NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, fingerprint)
);

CREATE INDEX issue_fingerprints_issue ON issue_fingerprints (issue_id);

CREATE TABLE issue_comments (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id   UUID        NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    body       TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX issue_comments_issue ON issue_comments (issue_id, created_at);
CREATE INDEX issue_comments_user  ON issue_comments (user_id);

CREATE TABLE issue_history (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id   UUID        NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    actor_id   UUID,
    event_type TEXT        NOT NULL,
    details    JSONB       NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX issue_history_issue ON issue_history (issue_id, created_at ASC);

CREATE TABLE events (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    event_id    TEXT,
    timestamp   TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    fingerprint TEXT,
    issue_id    UUID        REFERENCES issues(id) ON DELETE SET NULL,
    level       TEXT        GENERATED ALWAYS AS (payload->>'level') STORED,
    environment TEXT        GENERATED ALWAYS AS (payload->>'environment') STORED,
    release     TEXT        GENERATED ALWAYS AS (payload->>'release') STORED,
    platform    TEXT        GENERATED ALWAYS AS (payload->>'platform') STORED,
    trace_id    TEXT,
    span_id     TEXT,
    payload     JSONB       NOT NULL
);

CREATE INDEX events_project_timestamp  ON events (project_id, timestamp DESC);
CREATE INDEX events_project_received   ON events (project_id, received_at DESC);
CREATE UNIQUE INDEX events_project_event_id ON events (project_id, event_id) WHERE event_id IS NOT NULL;
CREATE INDEX events_ungrouped          ON events (received_at ASC) WHERE issue_id IS NULL;
CREATE INDEX events_issue_timestamp    ON events (issue_id, timestamp DESC);
CREATE INDEX events_project_release    ON events (project_id, release) WHERE release IS NOT NULL;
CREATE INDEX events_trace_id           ON events (trace_id) WHERE trace_id IS NOT NULL;

CREATE TABLE event_tags (
    event_id   UUID NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    issue_id   UUID REFERENCES issues(id) ON DELETE CASCADE,
    project_id UUID NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    PRIMARY KEY (event_id, key)
);

CREATE INDEX event_tags_issue_key ON event_tags (issue_id, key, value) WHERE issue_id IS NOT NULL;
CREATE INDEX event_tags_key_value ON event_tags (key, value, issue_id)  WHERE issue_id IS NOT NULL;

CREATE TABLE transactions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id      UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    trace_id        TEXT,
    span_id         TEXT,
    transaction     TEXT        NOT NULL,
    op              TEXT        NOT NULL DEFAULT '',
    status          TEXT        NOT NULL DEFAULT 'ok',
    duration_ms     INTEGER     NOT NULL,
    start_timestamp TIMESTAMPTZ NOT NULL,
    timestamp       TIMESTAMPTZ NOT NULL,
    received_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    environment     TEXT,
    release         TEXT,
    platform        TEXT,
    measurements    JSONB
);

CREATE INDEX transactions_project_start ON transactions (project_id, start_timestamp DESC, id DESC);
CREATE INDEX transactions_project_perf  ON transactions (project_id, start_timestamp DESC) INCLUDE (duration_ms);

CREATE TABLE spans (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    transaction_id  UUID        NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    span_id         TEXT        NOT NULL,
    parent_span_id  TEXT,
    op              TEXT        NOT NULL DEFAULT '',
    description     TEXT,
    start_timestamp TIMESTAMPTZ NOT NULL,
    timestamp       TIMESTAMPTZ NOT NULL,
    duration_ms     INTEGER     NOT NULL,
    status          TEXT        NOT NULL DEFAULT 'ok',
    data            JSONB
);

CREATE INDEX spans_transaction ON spans (transaction_id, start_timestamp ASC);
CREATE INDEX spans_op_start    ON spans (op text_pattern_ops, start_timestamp DESC);

CREATE TABLE perf_events (
    id             UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id       UUID        NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    transaction_id UUID        NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    span_count     INT         NOT NULL,
    total_ms       INT         NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX perf_events_issue ON perf_events (issue_id, created_at DESC);

CREATE TABLE releases (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    version     TEXT        NOT NULL,
    deployed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, version)
);

CREATE INDEX releases_project_deployed ON releases (project_id, deployed_at DESC);

CREATE TABLE api_tokens (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    token_hash   TEXT        NOT NULL UNIQUE,
    writable     BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '90 days'
);

CREATE INDEX api_tokens_project ON api_tokens (project_id, created_at DESC);

CREATE TABLE sourcemaps (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id   UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    release      TEXT        NOT NULL,
    url          TEXT        NOT NULL,
    content_hash TEXT        NOT NULL,
    size_bytes   INTEGER     NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (project_id, release, url)
);

CREATE INDEX sourcemaps_project_release ON sourcemaps (project_id, release);

CREATE TABLE alert_rules (
    id                 UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name               TEXT        NOT NULL,
    enabled            BOOLEAN     NOT NULL DEFAULT TRUE,
    trigger            TEXT        NOT NULL CHECK (trigger IN ('new_issue', 'regressed', 'new_or_regressed', 'event_count', 'cron_missed', 'cron_error')),
    threshold          INTEGER,
    window_mins        INTEGER,
    channel            TEXT        NOT NULL CHECK (channel IN ('webhook', 'slack', 'discord', 'email')),
    webhook_url        TEXT,
    email_to           TEXT,
    cooldown_mins      INTEGER     NOT NULL DEFAULT 60,
    last_fired_at      TIMESTAMPTZ,
    filter_level       TEXT,
    filter_environment TEXT,
    min_occurrences    INT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX alert_rules_enabled ON alert_rules (enabled, last_fired_at) WHERE enabled = TRUE;

-- Junction table: one rule can apply to multiple projects.
-- Rules with no rows here are global (fire for all projects).
CREATE TABLE alert_rule_projects (
    rule_id    UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    project_id UUID NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    PRIMARY KEY (rule_id, project_id)
);

CREATE INDEX alert_rule_projects_project ON alert_rule_projects (project_id);

-- no FKs on actor/project/target - rows must survive deletion of the things they describe
CREATE TABLE audit_log (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type TEXT        NOT NULL,
    actor_id   UUID,
    project_id UUID,
    target_id  TEXT,
    ip         TEXT,
    details    JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX audit_log_created ON audit_log (created_at DESC);
CREATE INDEX audit_log_actor   ON audit_log (actor_id,   created_at DESC) WHERE actor_id   IS NOT NULL;
CREATE INDEX audit_log_project ON audit_log (project_id, created_at DESC) WHERE project_id IS NOT NULL;

CREATE TABLE logs (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id  UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    timestamp   TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    level       TEXT        NOT NULL DEFAULT 'info',
    body        TEXT        NOT NULL,
    trace_id    TEXT,
    span_id     TEXT,
    environment TEXT,
    release     TEXT,
    attributes  JSONB       NOT NULL DEFAULT '{}'
);

CREATE INDEX logs_project_timestamp ON logs (project_id, timestamp DESC);
CREATE INDEX logs_project_level     ON logs (project_id, level, timestamp DESC);
CREATE INDEX logs_trace_id          ON logs (trace_id) WHERE trace_id IS NOT NULL;
CREATE INDEX logs_attributes        ON logs USING gin (attributes jsonb_path_ops);
CREATE INDEX logs_body_trgm         ON logs USING gin (body gin_trgm_ops);

CREATE TABLE cron_monitors (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id          UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name                TEXT        NOT NULL,
    schedule            TEXT        NOT NULL,
    grace_period_secs   INT         NOT NULL DEFAULT 300,
    status              TEXT        NOT NULL DEFAULT 'active'
                        CHECK (status IN ('active', 'paused')),
    is_running          BOOLEAN     NOT NULL DEFAULT FALSE,
    last_ok_at          TIMESTAMPTZ,
    next_expected_at    TIMESTAMPTZ,
    last_checkin_status TEXT,
    last_checkin_at     TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX cron_monitors_project ON cron_monitors (project_id, created_at DESC);
CREATE INDEX cron_monitors_overdue  ON cron_monitors (next_expected_at)
    WHERE status = 'active' AND next_expected_at IS NOT NULL;

CREATE TABLE cron_checkins (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    monitor_id  UUID        NOT NULL REFERENCES cron_monitors(id) ON DELETE CASCADE,
    status      TEXT        NOT NULL CHECK (status IN ('in_progress', 'ok', 'error')),
    duration_ms INTEGER,
    environment TEXT,
    started_at  TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    received_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX cron_checkins_monitor ON cron_checkins (monitor_id, received_at DESC);
