-- Issue user counts used to read and parse every event's payload on each
-- list load. Counting the generated user_identity column (id, username or
-- email, as the app-users page does) can use this index alone.
CREATE INDEX CONCURRENTLY events_issue_user_identity ON events (issue_id, user_identity)
    WHERE issue_id IS NOT NULL AND user_identity IS NOT NULL;
