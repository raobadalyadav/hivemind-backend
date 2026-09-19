-- Single definition of "attended": explicitly checked in, or a confirmed
-- booking on a plan that has since ended.
CREATE VIEW attended_bookings AS
SELECT b.id AS booking_id, b.user_id, b.plan_id
FROM bookings b
JOIN plans p ON p.id = b.plan_id
WHERE b.status = 'attended'::booking_status
   OR (b.status = 'confirmed'::booking_status AND p.ends_at < now() AND p.status IN ('published','completed'));
