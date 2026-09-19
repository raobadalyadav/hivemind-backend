-- migrations/0014 cast payout_accounts.status to the payout_status enum
-- (pending|processed|failed) — the same enum used for individual payout
-- transfers. But payout_accounts.status represents host-verification
-- state, a different concept entirely: 'active' (the value
-- admin.ApproveHost needs to write) isn't a valid payout_status value.
-- This was latent and unused until internal/host was built — nothing
-- wrote to this column before.
CREATE TYPE payout_account_status AS ENUM ('pending', 'active', 'suspended');

ALTER TABLE payout_accounts ALTER COLUMN status DROP DEFAULT;
ALTER TABLE payout_accounts ALTER COLUMN status TYPE payout_account_status USING status::text::payout_account_status;
ALTER TABLE payout_accounts ALTER COLUMN status SET DEFAULT 'pending';
