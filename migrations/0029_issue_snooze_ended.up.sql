-- When a time-based snooze ends, the issue reopens quietly and remembers it.
-- If the error then recurs, it is marked regressed so regression alerts fire.
ALTER TABLE issues ADD COLUMN snooze_ended_at TIMESTAMPTZ;
