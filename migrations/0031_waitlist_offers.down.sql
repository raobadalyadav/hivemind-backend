DROP INDEX IF EXISTS waitlist_offers_idx;
DROP INDEX IF EXISTS waitlist_queue_idx;
ALTER TABLE waitlist_entries
    DROP COLUMN updated_at,
    DROP COLUMN offer_expires_at,
    DROP COLUMN offered_at,
    DROP COLUMN status;
DROP TYPE waitlist_status;
