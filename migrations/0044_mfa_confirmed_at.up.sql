-- When the current authenticator was confirmed. A code alone only proves who
-- holds a session if the authenticator was enrolled before that session began.
-- Existing authenticators predate every session that could matter, so NULL.
ALTER TABLE users ADD COLUMN mfa_confirmed_at TIMESTAMPTZ;
