-- Re-confirming a password or two-factor code before a sensitive account
-- change counts against the session that asks. Too many wrong answers end the
-- session, so a stolen session cookie cannot keep guessing, and the owner's
-- own sign-in is never locked by it.
ALTER TABLE sessions ADD COLUMN reauth_attempts INT NOT NULL DEFAULT 0;
