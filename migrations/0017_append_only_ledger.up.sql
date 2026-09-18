-- PRD §29: "All finance state transitions are append-only in an
-- audit/reconciliation ledger." Enforced at the DB level, not just by
-- app-code discipline — UPDATE/DELETE on these tables becomes a silent
-- no-op (DO INSTEAD NOTHING), matching Postgres RULE semantics: it does not
-- raise an error, it just rewrites the statement to do nothing.
CREATE RULE reconciliation_entries_no_update AS ON UPDATE TO reconciliation_entries DO INSTEAD NOTHING;
CREATE RULE reconciliation_entries_no_delete AS ON DELETE TO reconciliation_entries DO INSTEAD NOTHING;

CREATE RULE audit_logs_no_update AS ON UPDATE TO audit_logs DO INSTEAD NOTHING;
CREATE RULE audit_logs_no_delete AS ON DELETE TO audit_logs DO INSTEAD NOTHING;

CREATE RULE outbox_events_no_delete AS ON DELETE TO outbox_events DO INSTEAD NOTHING;
-- outbox_events UPDATE is still allowed: Drain() legitimately sets published_at.
