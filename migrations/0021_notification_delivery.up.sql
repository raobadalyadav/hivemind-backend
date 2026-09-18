-- notifications.read tracks user interaction (did they open it); nothing
-- tracked whether Resend/FCM actually accepted the message. Add that.
CREATE TYPE delivery_status AS ENUM ('pending', 'sent', 'failed');

ALTER TABLE notifications ADD COLUMN delivery_status delivery_status NOT NULL DEFAULT 'pending';
ALTER TABLE notifications ADD COLUMN sent_at TIMESTAMPTZ;
ALTER TABLE notifications ADD COLUMN delivery_error TEXT;
