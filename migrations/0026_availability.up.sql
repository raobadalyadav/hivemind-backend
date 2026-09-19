-- Who's Free / Activity Buddy (Phase 4): one mechanism for both — a user
-- broadcasts a time-boxed availability window + activity type, another
-- user with an overlapping window can be matched, mutual accept creates an
-- ephemeral chat room. activity_type is free text so it covers both a
-- generic "free tonight" broadcast and a specific "badminton tonight" request.
CREATE TYPE availability_status AS ENUM ('open', 'matched', 'cancelled', 'expired');

-- Ad-hoc (plan-less) chat rooms need plan_id to be nullable. UNIQUE(plan_id)
-- still holds — Postgres allows multiple NULLs — and every existing
-- plan-scoped ON CONFLICT (plan_id) upsert always supplies a non-null
-- value, so this is purely additive for the new ad-hoc room path.
ALTER TABLE chat_rooms ALTER COLUMN plan_id DROP NOT NULL;

CREATE TABLE availability_windows (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    activity_type TEXT NOT NULL,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    status availability_status NOT NULL DEFAULT 'open',
    matched_with_user_id UUID REFERENCES users(id),
    chat_room_id UUID REFERENCES chat_rooms(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT availability_window_valid CHECK (ends_at > starts_at)
);

CREATE INDEX availability_windows_open_idx ON availability_windows (activity_type, starts_at) WHERE status = 'open';
