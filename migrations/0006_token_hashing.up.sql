-- P1-1: hash short-lived tokens at rest.
-- Session and API tokens already use SHA-256 hashes. This migration aligns
-- password_reset_tokens, mfa_challenges, oauth_states, and user_invites to
-- the same pattern. Existing rows contain plaintext tokens that cannot be
-- validated by the new code; they will expire naturally.

-- password_reset_tokens
ALTER TABLE password_reset_tokens RENAME COLUMN token TO token_hash;

-- mfa_challenges
ALTER TABLE mfa_challenges RENAME COLUMN token TO token_hash;

-- oauth_states
ALTER TABLE oauth_states RENAME COLUMN token TO token_hash;

-- user_invites: rename token column, add a stable UUID for admin operations
-- (the plaintext token is never returned after creation, so revoke must use a
-- separate non-secret identifier rather than the stored hash).
ALTER TABLE user_invites RENAME COLUMN token TO token_hash;
ALTER TABLE user_invites ADD COLUMN id UUID NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE user_invites DROP CONSTRAINT user_invites_pkey;
ALTER TABLE user_invites ADD PRIMARY KEY (id);
ALTER TABLE user_invites ADD CONSTRAINT user_invites_token_hash_key UNIQUE (token_hash);
