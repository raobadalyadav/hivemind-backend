-- Social notifications: typed, actor-attributed, de-duplicated; plus the
-- story-view / story-like / profile-view records that produce them.
ALTER TABLE notifications
    ADD COLUMN type       TEXT,
    ADD COLUMN actor_id   UUID REFERENCES users(id) ON DELETE CASCADE,
    ADD COLUMN target_id  TEXT,
    ADD COLUMN dedupe_key TEXT;

-- While a notification with this key is still unread, a repeat of the same
-- event (another chat message, a re-view) collapses into it.
CREATE UNIQUE INDEX notifications_dedupe_idx ON notifications (user_id, dedupe_key)
    WHERE dedupe_key IS NOT NULL AND NOT read;
CREATE INDEX notifications_unread_idx ON notifications (user_id, created_at DESC) WHERE NOT read;

ALTER TABLE notification_preferences
    ADD COLUMN muted_categories TEXT[] NOT NULL DEFAULT '{}';

CREATE TABLE story_views (
    story_id  UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    viewer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    viewed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (story_id, viewer_id)
);

CREATE TABLE story_likes (
    story_id   UUID NOT NULL REFERENCES stories(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (story_id, user_id)
);

-- One row per viewer, target and day: the throttle for "X viewed your profile".
CREATE TABLE profile_views (
    viewer_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    viewed_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    day       DATE NOT NULL DEFAULT CURRENT_DATE,
    PRIMARY KEY (viewer_id, viewed_id, day)
);
CREATE INDEX profile_views_viewed_idx ON profile_views (viewed_id, day DESC);

ALTER TABLE user_profiles ADD COLUMN hide_profile_views BOOLEAN NOT NULL DEFAULT false;
