-- Community types (flow.md §22): public / private (invite only) / approval /
-- paid (entitlement-gated — records gating only, no billing here).
CREATE TYPE community_type AS ENUM ('public', 'private', 'approval', 'paid');

ALTER TABLE communities
    ADD COLUMN membership_type community_type NOT NULL DEFAULT 'public',
    ADD COLUMN cover_image_url TEXT NOT NULL DEFAULT '',
    ADD COLUMN rules TEXT NOT NULL DEFAULT '',
    ADD COLUMN category_id UUID REFERENCES categories(id),
    ADD COLUMN required_entitlement TEXT;

ALTER TABLE communities
    ADD CONSTRAINT communities_paid_needs_entitlement
    CHECK (membership_type <> 'paid' OR required_entitlement IS NOT NULL);

-- Pending join requests (approval communities) and invites (private ones,
-- invited_by set + status 'approved').
CREATE TABLE community_join_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    community_id UUID NOT NULL REFERENCES communities(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status join_request_status NOT NULL DEFAULT 'pending',
    invited_by UUID REFERENCES users(id),
    decided_by UUID REFERENCES users(id),
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (community_id, user_id)
);
CREATE INDEX community_join_requests_status_idx ON community_join_requests (community_id, status);
