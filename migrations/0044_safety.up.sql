CREATE TABLE emergency_contacts (
    user_id      uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    email        text NOT NULL CHECK (char_length(email) <= 254),
    phone        text NOT NULL DEFAULT '' CHECK (char_length(phone) <= 20),
    relationship text NOT NULL DEFAULT '' CHECK (char_length(relationship) <= 50),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- contact_delivery is what we actually managed to do, recorded honestly:
-- 'sent' (email accepted by the provider), 'failed' (provider error) or
-- 'no_contact' (nothing configured to notify).
CREATE TABLE sos_events (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          uuid NOT NULL REFERENCES users(id),
    plan_id          uuid REFERENCES plans(id),
    latitude         double precision CHECK (latitude BETWEEN -90 AND 90),
    longitude        double precision CHECK (longitude BETWEEN -180 AND 180),
    note             text NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    contact_delivery text NOT NULL CHECK (contact_delivery IN ('sent','failed','no_contact')),
    delivery_error   text NOT NULL DEFAULT '',
    acknowledged_by  uuid REFERENCES users(id),
    acknowledged_at  timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sos_events_user_idx ON sos_events (user_id, created_at DESC);
CREATE INDEX sos_events_open_idx ON sos_events (created_at DESC) WHERE acknowledged_at IS NULL;
