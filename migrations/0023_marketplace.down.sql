ALTER TABLE reviews DROP CONSTRAINT reviews_booking_id_key;
ALTER TABLE reviews DROP COLUMN booking_id;

DROP INDEX IF EXISTS promoted_listings_host_id_idx;
DROP INDEX IF EXISTS promoted_listings_plan_id_idx;
DROP INDEX IF EXISTS promoted_listings_gateway_payment_id_key;
DROP TABLE promoted_listings;
DROP TYPE promoted_listing_status;

DROP RULE credit_ledger_no_delete ON credit_ledger;
DROP RULE credit_ledger_no_update ON credit_ledger;
DROP INDEX IF EXISTS credit_ledger_user_id_idx;
DROP TABLE credit_ledger;

DROP TABLE promo_codes;

DELETE FROM subscription_products WHERE name = 'business_pro';

ALTER TABLE payout_accounts DROP CONSTRAINT payout_accounts_host_id_key;
ALTER TABLE payout_accounts DROP COLUMN id_document_url;
ALTER TABLE payout_accounts DROP COLUMN bank_details;

DROP INDEX IF EXISTS venues_owner_host_id_idx;
ALTER TABLE venues DROP COLUMN owner_host_id;
