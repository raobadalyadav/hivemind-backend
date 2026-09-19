CREATE TYPE story_audience AS ENUM ('connections','community');

CREATE TABLE stories (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    author_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    media_url    text NOT NULL CHECK (char_length(media_url) <= 2048),
    media_type   text NOT NULL DEFAULT 'image' CHECK (media_type IN ('image','video')),
    caption      text NOT NULL DEFAULT '' CHECK (char_length(caption) <= 500),
    audience     story_audience NOT NULL,
    community_id uuid REFERENCES communities(id) ON DELETE CASCADE,
    keep_archive boolean NOT NULL DEFAULT false,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    CHECK ((audience = 'community') = (community_id IS NOT NULL)),
    CHECK (expires_at > created_at)
);
CREATE INDEX stories_author_idx ON stories (author_id, created_at DESC);
CREATE INDEX stories_expiry_idx ON stories (expires_at) WHERE NOT keep_archive;
