-- Environment menus read distinct values without fetching telemetry payloads.
CREATE INDEX CONCURRENTLY events_project_environment ON events (project_id, environment) WHERE environment IS NOT NULL AND environment <> '';

-- tindra:next-batch

CREATE INDEX CONCURRENTLY transactions_project_environment ON transactions (project_id, environment) WHERE environment IS NOT NULL AND environment <> '';

-- tindra:next-batch

CREATE INDEX CONCURRENTLY logs_project_environment ON logs (project_id, environment) WHERE environment IS NOT NULL AND environment <> '';
