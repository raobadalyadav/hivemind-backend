ALTER TABLE refunds DROP COLUMN IF EXISTS updated_at, DROP COLUMN IF EXISTS gateway_refund_id;
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_amount_non_negative;
ALTER TABLE orders ADD CONSTRAINT orders_amount_positive CHECK (amount_minor > 0);
DROP INDEX IF EXISTS bookings_pending_idx;
ALTER TABLE bookings DROP COLUMN IF EXISTS expires_at;
