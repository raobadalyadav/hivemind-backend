DROP RULE IF EXISTS outbox_events_no_delete ON outbox_events;
DROP RULE IF EXISTS audit_logs_no_delete ON audit_logs;
DROP RULE IF EXISTS audit_logs_no_update ON audit_logs;
DROP RULE IF EXISTS reconciliation_entries_no_delete ON reconciliation_entries;
DROP RULE IF EXISTS reconciliation_entries_no_update ON reconciliation_entries;
