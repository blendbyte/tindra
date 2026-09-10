ALTER TABLE cron_checkins ADD COLUMN sdk_checkin_id TEXT;
CREATE UNIQUE INDEX cron_checkins_sdk_id ON cron_checkins (monitor_id, sdk_checkin_id);
