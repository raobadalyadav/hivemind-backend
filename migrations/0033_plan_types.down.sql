DROP VIEW plans_discoverable;
DROP TABLE plan_invites;
DROP TABLE plan_join_requests;
DROP INDEX community_events_uidx;
DROP INDEX plans_series_starts_uidx;
ALTER TABLE plans DROP CONSTRAINT plans_invite_not_public, DROP CONSTRAINT plans_community_vis;
ALTER TABLE plans DROP COLUMN series_id, DROP COLUMN requires_entitlement, DROP COLUMN community_id,
    DROP COLUMN visibility, DROP COLUMN join_mode;
DROP TYPE join_request_status;
DROP TYPE plan_visibility;
DROP TYPE plan_join_mode;
