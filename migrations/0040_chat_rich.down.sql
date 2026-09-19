DROP TABLE chat_poll_votes;
DROP TABLE chat_poll_options;
DROP TABLE chat_polls;
ALTER TABLE chat_rooms DROP COLUMN pinned_message_id;
ALTER TABLE messages DROP COLUMN meta, DROP COLUMN type;
DROP TYPE message_type;
