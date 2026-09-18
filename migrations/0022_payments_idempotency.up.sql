-- A replayed (or Cashfree-retried) webhook must not create a second
-- payments/reconciliation_entries row for the same gateway payment. NULLs
-- are unaffected by a UNIQUE constraint (Postgres treats each NULL as
-- distinct), so this only rejects genuine duplicate gateway_payment_id
-- values — see internal/payments.Repository.MarkCaptured, which now treats
-- the resulting unique-violation as an idempotent success rather than an error.
ALTER TABLE payments ADD CONSTRAINT payments_gateway_payment_id_key UNIQUE (gateway_payment_id);
