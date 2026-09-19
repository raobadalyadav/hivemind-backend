ALTER TABLE payout_accounts ALTER COLUMN status DROP DEFAULT;
ALTER TABLE payout_accounts ALTER COLUMN status TYPE payout_status USING status::text::payout_status;
ALTER TABLE payout_accounts ALTER COLUMN status SET DEFAULT 'pending';

DROP TYPE payout_account_status;
