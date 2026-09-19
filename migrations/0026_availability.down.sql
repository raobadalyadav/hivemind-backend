DROP INDEX IF EXISTS availability_windows_open_idx;
DROP TABLE availability_windows;
DROP TYPE availability_status;

-- Reverting plan_id to NOT NULL requires no ad-hoc rooms exist — true on a
-- clean down immediately after up, which is what reversibility testing does.
ALTER TABLE chat_rooms ALTER COLUMN plan_id SET NOT NULL;
