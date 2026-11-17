-- Short issue IDs such as BACKEND-42: a per-project prefix and a per-project
-- issue number.

-- The prefix is taken from the slug when a project is created and then never
-- changes, so short IDs pasted into tickets and chat keep working after a
-- project is renamed. Every prefix ever handed out stays claimed here, also
-- after its project is deleted, so an old short ID never leads to an
-- unrelated issue.
CREATE TABLE issue_prefixes (
    prefix TEXT PRIMARY KEY
);

-- claim_issue_prefix upper-cases the slug, turns anything but letters and
-- digits into single dashes (slugs from the CLI are not validated), and
-- numbers it when taken (BACKEND2, BACKEND3, ...).
CREATE FUNCTION claim_issue_prefix(slug TEXT) RETURNS TEXT LANGUAGE plpgsql AS $$
DECLARE
    base      TEXT := COALESCE(NULLIF(trim(both '-' from regexp_replace(upper(slug), '[^A-Z0-9]+', '-', 'g')), ''), 'PROJECT');
    candidate TEXT := base;
    n         INT  := 1;
BEGIN
    LOOP
        INSERT INTO issue_prefixes (prefix) VALUES (candidate) ON CONFLICT DO NOTHING;
        IF FOUND THEN
            RETURN candidate;
        END IF;
        n := n + 1;
        candidate := base || n;
    END LOOP;
END $$;

-- Existing projects claim theirs oldest first, so the oldest of two slugs
-- that clean up the same keeps the plain prefix.
ALTER TABLE projects ADD COLUMN issue_prefix TEXT;
DO $$
DECLARE
    p RECORD;
BEGIN
    FOR p IN SELECT id, slug FROM projects ORDER BY created_at, id LOOP
        UPDATE projects SET issue_prefix = claim_issue_prefix(p.slug) WHERE id = p.id;
    END LOOP;
END $$;
ALTER TABLE projects ALTER COLUMN issue_prefix SET NOT NULL;
ALTER TABLE projects ADD CONSTRAINT projects_issue_prefix_key UNIQUE (issue_prefix);

CREATE FUNCTION projects_assign_issue_prefix() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.issue_prefix IS NULL THEN
        NEW.issue_prefix := claim_issue_prefix(NEW.slug);
    ELSE
        INSERT INTO issue_prefixes (prefix) VALUES (NEW.issue_prefix);
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER projects_issue_prefix BEFORE INSERT ON projects
    FOR EACH ROW EXECUTE FUNCTION projects_assign_issue_prefix();

-- The last issue number handed out per project. Numbers are taken in the
-- transaction that creates the issue, so a rolled-back issue gives its number
-- back. No transaction creates issues in two projects, so the row locks cannot
-- deadlock.
CREATE TABLE issue_counters (
    project_id  UUID   PRIMARY KEY REFERENCES projects(id) ON DELETE CASCADE,
    last_number BIGINT NOT NULL
);

-- Existing issues are numbered in the order they were first seen.
ALTER TABLE issues ADD COLUMN short_number BIGINT;
UPDATE issues i SET short_number = n.rn
FROM (
    SELECT id, row_number() OVER (PARTITION BY project_id ORDER BY first_seen, id) AS rn
    FROM issues
) n
WHERE i.id = n.id;
INSERT INTO issue_counters (project_id, last_number)
SELECT project_id, max(short_number) FROM issues GROUP BY project_id;
ALTER TABLE issues ALTER COLUMN short_number SET NOT NULL;
CREATE UNIQUE INDEX issues_project_short_number ON issues (project_id, short_number);

CREATE FUNCTION issues_assign_short_number() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.short_number IS NULL THEN
        INSERT INTO issue_counters AS c (project_id, last_number) VALUES (NEW.project_id, 1)
        ON CONFLICT (project_id) DO UPDATE SET last_number = c.last_number + 1
        RETURNING c.last_number INTO NEW.short_number;
    END IF;
    RETURN NEW;
END $$;

CREATE TRIGGER issues_short_number BEFORE INSERT ON issues
    FOR EACH ROW EXECUTE FUNCTION issues_assign_short_number();

-- A merged issue is deleted; its short ID keeps leading to the issue it was
-- merged into.
CREATE TABLE issue_short_aliases (
    project_id   UUID   NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    short_number BIGINT NOT NULL,
    issue_id     UUID   NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, short_number)
);
CREATE INDEX issue_short_aliases_issue ON issue_short_aliases (issue_id);
