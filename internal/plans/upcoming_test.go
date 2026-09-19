package plans

import (
	"context"
	"testing"
	"time"
)

func TestListUpcomingPlans_WindowCategoryFreeAndVisibility(t *testing.T) {
	pool := typesPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc, _ := newTypesServices(t, pool, fakeEnt{})

	var city, cat string
	pool.QueryRow(ctx, `INSERT INTO cities (name) VALUES ($1) RETURNING id`, "Up City "+time.Now().Format("150405.000000000")).Scan(&city)
	pool.QueryRow(ctx, `INSERT INTO categories (name, slug) VALUES ($1,$1) RETURNING id`, "UpCat"+time.Now().Format("150405.000000000")).Scan(&cat)
	host, viewer := typesUser(t, pool, "uphost"), typesUser(t, pool, "upview")
	pool.Exec(ctx, `UPDATE users SET city_id = $2 WHERE id = $1`, viewer, city)

	day := func(d int) time.Time { return time.Now().Add(time.Duration(d) * 24 * time.Hour) }
	mk := func(title string, in time.Time, price int64, category, vis, join string) *Plan {
		p, err := svc.CreatePlan(ctx, &Plan{Title: title, HostID: host, Capacity: 5, CityID: city, CategoryID: category, PriceMinor: price,
			Visibility: vis, JoinMode: join, StartsAt: in}, "")
		if err != nil {
			t.Fatalf("CreatePlan %s: %v", title, err)
		}
		return p
	}
	thisWeek := mk("this week dinner", day(2), 0, cat, "public", "open")
	nextWeek := mk("next week drinks", day(9), 50000, cat, "public", "open")
	otherCat := mk("no category", day(3), 0, "", "public", "open")
	mk("secret", day(4), 0, cat, "private", "invite_only")
	late := mk("far away", day(40), 0, cat, "public", "open")

	ids := func(f UpcomingFilter) map[string]int {
		t.Helper()
		got, err := svc.ListUpcomingPlans(ctx, viewer, f)
		if err != nil {
			t.Fatalf("ListUpcomingPlans: %v", err)
		}
		m := map[string]int{}
		for i, p := range got {
			m[p.ID] = i
		}
		return m
	}

	all := ids(UpcomingFilter{To: day(30)})
	if _, in := all[thisWeek.ID]; !in {
		t.Fatal("the caller's-city plan in the window is listed")
	}
	if _, in := all[late.ID]; in {
		t.Error("beyond the window is excluded")
	}
	if all[thisWeek.ID] > all[nextWeek.ID] {
		t.Error("soonest first")
	}
	next := ids(UpcomingFilter{From: day(7), To: day(14)})
	if _, in := next[nextWeek.ID]; !in || len(next) != 1 {
		t.Errorf("a 'next week' window has only next week's plan: %v", next)
	}
	if c := ids(UpcomingFilter{To: day(30), CategoryID: cat}); func() bool { _, in := c[otherCat.ID]; return in }() {
		t.Error("category filter excludes other categories")
	}
	free := ids(UpcomingFilter{To: day(30), FreeOnly: true})
	if _, in := free[nextWeek.ID]; in {
		t.Error("free-only excludes paid plans")
	}
	got, _ := svc.ListUpcomingPlans(ctx, viewer, UpcomingFilter{To: day(30)})
	for _, p := range got {
		if p.Title == "secret" {
			t.Fatal("private plans are never listed")
		}
	}
	if len(got) > 0 && got[0].CoverURL != "" {
		t.Error("cover fields load without error")
	}
	if _, err := svc.ListUpcomingPlans(ctx, viewer, UpcomingFilter{From: day(5), To: day(1)}); err != ErrInvalidInput {
		t.Errorf("inverted window: %v", err)
	}
	if _, err := svc.ListUpcomingPlans(ctx, viewer, UpcomingFilter{CategoryID: "nope", To: day(30)}); err != ErrInvalidInput {
		t.Errorf("bad category id: %v", err)
	}
}
