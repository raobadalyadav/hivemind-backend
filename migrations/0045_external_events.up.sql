CREATE TABLE external_events (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    city_id      uuid NOT NULL REFERENCES cities(id),
    category_id  uuid REFERENCES categories(id),
    title        text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    description  text NOT NULL DEFAULT '',
    source       text NOT NULL DEFAULT '' CHECK (char_length(source) <= 100),
    source_url   text NOT NULL CHECK (source_url ~ '^https?://' AND char_length(source_url) <= 2048),
    venue_name   text NOT NULL DEFAULT '',
    image_url    text NOT NULL DEFAULT '' CHECK (char_length(image_url) <= 2048),
    starts_at    timestamptz NOT NULL,
    ends_at      timestamptz,
    active       boolean NOT NULL DEFAULT true,
    created_by   uuid REFERENCES users(id),
    chat_room_id uuid REFERENCES chat_rooms(id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_at IS NULL OR ends_at > starts_at)
);
CREATE INDEX external_events_city_idx ON external_events (city_id, starts_at) WHERE active;

CREATE TABLE external_event_interests (
    event_id   uuid NOT NULL REFERENCES external_events(id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (event_id, user_id)
);
