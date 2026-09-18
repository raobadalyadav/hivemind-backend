ALTER TABLE community_members ALTER COLUMN role DROP DEFAULT;
ALTER TABLE community_members ALTER COLUMN role TYPE TEXT USING role::TEXT;
ALTER TABLE community_members ALTER COLUMN role SET DEFAULT 'member';

ALTER TABLE devices ALTER COLUMN platform TYPE TEXT USING platform::TEXT;

ALTER TABLE notifications ALTER COLUMN channel TYPE TEXT USING channel::TEXT;

ALTER TABLE user_profiles ALTER COLUMN verification_status DROP DEFAULT;
ALTER TABLE user_profiles ALTER COLUMN verification_status TYPE TEXT USING verification_status::TEXT;
ALTER TABLE user_profiles ALTER COLUMN verification_status SET DEFAULT 'unverified';

ALTER TABLE moderation_cases ALTER COLUMN status DROP DEFAULT;
ALTER TABLE moderation_cases ALTER COLUMN status TYPE TEXT USING status::TEXT;
ALTER TABLE moderation_cases ALTER COLUMN status SET DEFAULT 'open';

ALTER TABLE connections ALTER COLUMN status DROP DEFAULT;
ALTER TABLE connections ALTER COLUMN status TYPE TEXT USING status::TEXT;
ALTER TABLE connections ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE payouts ALTER COLUMN status DROP DEFAULT;
ALTER TABLE payouts ALTER COLUMN status TYPE TEXT USING status::TEXT;
ALTER TABLE payouts ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE payout_accounts ALTER COLUMN status DROP DEFAULT;
ALTER TABLE payout_accounts ALTER COLUMN status TYPE TEXT USING status::TEXT;
ALTER TABLE payout_accounts ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE refunds ALTER COLUMN status DROP DEFAULT;
ALTER TABLE refunds ALTER COLUMN status TYPE TEXT USING status::TEXT;
ALTER TABLE refunds ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE payments ALTER COLUMN status DROP DEFAULT;
ALTER TABLE payments ALTER COLUMN status TYPE TEXT USING status::TEXT;
ALTER TABLE payments ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE orders ALTER COLUMN status DROP DEFAULT;
ALTER TABLE orders ALTER COLUMN status TYPE TEXT USING status::TEXT;
ALTER TABLE orders ALTER COLUMN status SET DEFAULT 'created';

ALTER TABLE bookings ALTER COLUMN status DROP DEFAULT;
ALTER TABLE bookings ALTER COLUMN status TYPE TEXT USING status::TEXT;
ALTER TABLE bookings ALTER COLUMN status SET DEFAULT 'initiated';

ALTER TABLE plans ALTER COLUMN status DROP DEFAULT;
ALTER TABLE plans ALTER COLUMN status TYPE TEXT USING status::TEXT;
ALTER TABLE plans ALTER COLUMN status SET DEFAULT 'draft';

DROP TYPE community_role;
DROP TYPE device_platform;
DROP TYPE notification_channel;
DROP TYPE verification_status;
DROP TYPE case_status;
DROP TYPE connection_status;
DROP TYPE payout_status;
DROP TYPE refund_status;
DROP TYPE payment_status;
DROP TYPE order_status;
DROP TYPE booking_status;
DROP TYPE plan_status;
