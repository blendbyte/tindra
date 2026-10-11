-- Sentry SDKs identify cron monitors by slug (monitor_slug="nightly-backup").
ALTER TABLE cron_monitors ADD COLUMN slug TEXT;

-- Backfill from the name: lower-case, every run of other characters becomes a
-- dash. A slug already used in the same project gets a short ID suffix.
WITH base AS (
    SELECT id, project_id, created_at,
           COALESCE(NULLIF(trim(BOTH '-' FROM left(regexp_replace(lower(name), '[^a-z0-9]+', '-', 'g'), 40)), ''), 'monitor') AS slug
    FROM cron_monitors
), numbered AS (
    SELECT id, slug, row_number() OVER (PARTITION BY project_id, slug ORDER BY created_at, id) AS n
    FROM base
)
UPDATE cron_monitors m
SET slug = CASE WHEN numbered.n = 1 THEN numbered.slug ELSE numbered.slug || '-' || left(m.id::text, 8) END
FROM numbered
WHERE numbered.id = m.id;

-- The app always sets a slug derived from the name. The default only keeps
-- direct inserts (seed scripts, manual SQL) from failing the constraint.
ALTER TABLE cron_monitors
    ALTER COLUMN slug SET DEFAULT 'monitor-' || left(replace(gen_random_uuid()::text, '-', ''), 12),
    ALTER COLUMN slug SET NOT NULL;
CREATE UNIQUE INDEX cron_monitors_project_slug ON cron_monitors (project_id, slug);
