ALTER TABLE refunds DROP CONSTRAINT refunds_amount_positive;
ALTER TABLE payments DROP CONSTRAINT payments_amount_positive;
ALTER TABLE orders DROP CONSTRAINT orders_amount_positive;

DROP INDEX IF EXISTS moderation_cases_report_id_idx;
DROP INDEX IF EXISTS refunds_payment_id_idx;
DROP INDEX IF EXISTS payments_order_id_idx;
DROP INDEX IF EXISTS orders_booking_id_idx;

ALTER TABLE plan_participants DROP CONSTRAINT plan_participants_booking_id_fkey;
