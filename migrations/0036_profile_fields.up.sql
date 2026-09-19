ALTER TABLE user_profiles
    ADD COLUMN gender    text CHECK (gender IN ('male','female','non_binary','prefer_not_to_say')),
    ADD COLUMN education text NOT NULL DEFAULT '',
    ADD COLUMN hobbies   text[] NOT NULL DEFAULT '{}';

CREATE TABLE profile_photos (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    url        text NOT NULL CHECK (char_length(url) <= 2048),
    position   integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX profile_photos_user_idx ON profile_photos (user_id, position, created_at);
