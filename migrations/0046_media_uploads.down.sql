ALTER TABLE plans DROP COLUMN cover_thumb_url, DROP COLUMN cover_url, DROP COLUMN cover_media_id;
ALTER TABLE profile_photos DROP COLUMN height, DROP COLUMN width, DROP COLUMN thumb_url, DROP COLUMN media_id;
ALTER TABLE message_media DROP COLUMN duration_ms, DROP COLUMN height, DROP COLUMN width, DROP COLUMN thumb_url, DROP COLUMN kind, DROP COLUMN position, DROP COLUMN media_id;
ALTER TABLE post_media DROP COLUMN duration_ms, DROP COLUMN height, DROP COLUMN width, DROP COLUMN thumb_url, DROP COLUMN media_id;
ALTER TABLE stories DROP COLUMN edits, DROP COLUMN duration_ms, DROP COLUMN height, DROP COLUMN width, DROP COLUMN thumb_url, DROP COLUMN media_id;
DROP TABLE media_uploads;
