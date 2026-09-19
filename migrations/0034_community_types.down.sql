DROP TABLE community_join_requests;
ALTER TABLE communities DROP CONSTRAINT communities_paid_needs_entitlement;
ALTER TABLE communities DROP COLUMN required_entitlement, DROP COLUMN category_id, DROP COLUMN rules,
    DROP COLUMN cover_image_url, DROP COLUMN membership_type;
DROP TYPE community_type;
