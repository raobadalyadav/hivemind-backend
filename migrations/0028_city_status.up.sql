-- City launch workflow (Phase 5, flow.md §55): a real lifecycle status
-- instead of a boolean. active becomes a derived column so
-- internal/discovery.Repository.DetectCity's existing `WHERE active` query
-- — the only place anywhere that reads it — keeps working unchanged.
CREATE TYPE city_status AS ENUM ('pre_launch', 'soft_launch', 'active', 'scaling', 'mature');

ALTER TABLE cities ADD COLUMN status city_status;
UPDATE cities SET status = CASE WHEN active THEN 'active'::city_status ELSE 'pre_launch'::city_status END;
ALTER TABLE cities ALTER COLUMN status SET NOT NULL;
ALTER TABLE cities ALTER COLUMN status SET DEFAULT 'pre_launch';

ALTER TABLE cities DROP COLUMN active;
ALTER TABLE cities ADD COLUMN active BOOLEAN GENERATED ALWAYS AS
    (status IN ('active', 'scaling', 'mature')) STORED;
