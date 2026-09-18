CREATE TYPE user_role AS ENUM ('user','host','moderator','admin','super_admin');
ALTER TABLE users ADD COLUMN role user_role NOT NULL DEFAULT 'user';

-- Real session/device management per PRD §13.1, replacing the scaffold's
-- placeholder of minting two same-shaped JWTs with no way to revoke either.
CREATE TABLE refresh_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id UUID REFERENCES devices(id) ON DELETE SET NULL,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id) WHERE revoked_at IS NULL;
