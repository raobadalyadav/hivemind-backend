-- DB-level validation for status-ish columns that were plain TEXT in the
-- scaffold. Existing values already match these labels, so no Go code needs
-- to change: repositories keep scanning status as a string; writes cast
-- explicitly (e.g. $1::plan_status).
CREATE TYPE plan_status AS ENUM ('draft','published','full','cancelled','completed');
CREATE TYPE booking_status AS ENUM ('initiated','payment_pending','confirmed','cancelled','refunded','no_show','attended');
CREATE TYPE order_status AS ENUM ('created','paid','failed','cancelled');
CREATE TYPE payment_status AS ENUM ('pending','captured','failed');
CREATE TYPE refund_status AS ENUM ('pending','processed','failed');
CREATE TYPE payout_status AS ENUM ('pending','processed','failed');
CREATE TYPE connection_status AS ENUM ('pending','accepted','rejected');
CREATE TYPE case_status AS ENUM ('open','under_review','resolved','appealed');
CREATE TYPE verification_status AS ENUM ('unverified','email','phone','id');
CREATE TYPE notification_channel AS ENUM ('push','in_app','email');
CREATE TYPE device_platform AS ENUM ('ios','android');
CREATE TYPE community_role AS ENUM ('member','moderator','owner');

ALTER TABLE plans ALTER COLUMN status DROP DEFAULT;
ALTER TABLE plans ALTER COLUMN status TYPE plan_status USING status::plan_status;
ALTER TABLE plans ALTER COLUMN status SET DEFAULT 'draft';

ALTER TABLE bookings ALTER COLUMN status DROP DEFAULT;
ALTER TABLE bookings ALTER COLUMN status TYPE booking_status USING status::booking_status;
ALTER TABLE bookings ALTER COLUMN status SET DEFAULT 'initiated';

ALTER TABLE orders ALTER COLUMN status DROP DEFAULT;
ALTER TABLE orders ALTER COLUMN status TYPE order_status USING status::order_status;
ALTER TABLE orders ALTER COLUMN status SET DEFAULT 'created';

ALTER TABLE payments ALTER COLUMN status DROP DEFAULT;
ALTER TABLE payments ALTER COLUMN status TYPE payment_status USING status::payment_status;
ALTER TABLE payments ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE refunds ALTER COLUMN status DROP DEFAULT;
ALTER TABLE refunds ALTER COLUMN status TYPE refund_status USING status::refund_status;
ALTER TABLE refunds ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE payout_accounts ALTER COLUMN status DROP DEFAULT;
ALTER TABLE payout_accounts ALTER COLUMN status TYPE payout_status USING status::payout_status;
ALTER TABLE payout_accounts ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE payouts ALTER COLUMN status DROP DEFAULT;
ALTER TABLE payouts ALTER COLUMN status TYPE payout_status USING status::payout_status;
ALTER TABLE payouts ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE connections ALTER COLUMN status DROP DEFAULT;
ALTER TABLE connections ALTER COLUMN status TYPE connection_status USING status::connection_status;
ALTER TABLE connections ALTER COLUMN status SET DEFAULT 'pending';

ALTER TABLE moderation_cases ALTER COLUMN status DROP DEFAULT;
ALTER TABLE moderation_cases ALTER COLUMN status TYPE case_status USING status::case_status;
ALTER TABLE moderation_cases ALTER COLUMN status SET DEFAULT 'open';

ALTER TABLE user_profiles ALTER COLUMN verification_status DROP DEFAULT;
ALTER TABLE user_profiles ALTER COLUMN verification_status TYPE verification_status USING verification_status::verification_status;
ALTER TABLE user_profiles ALTER COLUMN verification_status SET DEFAULT 'unverified';

ALTER TABLE notifications ALTER COLUMN channel TYPE notification_channel USING channel::notification_channel;

ALTER TABLE devices ALTER COLUMN platform TYPE device_platform USING NULLIF(platform, '')::device_platform;

ALTER TABLE community_members ALTER COLUMN role DROP DEFAULT;
ALTER TABLE community_members ALTER COLUMN role TYPE community_role USING role::community_role;
ALTER TABLE community_members ALTER COLUMN role SET DEFAULT 'member';
