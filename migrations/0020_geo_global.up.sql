-- Location functionality (flow.md §2.3/§9) and per-city timezone (so
-- TODAY/TONIGHT/WEEKEND discovery is correct for a city in any timezone,
-- not just server-local).
ALTER TABLE cities ADD COLUMN centroid GEOGRAPHY(Point, 4326);
ALTER TABLE cities ADD COLUMN timezone TEXT NOT NULL DEFAULT 'UTC';

CREATE INDEX cities_centroid_gix ON cities USING GIST (centroid);

ALTER TABLE users ADD COLUMN last_location GEOGRAPHY(Point, 4326);
ALTER TABLE users ADD COLUMN last_location_at TIMESTAMPTZ;
