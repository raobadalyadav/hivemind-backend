ALTER TABLE users DROP COLUMN last_location_at;
ALTER TABLE users DROP COLUMN last_location;

DROP INDEX IF EXISTS cities_centroid_gix;
ALTER TABLE cities DROP COLUMN timezone;
ALTER TABLE cities DROP COLUMN centroid;
