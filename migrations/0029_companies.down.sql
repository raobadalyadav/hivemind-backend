DROP INDEX IF EXISTS bookings_company_id_idx;
ALTER TABLE bookings DROP COLUMN company_id;
DROP TABLE company_members;
DROP TABLE companies;
