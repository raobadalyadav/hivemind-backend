package plans

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func seedAttendee(t *testing.T, pool *pgxpool.Pool, planID, label string, interests []string) string {
	t.Helper()
	ctx := context.Background()
	id := typesUser(t, pool, label)
	if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (user_id, display_name, interests) VALUES ($1,$2,$3)
		ON CONFLICT (user_id) DO UPDATE SET display_name=$2, interests=$3`, id, label, interests); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plan_participants (plan_id, user_id) VALUES ($1,$2)`, planID, id); err != nil {
		t.Fatalf("seed participant: %v", err)
	}
	return id
}

func TestGetPlanParticipants_PrivacyRules(t *testing.T) {
	pool := typesPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc, _ := newTypesServices(t, pool, fakeEnt{})

	host := typesUser(t, pool, "wghost")
	plan, err := svc.CreatePlan(ctx, &Plan{Title: "Who's going", HostID: host, Capacity: 20, StartsAt: time.Now().Add(time.Hour)}, "")
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	me := seedAttendee(t, pool, plan.ID, "me", []string{"Food", "Travel"})
	normal := seedAttendee(t, pool, plan.ID, "normal", []string{"Food", "Travel", "Music"})
	hiddenForPlan := seedAttendee(t, pool, plan.ID, "hiddenplan", []string{"Food"})
	optedOut := seedAttendee(t, pool, plan.ID, "optedout", []string{"Food"})
	blocked := seedAttendee(t, pool, plan.ID, "blocked", []string{"Food"})
	blocker := seedAttendee(t, pool, plan.ID, "blocker", []string{"Food"})

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := svc.SetParticipantVisibility(ctx, plan.ID, hiddenForPlan, false); err != nil {
		t.Fatalf("SetParticipantVisibility: %v", err)
	}
	mustExec(`UPDATE user_profiles SET show_in_participant_previews = false WHERE user_id = $1`, optedOut)
	mustExec(`INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, me, blocked) // I blocked them
	mustExec(`INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, blocker, me) // they blocked me
	mustExec(`INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'accepted')`, me, normal)

	got, err := svc.GetPlanParticipants(ctx, plan.ID, me, "user")
	if err != nil {
		t.Fatalf("GetPlanParticipants: %v", err)
	}
	if len(got.Cards) != 1 || got.Cards[0].UserID != normal {
		t.Fatalf("only the normal attendee should show, got %+v", got.Cards)
	}
	if got.TotalAttending != 6 || got.HiddenCount != 4 {
		t.Fatalf("total=6 hidden=4 expected, got total=%d hidden=%d", got.TotalAttending, got.HiddenCount)
	}
	if got.Cards[0].SharedInterestCount != 2 || got.ConnectionsAttending != 1 {
		t.Fatalf("shared interests=2, connections attending=1 expected, got %+v conns=%d", got.Cards[0], got.ConnectionsAttending)
	}

	if err := svc.SetParticipantVisibility(ctx, plan.ID, host, false); err != ErrNotParticipant {
		t.Fatalf("a non-attendee has nothing to hide, got %v", err)
	}
}

func TestGetPlanParticipants_PrivatePlanHiddenFromStranger(t *testing.T) {
	pool := typesPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc, _ := newTypesServices(t, pool, fakeEnt{})
	host, stranger := typesUser(t, pool, "pvhost"), typesUser(t, pool, "pvstranger")
	plan, err := svc.CreatePlan(ctx, &Plan{Title: "Secret", HostID: host, Capacity: 5, JoinMode: "invite_only",
		Visibility: "private", StartsAt: time.Now().Add(time.Hour)}, "")
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if _, err := svc.GetPlanParticipants(ctx, plan.ID, stranger, "user"); err != ErrPlanNotFound {
		t.Fatalf("attendees of a hidden plan must not be listable, got %v", err)
	}
}

func TestGetPlanParticipants_ReportsWhetherTheCallerHidThemselves(t *testing.T) {
	pool := typesPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc, _ := newTypesServices(t, pool, fakeEnt{})
	host, me := typesUser(t, pool, "hbhost"), typesUser(t, pool, "hbme")
	p, err := svc.CreatePlan(ctx, &Plan{Title: "hidden by me", HostID: host, Capacity: 5, Visibility: "public", JoinMode: "open", StartsAt: time.Now().Add(48 * time.Hour)}, "")
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO plan_participants (plan_id, user_id, status) VALUES ($1,$2,'confirmed')`, p.ID, me); err != nil {
		t.Fatalf("participant: %v", err)
	}
	got, err := svc.GetPlanParticipants(ctx, p.ID, me, "")
	if err != nil || got.HiddenByMe {
		t.Fatalf("visible by default: %+v %v", got, err)
	}
	if err := svc.SetParticipantVisibility(ctx, p.ID, me, false); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if got, _ := svc.GetPlanParticipants(ctx, p.ID, me, ""); !got.HiddenByMe {
		t.Error("after hiding, the app can show 'Show me' again")
	}
	if stranger, _ := svc.GetPlanParticipants(ctx, p.ID, host, ""); stranger.HiddenByMe {
		t.Error("hidden_by_me is about the caller only")
	}
}
