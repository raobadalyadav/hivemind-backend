-- Meet: swipes. A 'wave' is "I'd like to be friends"; 'super' is a wave that
-- is shown first to the target. A mutual wave/super is a match.
CREATE TABLE swipes (
    actor_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action     text NOT NULL CHECK (action IN ('pass','wave','super')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (actor_id, target_id),
    CHECK (actor_id <> target_id)
);
CREATE INDEX swipes_incoming_idx ON swipes (target_id, created_at DESC) WHERE action IN ('wave','super');
CREATE INDEX swipes_actor_time_idx ON swipes (actor_id, created_at DESC);

-- Boost: for its duration the profile is shown first in other people's decks.
CREATE TABLE profile_boosts (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    starts_at  timestamptz NOT NULL,
    ends_at    timestamptz NOT NULL CHECK (ends_at > starts_at),
    cost_minor bigint NOT NULL DEFAULT 0 CHECK (cost_minor >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX profile_boosts_user_idx ON profile_boosts (user_id, ends_at DESC);

-- Live-selfie verification (blue tick). issued → pending (selfie submitted) →
-- approved | rejected, or expired if the challenge lapses. The selfie is
-- detached (media_id NULL) once reviewed so the media GC removes it.
CREATE TABLE verification_requests (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    challenge     text NOT NULL,
    status        text NOT NULL DEFAULT 'issued' CHECK (status IN ('issued','pending','approved','rejected','expired')),
    media_id      uuid REFERENCES media_uploads(id) ON DELETE SET NULL,
    expires_at    timestamptz NOT NULL,
    reject_reason text NOT NULL DEFAULT '',
    reviewed_by   uuid REFERENCES users(id),
    reviewed_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX verification_open_uidx ON verification_requests (user_id) WHERE status IN ('issued','pending');
CREATE INDEX verification_pending_idx ON verification_requests (created_at) WHERE status = 'pending';
ALTER TABLE user_profiles ADD COLUMN selfie_verified_at timestamptz;

-- Chat inbox: rooms know what kind they are; members track what they've read.
ALTER TABLE chat_rooms
    ADD COLUMN kind   text NOT NULL DEFAULT 'group' CHECK (kind IN ('plan','dm','group')),
    ADD COLUMN dm_key text UNIQUE; -- "<lower uuid>:<higher uuid>" — one DM room per pair
UPDATE chat_rooms SET kind = 'plan' WHERE plan_id IS NOT NULL;
ALTER TABLE chat_members ADD COLUMN last_read_at timestamptz NOT NULL DEFAULT now();
CREATE INDEX chat_members_user_idx ON chat_members (user_id);

-- Plan categories people actually browse by (flow.md §8).
INSERT INTO categories (name, slug)
SELECT n, lower(n) FROM unnest(ARRAY['Drinks','Dinner','Brunch','Walk','Party','Workshop']) AS n
ON CONFLICT DO NOTHING;
