-- flow.md §2.1: registration is Google (Android) / Apple (Apple+Google) only
-- — no email+password signup. This replaces the password-based auth built
-- during the Phase 1 pass with OAuth identity linking + a verified
-- recovery-email path for when a user loses access to their OAuth provider.

CREATE TYPE oauth_provider AS ENUM ('google', 'apple');

CREATE TABLE oauth_identities (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider oauth_provider NOT NULL,
    provider_user_id TEXT NOT NULL,
    email TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_user_id)
);

CREATE INDEX oauth_identities_user_id_idx ON oauth_identities (user_id);

ALTER TABLE users DROP COLUMN password_hash;
ALTER TABLE users ADD COLUMN recovery_email TEXT UNIQUE;
ALTER TABLE users ADD COLUMN recovery_email_verified_at TIMESTAMPTZ;

-- Superseded by recovery_codes below — password_reset_tokens assumed a
-- password existed to reset, which is no longer true.
DROP TABLE IF EXISTS password_reset_tokens;

-- Backs both "verify a newly added recovery email" and "recover account
-- access via that email" — same shape as refresh_tokens (hash + expiry +
-- single-use via used_at).
CREATE TABLE recovery_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash TEXT NOT NULL UNIQUE,
    purpose TEXT NOT NULL CHECK (purpose IN ('verify_recovery_email', 'account_recovery')),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX recovery_codes_user_id_idx ON recovery_codes (user_id) WHERE used_at IS NULL;
