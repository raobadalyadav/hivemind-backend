-- Smart Groups (Phase 4, flow.md §12): a host/admin-triggered split of a
-- plan's confirmed participants into balanced subgroups, each with its own
-- ephemeral chat room.
CREATE TABLE plan_groups (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_id UUID NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    chat_room_id UUID NOT NULL REFERENCES chat_rooms(id),
    label TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE plan_group_members (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    plan_group_id UUID NOT NULL REFERENCES plan_groups(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE (plan_group_id, user_id)
);

-- Prevents duplicate "Group A" rows for the same plan on a retried
-- generation call — service-level idempotency (GenerateSmartGroups checks
-- for existing groups first) is the primary guard; this is the DB backstop.
CREATE UNIQUE INDEX plan_groups_plan_id_label_idx ON plan_groups (plan_id, label);
