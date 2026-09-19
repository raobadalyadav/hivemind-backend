-- The visibility data-fix in the up migration is not reversible (the old
-- values were unrecognised anyway).
DROP INDEX posts_feed_idx;
DROP TABLE post_saves;
ALTER TABLE post_media DROP COLUMN media_type;
ALTER TABLE posts DROP CONSTRAINT posts_visibility_chk;
ALTER TABLE posts DROP COLUMN community_id;
