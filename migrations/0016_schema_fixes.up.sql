-- plan_participants.booking_id was UUID with no FK in the scaffold.
ALTER TABLE plan_participants
    ADD CONSTRAINT plan_participants_booking_id_fkey
    FOREIGN KEY (booking_id) REFERENCES bookings(id) ON DELETE SET NULL;

CREATE INDEX orders_booking_id_idx ON orders (booking_id);
CREATE INDEX payments_order_id_idx ON payments (order_id);
CREATE INDEX refunds_payment_id_idx ON refunds (payment_id);
CREATE INDEX moderation_cases_report_id_idx ON moderation_cases (report_id);

ALTER TABLE orders ADD CONSTRAINT orders_amount_positive CHECK (amount_minor > 0);
ALTER TABLE payments ADD CONSTRAINT payments_amount_positive CHECK (amount_minor > 0);
ALTER TABLE refunds ADD CONSTRAINT refunds_amount_positive CHECK (amount_minor > 0);
