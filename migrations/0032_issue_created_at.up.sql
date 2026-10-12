-- New-issue alerts compared the SDK's own first_seen timestamp with the
-- server-side last_fired_at, so issues from clients with slow clocks, or
-- grouped while an alert was being delivered, were never alerted on. Record
-- when Tindra created each issue. Existing issues use first_seen so the
-- upgrade does not re-alert on all of them.
ALTER TABLE issues ADD COLUMN created_at TIMESTAMPTZ;
UPDATE issues SET created_at = first_seen;
ALTER TABLE issues
    ALTER COLUMN created_at SET DEFAULT now(),
    ALTER COLUMN created_at SET NOT NULL;

-- tindra:next-batch
CREATE INDEX CONCURRENTLY issues_created_at ON issues (created_at);
