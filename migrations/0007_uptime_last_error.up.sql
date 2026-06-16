ALTER TABLE uptime_monitors
    ADD COLUMN last_error    TEXT,
    ADD COLUMN went_down_at  TIMESTAMPTZ;
