ALTER TABLE notifications DROP COLUMN delivery_error;
ALTER TABLE notifications DROP COLUMN sent_at;
ALTER TABLE notifications DROP COLUMN delivery_status;
DROP TYPE delivery_status;
