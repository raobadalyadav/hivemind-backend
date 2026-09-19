CREATE TABLE referral_codes (
    user_id    uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    code       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- referee_id UNIQUE = a user can be referred at most once; that constraint is
-- what makes the reward claim idempotent.
CREATE TABLE referrals (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    referrer_id  uuid NOT NULL REFERENCES users(id),
    referee_id   uuid NOT NULL UNIQUE REFERENCES users(id),
    code         text NOT NULL,
    reward_minor bigint NOT NULL CHECK (reward_minor >= 0),
    created_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (referrer_id <> referee_id)
);
CREATE INDEX referrals_referrer_idx ON referrals (referrer_id);
