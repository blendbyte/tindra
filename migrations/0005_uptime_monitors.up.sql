CREATE TABLE uptime_monitors (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id           UUID        NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name                 TEXT        NOT NULL,
    url                  TEXT        NOT NULL,
    method               TEXT        NOT NULL DEFAULT 'GET'
                         CHECK (method IN ('GET', 'HEAD')),
    interval_secs        INT         NOT NULL DEFAULT 300,
    timeout_secs         INT         NOT NULL DEFAULT 10,
    expected_codes       TEXT        NOT NULL DEFAULT '200-299',
    body_contains        TEXT,
    status               TEXT        NOT NULL DEFAULT 'active'
                         CHECK (status IN ('active', 'paused')),
    state                TEXT        NOT NULL DEFAULT 'unknown'
                         CHECK (state IN ('unknown', 'up', 'down')),
    consecutive_failures INT         NOT NULL DEFAULT 0,
    last_checked_at      TIMESTAMPTZ,
    last_ok_at           TIMESTAMPTZ,
    next_check_at        TIMESTAMPTZ,
    last_status_code     INT,
    last_response_ms     INT,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX uptime_monitors_project ON uptime_monitors (project_id, created_at DESC);
CREATE INDEX uptime_monitors_due ON uptime_monitors (next_check_at)
    WHERE status = 'active';

CREATE TABLE uptime_checks (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    monitor_id  UUID        NOT NULL REFERENCES uptime_monitors(id) ON DELETE CASCADE,
    status      TEXT        NOT NULL CHECK (status IN ('up', 'down')),
    status_code INT,
    response_ms INT,
    error       TEXT,
    checked_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX uptime_checks_monitor ON uptime_checks (monitor_id, checked_at DESC);

-- Extend the alert trigger enum to include uptime alerts.
ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_trigger_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_trigger_check
    CHECK (trigger IN (
        'new_issue', 'regressed', 'new_or_regressed', 'event_count',
        'cron_missed', 'cron_error',
        'uptime_down', 'uptime_recovered'
    ));
