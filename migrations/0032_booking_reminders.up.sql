-- Smart reminders (flow.md §40): one row per (booking, kind) is the claim
-- that makes the reminder job idempotent across ticks and worker replicas.
CREATE TABLE booking_reminders (
    booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('24h', '3h', '1h')),
    sent_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (booking_id, kind)
);
