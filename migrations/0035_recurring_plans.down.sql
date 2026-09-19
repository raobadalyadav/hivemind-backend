ALTER TABLE plan_recurrences
    DROP COLUMN ended_before, DROP COLUMN active, DROP CONSTRAINT plan_recurrences_plan_uidx;
