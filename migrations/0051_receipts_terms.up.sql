-- Payment receipts with gap-free-per-year style numbers, issued once per captured payment.
CREATE SEQUENCE receipt_number_seq;
CREATE TABLE receipts (
    payment_id     UUID PRIMARY KEY REFERENCES payments(id),
    number         TEXT NOT NULL UNIQUE,
    issued_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    price_minor    BIGINT NOT NULL,   -- what the host charges for the seat
    fee_minor      BIGINT NOT NULL,   -- platform service fee, GST included
    fee_base_minor BIGINT NOT NULL,   -- the fee before GST
    fee_gst_minor  BIGINT NOT NULL,   -- GST on the fee
    gst_percent    NUMERIC(5,2) NOT NULL,
    paid_minor     BIGINT NOT NULL    -- what was actually charged to the payer (credits / coupons excluded)
);

-- Which version of the Terms + Privacy Policy a person accepted, and when (consents.consent_type = 'terms:<version>').
CREATE UNIQUE INDEX consents_user_type_idx ON consents (user_id, consent_type);
