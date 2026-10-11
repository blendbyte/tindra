-- Deleting a user used to delete every comment they wrote. Keep the comments
-- and detach the author, as issue_history and audit_log already do.
ALTER TABLE issue_comments ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE issue_comments DROP CONSTRAINT issue_comments_user_id_fkey;
ALTER TABLE issue_comments
    ADD CONSTRAINT issue_comments_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL;
