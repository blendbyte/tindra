-- Web Push: browsers and installed PWAs register a push subscription per
-- device. Alert rules with channel 'push' notify the users listed in
-- alert_rule_recipients on every device they registered.

CREATE TABLE push_subscriptions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint        TEXT        NOT NULL UNIQUE,
    p256dh          TEXT        NOT NULL,
    auth            TEXT        NOT NULL,
    label           TEXT        NOT NULL DEFAULT '',
    failure_count   INT         NOT NULL DEFAULT 0,
    last_success_at TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX push_subscriptions_user ON push_subscriptions (user_id);

CREATE TABLE alert_rule_recipients (
    rule_id UUID NOT NULL REFERENCES alert_rules(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (rule_id, user_id)
);

CREATE INDEX alert_rule_recipients_user ON alert_rule_recipients (user_id);

-- Instance-wide secrets generated on first start, such as the VAPID key pair.
CREATE TABLE instance_secrets (
    key        TEXT        PRIMARY KEY,
    value      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
