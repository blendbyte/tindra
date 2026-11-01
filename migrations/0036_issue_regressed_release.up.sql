-- The release an issue regressed in. Release pages count an issue as brought
-- back only by this release, not by every older release it was seen in.
ALTER TABLE issues ADD COLUMN IF NOT EXISTS regressed_release TEXT;

-- Backfill issues that are regressed now with the release of their latest
-- event at or before the regression. Events are grouped after they arrive, so
-- the event that brought the issue back is the last one before regressed_at.
UPDATE issues i
SET regressed_release = (
    SELECT e.release FROM events e
    WHERE e.issue_id = i.id
      AND e.timestamp <= COALESCE(i.regressed_at, 'infinity'::timestamptz)
    ORDER BY e.timestamp DESC
    LIMIT 1
)
WHERE i.status = 'regressed' AND i.regressed_release IS NULL;

CREATE INDEX IF NOT EXISTS issues_project_regressed_release ON issues (project_id, regressed_release)
    WHERE status = 'regressed';
