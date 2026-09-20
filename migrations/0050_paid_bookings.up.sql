-- Paid bookings: a seat is held (payment_pending) until the payment lands or the hold expires.
ALTER TABLE bookings ADD COLUMN expires_at TIMESTAMPTZ;
CREATE INDEX bookings_pending_idx ON bookings (expires_at) WHERE status = 'payment_pending';

-- An order fully covered by credits or a coupon has nothing left to charge.
ALTER TABLE orders DROP CONSTRAINT orders_amount_positive;
ALTER TABLE orders ADD CONSTRAINT orders_amount_non_negative CHECK (amount_minor >= 0);

-- Refunds go to the gateway now: remember its id and when each attempt last changed.
ALTER TABLE refunds ADD COLUMN gateway_refund_id TEXT, ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
