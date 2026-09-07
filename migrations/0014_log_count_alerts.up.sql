ALTER TABLE alert_rules
    ADD COLUMN filter_search TEXT;

ALTER TABLE alert_rules DROP CONSTRAINT IF EXISTS alert_rules_trigger_check;
ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_trigger_check
    CHECK (trigger IN (
        'new_issue', 'regressed', 'new_or_regressed', 'event_count', 'log_count',
        'cron_missed', 'cron_error',
        'uptime_down', 'uptime_recovered'
    ));
