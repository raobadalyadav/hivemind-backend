package admin

import (
	"context"
	"testing"
	"time"
)

func TestAdmin_SOSAcknowledgeAndExternalEventsAreAudited(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	suffix := time.Now().Format("150405.000000000")
	var admin1, admin2, victim, city string
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "adm1-"+suffix+"@example.com").Scan(&admin1)
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "adm2-"+suffix+"@example.com").Scan(&admin2)
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "victim-"+suffix+"@example.com").Scan(&victim)
	pool.QueryRow(ctx, `INSERT INTO cities (name) VALUES ($1) RETURNING id`, "Admin City "+suffix).Scan(&city)
	var sosID string
	if err := pool.QueryRow(ctx, `INSERT INTO sos_events (user_id, contact_delivery) VALUES ($1,'no_contact') RETURNING id`, victim).Scan(&sosID); err != nil {
		t.Fatalf("seed sos: %v", err)
	}

	open, err := svc.ListSOSEvents(ctx, true, 100)
	found := false
	for _, e := range open {
		found = found || e.ID == sosID
	}
	if err != nil || !found {
		t.Fatalf("the new event should be listed as unacknowledged: err=%v", err)
	}
	if err := svc.AcknowledgeSOSEvent(ctx, sosID, admin1); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if err := svc.AcknowledgeSOSEvent(ctx, sosID, admin2); err != nil { // idempotent, first admin stays
		t.Fatalf("second ack: %v", err)
	}
	var by string
	var audits int
	pool.QueryRow(ctx, `SELECT acknowledged_by::text FROM sos_events WHERE id = $1`, sosID).Scan(&by)
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action = 'acknowledge_sos' AND subject_id = $1`, sosID).Scan(&audits)
	if by != admin1 || audits != 1 {
		t.Fatalf("first acknowledger is kept and audited exactly once: by=%s audits=%d", by, audits)
	}
	if err := svc.AcknowledgeSOSEvent(ctx, "00000000-0000-0000-0000-000000000000", admin1); err != ErrNotFound {
		t.Fatalf("unknown sos: %v", err)
	}

	start := time.Now().Add(48 * time.Hour)
	for name, bad := range map[string]ExternalEvent{
		"no title":     {CityID: city, SourceURL: "https://x.example", StartsAt: start},
		"bad url":      {CityID: city, Title: "t", SourceURL: "javascript:alert(1)", StartsAt: start},
		"unknown city": {CityID: "00000000-0000-0000-0000-000000000000", Title: "t", SourceURL: "https://x.example", StartsAt: start},
		"ends before":  {CityID: city, Title: "t", SourceURL: "https://x.example", StartsAt: start, EndsAt: &[]time.Time{start.Add(-time.Hour)}[0]},
	} {
		if _, err := svc.CreateExternalEvent(ctx, bad, admin1); err != ErrInvalidInput {
			t.Errorf("%s must be rejected, got %v", name, err)
		}
	}
	ev, err := svc.CreateExternalEvent(ctx, ExternalEvent{CityID: city, Title: "Comedy Night", SourceURL: "https://tix.example/1", StartsAt: start}, admin1)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.DeactivateExternalEvent(ctx, ev.ID, admin1); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	svc.DeactivateExternalEvent(ctx, ev.ID, admin2) // repeat: no second audit
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action IN ('create_external_event','deactivate_external_event') AND subject_id = $1`, ev.ID).Scan(&audits)
	if audits != 2 {
		t.Fatalf("create + one deactivate audited, got %d", audits)
	}
	if err := svc.DeactivateExternalEvent(ctx, "not-a-uuid", admin1); err != ErrNotFound {
		t.Fatalf("bad id: %v", err)
	}
}
