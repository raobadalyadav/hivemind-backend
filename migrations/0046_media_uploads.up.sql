-- Every uploaded photo/video is a row here; content lives in object storage
-- (MinIO/S3) under object_key. Attachment tables reference it by media_id and
-- keep a denormalised url so existing read paths are unchanged.
CREATE TABLE media_uploads (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind        text NOT NULL CHECK (kind IN ('image','video')),
    content_type text NOT NULL,
    size_bytes  bigint NOT NULL CHECK (size_bytes > 0),
    width       integer NOT NULL DEFAULT 0,
    height      integer NOT NULL DEFAULT 0,
    duration_ms integer NOT NULL DEFAULT 0,
    object_key  text NOT NULL UNIQUE,
    thumb_key   text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX media_uploads_owner_idx ON media_uploads (owner_id, created_at DESC);

ALTER TABLE stories
    ADD COLUMN media_id     uuid REFERENCES media_uploads(id),
    ADD COLUMN thumb_url    text NOT NULL DEFAULT '',
    ADD COLUMN width        integer NOT NULL DEFAULT 0,
    ADD COLUMN height       integer NOT NULL DEFAULT 0,
    ADD COLUMN duration_ms  integer NOT NULL DEFAULT 0,
    ADD COLUMN edits        jsonb NOT NULL DEFAULT '{}';

ALTER TABLE post_media
    ADD COLUMN media_id     uuid REFERENCES media_uploads(id),
    ADD COLUMN thumb_url    text NOT NULL DEFAULT '',
    ADD COLUMN width        integer NOT NULL DEFAULT 0,
    ADD COLUMN height       integer NOT NULL DEFAULT 0,
    ADD COLUMN duration_ms  integer NOT NULL DEFAULT 0;

ALTER TABLE message_media
    ADD COLUMN media_id     uuid REFERENCES media_uploads(id),
    ADD COLUMN position     integer NOT NULL DEFAULT 0,
    ADD COLUMN kind         text NOT NULL DEFAULT 'image' CHECK (kind IN ('image','video')),
    ADD COLUMN thumb_url    text NOT NULL DEFAULT '',
    ADD COLUMN width        integer NOT NULL DEFAULT 0,
    ADD COLUMN height       integer NOT NULL DEFAULT 0,
    ADD COLUMN duration_ms  integer NOT NULL DEFAULT 0;

ALTER TABLE profile_photos
    ADD COLUMN media_id     uuid REFERENCES media_uploads(id),
    ADD COLUMN thumb_url    text NOT NULL DEFAULT '',
    ADD COLUMN width        integer NOT NULL DEFAULT 0,
    ADD COLUMN height       integer NOT NULL DEFAULT 0;

ALTER TABLE plans
    ADD COLUMN cover_media_id  uuid REFERENCES media_uploads(id),
    ADD COLUMN cover_url       text NOT NULL DEFAULT '',
    ADD COLUMN cover_thumb_url text NOT NULL DEFAULT '';

-- The GC job looks up "is this upload still referenced?" per table.
CREATE INDEX stories_media_idx ON stories (media_id) WHERE media_id IS NOT NULL;
CREATE INDEX post_media_media_idx ON post_media (media_id) WHERE media_id IS NOT NULL;
CREATE INDEX message_media_media_idx ON message_media (media_id) WHERE media_id IS NOT NULL;
CREATE INDEX profile_photos_media_idx ON profile_photos (media_id) WHERE media_id IS NOT NULL;
CREATE INDEX plans_cover_media_idx ON plans (cover_media_id) WHERE cover_media_id IS NOT NULL;
