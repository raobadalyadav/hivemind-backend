ALTER TABLE cities DROP COLUMN active;
ALTER TABLE cities ADD COLUMN active BOOLEAN NOT NULL DEFAULT true;
UPDATE cities SET active = (status IN ('active', 'scaling', 'mature'));

ALTER TABLE cities DROP COLUMN status;
DROP TYPE city_status;
