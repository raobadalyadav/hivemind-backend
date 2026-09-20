ALTER TABLE user_profiles DROP COLUMN IF EXISTS hide_profile_views;
DROP TABLE IF EXISTS profile_views;
DROP TABLE IF EXISTS story_likes;
DROP TABLE IF EXISTS story_views;
ALTER TABLE notification_preferences DROP COLUMN IF EXISTS muted_categories;
DROP INDEX IF EXISTS notifications_unread_idx;
DROP INDEX IF EXISTS notifications_dedupe_idx;
ALTER TABLE notifications DROP COLUMN IF EXISTS dedupe_key, DROP COLUMN IF EXISTS target_id, DROP COLUMN IF EXISTS actor_id, DROP COLUMN IF EXISTS type;
