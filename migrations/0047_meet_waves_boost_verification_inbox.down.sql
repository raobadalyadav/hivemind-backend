DELETE FROM categories c WHERE c.name IN ('Drinks','Dinner','Brunch','Walk','Party','Workshop')
  AND NOT EXISTS (SELECT 1 FROM plans p WHERE p.category_id = c.id)
  AND NOT EXISTS (SELECT 1 FROM communities m WHERE m.category_id = c.id);
DROP INDEX chat_members_user_idx;
ALTER TABLE chat_members DROP COLUMN last_read_at;
ALTER TABLE chat_rooms DROP COLUMN dm_key, DROP COLUMN kind;
ALTER TABLE user_profiles DROP COLUMN selfie_verified_at;
DROP TABLE verification_requests;
DROP TABLE profile_boosts;
DROP TABLE swipes;
