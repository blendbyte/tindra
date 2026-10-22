-- Source map URLs are now normalized like frame URLs from every runtime:
-- custom schemes (app:///x.js) and bare relative paths (x.js) both become
-- ~/x.js. Rewrite URLs stored under the old rules so their maps keep matching.
-- Mirrors sourcemaps.NormalizeURL. When several old URLs, or an old and a
-- current one, map to the same URL in a release, the newest upload wins and the
-- rest are left as they are (they can no longer match a frame).
WITH renamed AS (
    SELECT id, project_id, release, created_at,
           '~' || CASE WHEN rest LIKE '/%' THEN rest ELSE '/' || rest END AS new_url
    FROM (
        SELECT id, project_id, release, created_at,
               regexp_replace(regexp_replace(url, '^~', ''), '^[a-zA-Z][a-zA-Z0-9+.-]*://[^/]*', '') AS rest
        FROM sourcemaps
        WHERE url NOT LIKE '~/%'
    ) r
), winners AS (
    SELECT DISTINCT ON (project_id, release, new_url) id, project_id, release, new_url
    FROM renamed
    ORDER BY project_id, release, new_url, created_at DESC, id
)
UPDATE sourcemaps s
SET url = w.new_url
FROM winners w
WHERE s.id = w.id
  AND NOT EXISTS (
      SELECT 1 FROM sourcemaps o
      WHERE o.project_id = w.project_id AND o.release = w.release AND o.url = w.new_url
  );
