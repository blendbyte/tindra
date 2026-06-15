CREATE TABLE alert_firings (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_id       UUID        NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    fired_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    trigger       TEXT        NOT NULL,
    channel       TEXT        NOT NULL,
    status        TEXT        NOT NULL CHECK (status IN ('pending', 'success', 'failed')),
    status_code   INTEGER,
    error         TEXT,
    item_count    INTEGER,
    attempt       INTEGER     NOT NULL DEFAULT 1,
    next_retry_at TIMESTAMPTZ,
    payload       JSONB
);

CREATE INDEX alert_firings_rule_id ON alert_firings (rule_id, fired_at DESC);
CREATE INDEX alert_firings_retry   ON alert_firings (next_retry_at)
    WHERE status = 'pending' AND next_retry_at IS NOT NULL;
