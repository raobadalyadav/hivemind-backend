-- Corporate/B2B team bookings (Phase 5, flow.md §53).
CREATE TABLE companies (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    billing_email TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE company_members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    company_id UUID NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'member', -- owner|member
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (company_id, user_id)
);

-- Attribution only, for the company's own out-of-app billing reconciliation
-- — no invoicing system exists here (matches how payouts are recorded rows
-- an admin marks processed, not a real bank transfer).
ALTER TABLE bookings ADD COLUMN company_id UUID REFERENCES companies(id);
CREATE INDEX bookings_company_id_idx ON bookings (company_id);
