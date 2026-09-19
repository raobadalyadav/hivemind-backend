-- Any legacy value outside the new set becomes private (fail closed): the old
-- access check treated everything that wasn't 'private' as public.
UPDATE posts SET visibility = 'private' WHERE visibility NOT IN ('private','public');

ALTER TABLE posts ADD COLUMN community_id uuid REFERENCES communities(id) ON DELETE CASCADE;
ALTER TABLE posts ADD CONSTRAINT posts_visibility_chk
    CHECK (visibility IN ('private','public','connections','community')
           AND ((visibility = 'community') = (community_id IS NOT NULL)));

ALTER TABLE post_media ADD COLUMN media_type text NOT NULL DEFAULT 'image'
    CHECK (media_type IN ('image','video'));

CREATE TABLE post_saves (
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    post_id    uuid NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, post_id)
);

CREATE INDEX posts_feed_idx ON posts (created_at DESC, id DESC);
