-- Phase 3 Marketplace: venues ownership, host verification/payouts, coupons,
-- credits, promoted listings, and gating reviews to a real attended booking.

-- Part A: venues gain an owner (internal/venues didn't exist before this).
ALTER TABLE venues ADD COLUMN owner_host_id UUID REFERENCES users(id);
CREATE INDEX venues_owner_host_id_idx ON venues (owner_host_id);

-- Part B: payout_accounts becomes the combined host-verification + payout
-- profile (PRD §21: "verification, bank details, tax fields, payout status"
-- bundled together). UNIQUE(host_id) makes ApplyForHost idempotent via
-- ON CONFLICT (host_id) DO UPDATE.
ALTER TABLE payout_accounts ADD COLUMN bank_details JSONB NOT NULL DEFAULT '{}';
ALTER TABLE payout_accounts ADD COLUMN id_document_url TEXT NOT NULL DEFAULT '';
ALTER TABLE payout_accounts ADD CONSTRAINT payout_accounts_host_id_key UNIQUE (host_id);

-- Part C: business_pro is a subscription_products row like any other
-- product — Business Accounts need no new schema, just an entitlement to
-- check (internal/subscriptions gains HasEntitlement, no new table).
INSERT INTO subscription_products (name, price_minor, currency, interval)
VALUES ('business_pro', 99900, 'INR', 'month');

-- Part D: coupons. Admin-created only for MVP (avoid host-abuse complexity).
CREATE TABLE promo_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL UNIQUE,
    discount_type TEXT NOT NULL DEFAULT 'percent', -- percent|fixed
    discount_value BIGINT NOT NULL,
    max_uses INT NOT NULL DEFAULT 0, -- 0 = unlimited
    uses_count INT NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Part E: credits — append-only, platform-granted only, non-withdrawable
-- (PRD §21 explicitly forbids a general cash-out wallet; this ledger can
-- only ever be a discount at checkout, never converted back to money).
CREATE TABLE credit_ledger (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    amount_minor BIGINT NOT NULL, -- positive = grant, negative = spend
    reason TEXT NOT NULL,
    order_id UUID REFERENCES orders(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX credit_ledger_user_id_idx ON credit_ledger (user_id);
CREATE RULE credit_ledger_no_update AS ON UPDATE TO credit_ledger DO INSTEAD NOTHING;
CREATE RULE credit_ledger_no_delete AS ON DELETE TO credit_ledger DO INSTEAD NOTHING;

-- Part F: promoted listings — deliberately its own row-is-the-order table,
-- not folded into orders/payments (keeps the live-verified booking-payment
-- path untouched for a lower-volume feature).
CREATE TYPE promoted_listing_status AS ENUM ('pending_payment', 'paid', 'failed');
CREATE TABLE promoted_listings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES plans(id),
    host_id UUID NOT NULL REFERENCES users(id),
    amount_minor BIGINT NOT NULL,
    currency CHAR(3) NOT NULL DEFAULT 'INR',
    gateway_order_id TEXT,
    gateway_payment_id TEXT,
    status promoted_listing_status NOT NULL DEFAULT 'pending_payment',
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX promoted_listings_gateway_payment_id_key ON promoted_listings (gateway_payment_id) WHERE gateway_payment_id IS NOT NULL;
CREATE INDEX promoted_listings_plan_id_idx ON promoted_listings (plan_id);
CREATE INDEX promoted_listings_host_id_idx ON promoted_listings (host_id);

-- Part G: reviews — gate to a real attended booking, one review per booking.
-- Safe to add NOT NULL: table has no writers anywhere in the codebase yet.
ALTER TABLE reviews ADD COLUMN booking_id UUID NOT NULL REFERENCES bookings(id);
ALTER TABLE reviews ADD CONSTRAINT reviews_booking_id_key UNIQUE (booking_id);
