-- Wishlist: a user can save (heart) plans. Mirrors post_saves.
CREATE TABLE plan_saves (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id    UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, plan_id)
);
CREATE INDEX plan_saves_user_idx ON plan_saves (user_id, created_at DESC);

-- Host rating = average over all reviews of a host's plans (plans -> reviews join).
CREATE INDEX IF NOT EXISTS reviews_plan_idx ON reviews (plan_id);
