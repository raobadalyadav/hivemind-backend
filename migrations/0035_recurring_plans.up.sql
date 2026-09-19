-- Recurring plans (flow.md §23): plan_recurrences (migration 0004) had no
-- writers. One rule per template plan; occurrences are real plan rows sharing
-- series_id (migration 0033).
ALTER TABLE plan_recurrences
    ADD CONSTRAINT plan_recurrences_plan_uidx UNIQUE (plan_id),
    ADD COLUMN active BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN ended_before TIMESTAMPTZ;
