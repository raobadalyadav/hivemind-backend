-- Schema per PRD §18 "Representative schema choices" for plans / plan_participants.
CREATE TABLE plans (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id UUID REFERENCES cities(id),
    host_id UUID NOT NULL REFERENCES users(id),
    venue_id UUID REFERENCES venues(id),
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    category_id UUID REFERENCES categories(id),
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    capacity INT NOT NULL,
    confirmed_count INT NOT NULL DEFAULT 0,
    price_minor BIGINT NOT NULL DEFAULT 0,
    currency CHAR(3) NOT NULL DEFAULT 'INR',
    status TEXT NOT NULL DEFAULT 'draft', -- draft|published|full|cancelled|completed
    location GEOGRAPHY(Point, 4326),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT capacity_nonnegative CHECK (capacity >= 0),
    CONSTRAINT confirmed_within_capacity CHECK (confirmed_count <= capacity)
);

CREATE INDEX plans_location_gix ON plans USING GIST (location);
CREATE INDEX plans_city_starts_at_idx ON plans (city_id, starts_at);

CREATE TABLE plan_media (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    media_url TEXT NOT NULL,
    position INT NOT NULL DEFAULT 0
);

CREATE TABLE plan_rules (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    rule_key TEXT NOT NULL,
    rule_value TEXT NOT NULL
);

CREATE TABLE plan_recurrences (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    recurrence_rule TEXT NOT NULL -- RFC 5545 RRULE string
);

-- Schema per PRD §18 representative choice, verbatim field set.
CREATE TABLE plan_participants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    booking_id UUID,
    status TEXT NOT NULL DEFAULT 'confirmed', -- confirmed|cancelled|waitlisted
    visibility TEXT NOT NULL DEFAULT 'visible',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    checked_in_at TIMESTAMPTZ,
    UNIQUE (plan_id, user_id)
);
