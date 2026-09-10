ALTER TABLE users ADD COLUMN mfa_pending_secret TEXT;
ALTER TABLE users ADD COLUMN mfa_pending_expires_at TIMESTAMPTZ;

-- Unconfirmed secrets from the old setup flow are not active factors.
UPDATE users SET mfa_secret = NULL WHERE NOT mfa_enabled;
