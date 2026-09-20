package plans

import (
	"context"
	"testing"
	"time"
)

func TestSavedPlans_RatingsAndSavedFlagAreDecoratedInBatch(t *testing.T) {
	pool := typesPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc, _ := newTypesServices(t, pool, fakeEnt{})

	host, other, viewer, guest := typesUser(t, pool, "svhost"), typesUser(t, pool, "svother"), typesUser(t, pool, "svview"), typesUser(t, pool, "svguest")
	start := time.Now().Add(48 * time.Hour)
	mk := func(h, title, vis, join string) *Plan {
		p, err := svc.CreatePlan(ctx, &Plan{Title: title, HostID: h, Capacity: 5, PriceMinor: 0, Visibility: vis, JoinMode: join, StartsAt: start}, "")
		if err != nil {
			t.Fatalf("CreatePlan: %v", err)
		}
		return p
	}
	pub, other1, secret := mk(host, "public dinner", "public", "open"), mk(other, "other's plan", "public", "open"), mk(host, "secret", "private", "invite_only")

	// two reviews on the host's (older) plans → 4 and 5 => avg 4.5, count 2; the other host has none
	review := func(plan, user string, rating int) {
		t.Helper()
		var b string
		if err := pool.QueryRow(ctx, `INSERT INTO bookings (plan_id, user_id, status, price_minor, currency, idempotency_key) VALUES ($1,$2,'attended',0,'INR',$3) RETURNING id`,
			plan, user, "sv-"+plan+user).Scan(&b); err != nil {
			t.Fatalf("booking: %v", err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO reviews (plan_id, user_id, booking_id, rating) VALUES ($1,$2,$3,$4)`, plan, user, b, rating); err != nil {
			t.Fatalf("review: %v", err)
		}
	}
	review(pub.ID, viewer, 4)
	review(pub.ID, guest, 5)

	// save is idempotent; a hidden plan can't be saved by a stranger
	for i := 0; i < 2; i++ {
		if err := svc.SavePlan(ctx, pub.ID, viewer); err != nil {
			t.Fatalf("SavePlan #%d: %v", i, err)
		}
	}
	if err := svc.SavePlan(ctx, secret.ID, viewer); err != ErrPlanNotFound {
		t.Errorf("saving a private plan you can't see: %v", err)
	}
	if err := svc.SavePlan(ctx, "not-a-uuid", viewer); err != ErrPlanNotFound {
		t.Errorf("bad id: %v", err)
	}

	got, err := svc.GetPlanAsUser(ctx, pub.ID, viewer, "")
	if err != nil || !got.SavedByMe || got.HostRatingCount != 2 || got.HostRatingAvg != 4.5 {
		t.Fatalf("decorated plan: %+v err=%v", got, err)
	}
	if g2, _ := svc.GetPlanAsUser(ctx, pub.ID, guest, ""); g2.SavedByMe {
		t.Error("saved_by_me is per viewer")
	}
	if g3, _ := svc.GetPlanAsUser(ctx, pub.ID, "", ""); g3 == nil || g3.SavedByMe {
		t.Error("an anonymous viewer still gets the plan, not saved")
	}
	if o, _ := svc.GetPlanAsUser(ctx, other1.ID, viewer, ""); o.HostRatingCount != 0 || o.HostRatingAvg != 0 {
		t.Errorf("a host without reviews has count 0: %+v", o)
	}

	// batch: one call decorates every plan (upcoming list path)
	batch := []*Plan{{ID: pub.ID, HostID: host}, {ID: other1.ID, HostID: other}}
	if err := svc.Decorate(ctx, batch, viewer); err != nil || !batch[0].SavedByMe || batch[0].HostRatingCount != 2 || batch[1].SavedByMe || batch[1].HostRatingCount != 0 {
		t.Fatalf("batch decorate: %+v %+v err=%v", batch[0], batch[1], err)
	}

	list, err := svc.ListSavedPlans(ctx, viewer)
	if err != nil || len(list) != 1 || list[0].ID != pub.ID || !list[0].SavedByMe {
		t.Fatalf("saved list: %+v err=%v", list, err)
	}
	if err := svc.UnsavePlan(ctx, pub.ID, viewer); err != nil {
		t.Fatalf("unsave: %v", err)
	}
	if err := svc.UnsavePlan(ctx, pub.ID, viewer); err != nil {
		t.Errorf("unsave twice is fine: %v", err)
	}
	if list, _ := svc.ListSavedPlans(ctx, viewer); len(list) != 0 {
		t.Errorf("wishlist empty after unsave: %d", len(list))
	}
}
