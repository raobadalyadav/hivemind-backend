-- Plan types (flow.md §14): join mode (open / host approval / invite only),
-- visibility (public / private / community), premium gating via an
-- entitlement name, and series_id for recurring plans (migration 0035).
CREATE TYPE plan_join_mode AS ENUM ('open', 'approval', 'invite_only');
CREATE TYPE plan_visibility AS ENUM ('public', 'private', 'community');
CREATE TYPE join_request_status AS ENUM ('pending', 'approved', 'rejected', 'cancelled');

ALTER TABLE plans
    ADD COLUMN join_mode plan_join_mode NOT NULL DEFAULT 'open',
    ADD COLUMN visibility plan_visibility NOT NULL DEFAULT 'public',
    ADD COLUMN community_id UUID REFERENCES communities(id),
    ADD COLUMN requires_entitlement TEXT,
    ADD COLUMN series_id UUID REFERENCES plans(id);

ALTER TABLE plans
    ADD CONSTRAINT plans_community_vis CHECK (visibility <> 'community' OR community_id IS NOT NULL),
    ADD CONSTRAINT plans_invite_not_public CHECK (join_mode <> 'invite_only' OR visibility <> 'public');

CREATE UNIQUE INDEX plans_series_starts_uidx ON plans (series_id, starts_at);
CREATE UNIQUE INDEX community_events_uidx ON community_events (community_id, plan_id);

CREATE TABLE plan_join_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status join_request_status NOT NULL DEFAULT 'pending',
    message TEXT NOT NULL DEFAULT '',
    decided_by UUID REFERENCES users(id),
    decided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (plan_id, user_id)
);
CREATE INDEX plan_join_requests_plan_status_idx ON plan_join_requests (plan_id, status);

CREATE TABLE plan_invites (
    plan_id UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    invited_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (plan_id, user_id)
);

-- Every discovery/search/recommendation query reads this instead of raw
-- plans: only published PUBLIC plans, and only the next upcoming occurrence
-- of a recurring series. (Columns are fixed at creation — a future plans
-- column must be added to this view explicitly.)
CREATE VIEW plans_discoverable AS
SELECT p.* FROM plans p
WHERE p.status = 'published' AND p.visibility = 'public'
  AND (p.series_id IS NULL OR p.id = (
        SELECT s.id FROM plans s
        WHERE s.series_id = p.series_id AND s.status = 'published' AND s.starts_at > now()
        ORDER BY s.starts_at LIMIT 1));
