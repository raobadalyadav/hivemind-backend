package safety

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("skipping: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("skipping: %v", err)
	}
	return pool
}

func seedUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"sf-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	pool.Exec(context.Background(), `INSERT INTO user_profiles (user_id, display_name) VALUES ($1,$2)`, id, label)
	return id
}

type fakeEmail struct {
	sent []struct{ to, subject, body string }
	err  error
}

func (f *fakeEmail) Send(_ context.Context, to, subject, body string) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, struct{ to, subject, body string }{to, subject, body})
	return nil
}

func TestEmergencyContact_Validation(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool), nil, "")
	u := seedUser(t, pool, "contact")

	for name, c := range map[string]Contact{
		"no name":   {Email: "a@example.com"},
		"bad email": {Name: "Mum", Email: "not-an-email"},
		"bad phone": {Name: "Mum", Email: "a@example.com", Phone: "call me"},
	} {
		if err := svc.SetContact(ctx, u, c); err != ErrInvalidInput {
			t.Errorf("%s must be rejected, got %v", name, err)
		}
	}
	if _, err := svc.GetContact(ctx, u); err != ErrNoContact {
		t.Fatalf("no contact yet: %v", err)
	}
	if err := svc.SetContact(ctx, u, Contact{Name: " Mum ", Email: "Mum <mum@example.com>", Phone: "+91 98765-43210"}); err != nil {
		t.Fatalf("valid contact: %v", err)
	}
	if c, _ := svc.GetContact(ctx, u); c.Name != "Mum" || c.Email != "mum@example.com" {
		t.Fatalf("stored contact should be normalised: %+v", c)
	}
	center, _ := svc.SafetyCenter(ctx, u)
	if !center.HasContact || center.EmergencyNumber != "112" || !strings.Contains(center.Disclaimer, "does not contact") {
		t.Fatalf("safety center: %+v", center)
	}
	svc.DeleteContact(ctx, u)
	if center, _ = svc.SafetyCenter(ctx, u); center.HasContact {
		t.Fatal("contact should be gone")
	}
}

func TestTriggerSOS_RecordsAndDegradesWithoutEmail(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	u := seedUser(t, pool, "sos")

	noEmail := NewService(NewRepository(pool), nil, "112")
	if _, err := noEmail.TriggerSOS(ctx, SOS{UserID: u}, false); err != ErrNotConfirmed {
		t.Fatalf("an unconfirmed SOS must be refused, got %v", err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM sos_events WHERE user_id = $1`, u).Scan(&n)
	if n != 0 {
		t.Fatal("an unconfirmed SOS must not be recorded")
	}

	// No contact at all: still recorded, honest about it, still has the disclaimer.
	res, err := noEmail.TriggerSOS(ctx, SOS{UserID: u, Note: "help"}, true)
	if err != nil || res.ContactNotified || res.EmergencyNumber != "112" || !strings.Contains(res.Disclaimer, "does not contact") {
		t.Fatalf("no-contact SOS: %+v err=%v", res, err)
	}
	var delivery string
	pool.QueryRow(ctx, `SELECT contact_delivery FROM sos_events WHERE id = $1`, res.ID).Scan(&delivery)
	if delivery != "no_contact" {
		t.Fatalf("delivery should be no_contact, got %q", delivery)
	}

	// Contact set but no email provider: recorded as failed, never claimed sent.
	u2 := seedUser(t, pool, "sos2")
	noEmail.SetContact(ctx, u2, Contact{Name: "Mum", Email: "mum@example.com"})
	res, _ = noEmail.TriggerSOS(ctx, SOS{UserID: u2}, true)
	pool.QueryRow(ctx, `SELECT contact_delivery FROM sos_events WHERE id = $1`, res.ID).Scan(&delivery)
	if res.ContactNotified || delivery != "failed" {
		t.Fatalf("no provider → failed and not notified: %+v delivery=%s", res, delivery)
	}
}

func TestTriggerSOS_EmailsOnceAndVerifiesPlan(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	fake := &fakeEmail{}
	svc := NewService(NewRepository(pool), fake, "112")
	clock := time.Now()
	svc.now = func() time.Time { return clock }

	u, host, stranger := seedUser(t, pool, "sosu"), seedUser(t, pool, "soshost"), seedUser(t, pool, "sosx")
	svc.SetContact(ctx, u, Contact{Name: "Mum <b>", Email: "mum@example.com"})
	var planID string
	pool.QueryRow(ctx, `INSERT INTO plans (title, host_id, starts_at, ends_at, capacity, status)
		VALUES ('Saturday <i>Dinner</i>', $1, now(), now()+interval '2 hours', 5, 'published') RETURNING id`, host).Scan(&planID)
	pool.Exec(ctx, `INSERT INTO plan_participants (plan_id, user_id) VALUES ($1,$2)`, planID, u)

	if _, err := svc.TriggerSOS(ctx, SOS{UserID: stranger, PlanID: planID}, true); err != ErrPlanNotAllowed {
		t.Fatalf("a stranger can't attach someone else's plan: %v", err)
	}
	lat, lng := 28.6139, 77.2090
	res, err := svc.TriggerSOS(ctx, SOS{UserID: u, PlanID: planID, Lat: &lat, Lng: &lng, Note: "<script>x</script>"}, true)
	if err != nil || !res.ContactNotified || len(fake.sent) != 1 {
		t.Fatalf("first SOS: %+v err=%v sent=%d", res, err, len(fake.sent))
	}
	body := fake.sent[0].body
	if fake.sent[0].to != "mum@example.com" || !strings.Contains(body, "maps.google.com/?q=28.613900,77.209000") ||
		!strings.Contains(body, "Saturday &lt;i&gt;Dinner&lt;/i&gt;") || strings.Contains(body, "<script>") {
		t.Fatalf("email content (must contain verified plan + map link, and be HTML-escaped): %s", body)
	}

	// double-tap within 60s: same event, no second email
	again, _ := svc.TriggerSOS(ctx, SOS{UserID: u}, true)
	if again.ID != res.ID || len(fake.sent) != 1 || !again.ContactNotified {
		t.Fatalf("repeat within the window must not resend: %+v sent=%d", again, len(fake.sent))
	}
	// after the window it's a new event
	clock = clock.Add(2 * time.Minute)
	later, _ := svc.TriggerSOS(ctx, SOS{UserID: u}, true)
	if later.ID == res.ID || len(fake.sent) != 2 {
		t.Fatalf("a later SOS is a new event: %+v sent=%d", later, len(fake.sent))
	}

	// a provider failure is recorded, not hidden
	failing := NewService(NewRepository(pool), &fakeEmail{err: errors.New("smtp down")}, "112")
	u3 := seedUser(t, pool, "sosfail")
	failing.SetContact(ctx, u3, Contact{Name: "Dad", Email: "dad@example.com"})
	r3, err := failing.TriggerSOS(ctx, SOS{UserID: u3}, true)
	var d, e string
	pool.QueryRow(ctx, `SELECT contact_delivery, delivery_error FROM sos_events WHERE id = $1`, r3.ID).Scan(&d, &e)
	if err != nil || r3.ContactNotified || d != "failed" || e != "smtp down" {
		t.Fatalf("failure path: %+v err=%v delivery=%s/%s", r3, err, d, e)
	}
}
