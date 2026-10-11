-- Bound TOTP guessing during MFA login. Each challenge allows a few wrong codes
-- before it is discarded, and repeated failures lock MFA verification for the
-- user regardless of how many IPs or challenges the attacker spreads them over.
ALTER TABLE mfa_challenges ADD COLUMN failed_attempts INT NOT NULL DEFAULT 0;

ALTER TABLE users
    ADD COLUMN mfa_failed_attempts INT NOT NULL DEFAULT 0,
    ADD COLUMN mfa_locked_until    TIMESTAMPTZ;
