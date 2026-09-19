-- Waitlist (flow.md §16): waitlist_entries becomes the single source of
-- truth for queue position and seat offers. An 'offered' row holds a seat
-- until offer_expires_at; expiry is lazy (holds are counted only while
-- offer_expires_at > now()) and swept by the worker.
CREATE TYPE waitlist_status AS ENUM ('waiting', 'offered', 'accepted', 'expired', 'left');

ALTER TABLE waitlist_entries
    ADD COLUMN status waitlist_status NOT NULL DEFAULT 'waiting',
    ADD COLUMN offered_at TIMESTAMPTZ,
    ADD COLUMN offer_expires_at TIMESTAMPTZ,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX waitlist_queue_idx ON waitlist_entries (plan_id, created_at, id) WHERE status = 'waiting';
CREATE INDEX waitlist_offers_idx ON waitlist_entries (offer_expires_at) WHERE status = 'offered';
