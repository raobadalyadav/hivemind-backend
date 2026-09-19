package meet

import (
	"context"
	"fmt"
	"os"
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

var seq int

type world struct {
	t    *testing.T
	pool *pgxpool.Pool
	ctx  context.Context
	city string
	svc  *Service
}

func newWorld(t *testing.T) *world {
	pool := testPool(t)
	t.Cleanup(pool.Close)
	w := &world{t: t, pool: pool, ctx: context.Background()}
	if err := pool.QueryRow(w.ctx, `INSERT INTO cities (name) VALUES ($1) RETURNING id`, fmt.Sprintf("Meet City %d-%d", time.Now().UnixNano(), seq)).Scan(&w.city); err != nil {
		t.Fatalf("seed city: %v", err)
	}
	w.svc = NewService(NewRepository(pool))
	return w
}

var fiveInterests = []string{"Food", "Coffee", "Sports", "Fitness", "Travel"}

// user creates an onboarded user in the world's city (override with opts).
func (w *world) user(name string, opts ...func(*userOpts)) string {
	w.t.Helper()
	seq++
	o := userOpts{city: w.city, interests: fiveInterests, dob: "1996-05-05", status: "active"}
	for _, f := range opts {
		f(&o)
	}
	var id string
	err := w.pool.QueryRow(w.ctx, `INSERT INTO users (email, city_id, date_of_birth, status) VALUES ($1, NULLIF($2,'')::uuid, NULLIF($3,'')::date, $4) RETURNING id`,
		fmt.Sprintf("mt-%s-%d-%d@example.com", name, time.Now().UnixNano(), seq), o.city, o.dob, o.status).Scan(&id)
	if err != nil {
		w.t.Fatalf("seed user: %v", err)
	}
	if _, err := w.pool.Exec(w.ctx, `INSERT INTO user_profiles (user_id, display_name, interests) VALUES ($1,$2,$3)`, id, name, o.interests); err != nil {
		w.t.Fatalf("seed profile: %v", err)
	}
	return id
}

type userOpts struct {
	city, dob, status string
	interests         []string
}

func inCity(c string) func(*userOpts)           { return func(o *userOpts) { o.city = c } }
func withInterests(i ...string) func(*userOpts) { return func(o *userOpts) { o.interests = i } }
func born(d string) func(*userOpts)             { return func(o *userOpts) { o.dob = d } }
func suspended() func(*userOpts)                { return func(o *userOpts) { o.status = "suspended" } }

func (w *world) exec(q string, args ...any) {
	w.t.Helper()
	if _, err := w.pool.Exec(w.ctx, q, args...); err != nil {
		w.t.Fatalf("%s: %v", q, err)
	}
}

func (w *world) deckIDs(viewer string, min, max int) map[string]int {
	w.t.Helper()
	cards, err := w.svc.GetDeck(w.ctx, viewer, 30, min, max)
	if err != nil {
		w.t.Fatalf("GetDeck: %v", err)
	}
	out := map[string]int{}
	for i, c := range cards {
		out[c.UserID] = i
	}
	return out
}

func TestDeck_OnlyEligiblePeople(t *testing.T) {
	w := newWorld(t)
	me := w.user("me")
	ok := w.user("ok")
	otherCity := w.user("elsewhere", inCity(func() string {
		var id string
		w.pool.QueryRow(w.ctx, `INSERT INTO cities (name) VALUES ($1) RETURNING id`, fmt.Sprintf("Other %d", time.Now().UnixNano())).Scan(&id)
		return id
	}()))
	blocked := w.user("blocked")
	blockedMe := w.user("blockedme")
	friend := w.user("friend")
	pending := w.user("pending")
	waved := w.user("waved")
	passed := w.user("passed")
	thin := w.user("thin", withInterests("Food"))
	gone := w.user("gone", suspended())

	w.exec(`INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, me, blocked)
	w.exec(`INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, blockedMe, me)
	w.exec(`INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'accepted')`, me, friend)
	w.exec(`INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'pending')`, pending, me)
	w.exec(`INSERT INTO swipes (actor_id, target_id, action) VALUES ($1,$2,'wave')`, me, waved)
	w.exec(`INSERT INTO swipes (actor_id, target_id, action) VALUES ($1,$2,'pass')`, me, passed)

	deck := w.deckIDs(me, 0, 0)
	if _, in := deck[ok]; !in {
		t.Fatal("an eligible person must be in the deck")
	}
	for name, id := range map[string]string{"other city": otherCity, "blocked by me": blocked, "blocked me": blockedMe, "friend": friend,
		"pending request": pending, "already waved": waved, "recently passed": passed, "<5 interests": thin, "suspended": gone, "me": me} {
		if _, in := deck[id]; in {
			t.Errorf("%s must not be in the deck", name)
		}
	}

	// A pass expires: after 30 days the person can reappear.
	w.exec(`UPDATE swipes SET created_at = now() - interval '31 days' WHERE actor_id = $1 AND target_id = $2`, me, passed)
	if _, in := w.deckIDs(me, 0, 0)[passed]; !in {
		t.Error("a pass older than 30 days should let the person come back")
	}
}

func TestDeck_OrderingAndAgeFilter(t *testing.T) {
	w := newWorld(t)
	me := w.user("me")
	plain := w.user("plain", withInterests("Music", "Movies", "Gaming", "Books", "Art"))
	similar := w.user("similar") // same five interests
	boosted := w.user("boosted", withInterests("Music", "Movies", "Gaming", "Books", "Art"))
	wavedAtMe := w.user("wavedatme", withInterests("Music", "Movies", "Gaming", "Books", "Art"))
	w.exec(`INSERT INTO profile_boosts (user_id, starts_at, ends_at) VALUES ($1, now() - interval '5 minutes', now() + interval '20 minutes')`, boosted)
	w.exec(`INSERT INTO swipes (actor_id, target_id, action) VALUES ($1,$2,'wave')`, wavedAtMe, me)

	deck := w.deckIDs(me, 0, 0)
	if !(deck[boosted] < deck[wavedAtMe] && deck[wavedAtMe] < deck[similar] && deck[similar] < deck[plain]) {
		t.Fatalf("order must be boosted → waved-at-me → shared interests → rest, got %v", deck)
	}

	w.exec(`UPDATE profile_boosts SET ends_at = now() - interval '1 minute', starts_at = now() - interval '31 minutes' WHERE user_id = $1`, boosted)
	if d := w.deckIDs(me, 0, 0); d[wavedAtMe] > d[boosted] {
		t.Fatalf("an expired boost must not outrank anyone, got %v", d)
	}

	young := w.user("young", born(time.Now().AddDate(-22, 0, 0).Format("2006-01-02")))
	old := w.user("old", born(time.Now().AddDate(-45, 0, 0).Format("2006-01-02")))
	unknown := w.user("unknown", born(""))
	d := w.deckIDs(me, 30, 50)
	if _, in := d[young]; in {
		t.Error("22-year-old is under the 30 minimum")
	}
	if _, in := d[old]; !in {
		t.Error("45-year-old is inside 30–50")
	}
	if _, in := d[unknown]; !in {
		t.Error("someone with an unknown age is never filtered out")
	}
	if _, err := w.svc.GetDeck(w.ctx, me, 10, 40, 30); err != ErrInvalidInput {
		t.Errorf("min > max is invalid, got %v", err)
	}
}

func notifications(w *world, to string) int {
	var n int
	w.pool.QueryRow(w.ctx, `SELECT count(*) FROM outbox_events WHERE event_type = 'NOTIFY_USER' AND subject_id = $1`, to).Scan(&n)
	return n
}

type fakeDM struct{ calls []string }

func (f *fakeDM) OpenDM(_ context.Context, a, b string) (string, error) {
	f.calls = append(f.calls, a+"|"+b)
	return "room-1", nil
}

func TestSwipe_WaveThenMutualWaveMatches(t *testing.T) {
	w := newWorld(t)
	dm := &fakeDM{}
	w.svc.WithDM(dm)
	a, b := w.user("aman"), w.user("bhavna")

	r, err := w.svc.Swipe(w.ctx, a, b, "wave")
	if err != nil || r.Matched || r.TargetName != "bhavna" {
		t.Fatalf("first wave: %+v err=%v", r, err)
	}
	if notifications(w, b) != 1 || notifications(w, a) != 0 {
		t.Fatal("the target — and only the target — is told about a wave")
	}
	// repeating the same swipe is idempotent
	if r, _ = w.svc.Swipe(w.ctx, a, b, "wave"); r.Matched {
		t.Fatal("waving twice must not create a match by itself")
	}

	incoming, _ := w.svc.ListWaves(w.ctx, b, false)
	if len(incoming) != 1 || incoming[0].UserID != a || incoming[0].Matched {
		t.Fatalf("B sees A's wave: %+v", incoming)
	}
	sent, _ := w.svc.ListWaves(w.ctx, a, true)
	if len(sent) != 1 || sent[0].UserID != b {
		t.Fatalf("A sees the sent wave: %+v", sent)
	}

	r, err = w.svc.Swipe(w.ctx, b, a, "wave")
	if err != nil || !r.Matched || r.RoomID != "room-1" || len(dm.calls) != 1 {
		t.Fatalf("B waving back is a match with a DM: %+v err=%v dm=%v", r, err, dm.calls)
	}
	var status string
	var rows int
	w.pool.QueryRow(w.ctx, `SELECT min(status::text), count(*) FROM connections WHERE (requester_id=$1 AND recipient_id=$2) OR (requester_id=$2 AND recipient_id=$1)`, a, b).Scan(&status, &rows)
	if status != "accepted" || rows != 1 {
		t.Fatalf("a match is exactly one accepted connection, got %s ×%d", status, rows)
	}
	if notifications(w, a) != 1 || notifications(w, b) != 2 {
		t.Fatalf("both are told about the match; the repeated wave did not notify again (A:1 B:wave+match) got A=%d B=%d", notifications(w, a), notifications(w, b))
	}
	if inc, _ := w.svc.ListWaves(w.ctx, b, false); len(inc) != 1 || !inc[0].Matched {
		t.Fatalf("the wave is now marked matched: %+v", inc)
	}
	// they're out of each other's decks now
	if _, in := w.deckIDs(a, 0, 0)[b]; in {
		t.Fatal("connections don't reappear in the deck")
	}
}

func TestSwipe_SuperMatchesAndUpgradesAPass(t *testing.T) {
	w := newWorld(t)
	a, b := w.user("a"), w.user("b")
	w.svc.Swipe(w.ctx, a, b, "pass")
	if r, _ := w.svc.Swipe(w.ctx, a, b, "super"); r.Matched {
		t.Fatal("a super wave alone is not a match")
	}
	var action string
	w.pool.QueryRow(w.ctx, `SELECT action FROM swipes WHERE actor_id=$1 AND target_id=$2`, a, b).Scan(&action)
	if action != "super" {
		t.Fatalf("a later swipe replaces the earlier decision, got %s", action)
	}
	if r, _ := w.svc.Swipe(w.ctx, b, a, "wave"); !r.Matched {
		t.Fatal("wave back at a super wave matches")
	}
}

func TestSwipe_PassIsSilentAndMutualWithExistingRequestReusesRow(t *testing.T) {
	w := newWorld(t)
	a, b := w.user("a"), w.user("b")
	if r, err := w.svc.Swipe(w.ctx, a, b, "pass"); err != nil || r.Matched {
		t.Fatalf("pass: %+v %v", r, err)
	}
	if notifications(w, b) != 0 {
		t.Fatal("a pass tells nobody anything")
	}
	// B had earlier sent A a connection request; a mutual wave accepts THAT row.
	c, d := w.user("c"), w.user("d")
	w.exec(`INSERT INTO connections (requester_id, recipient_id, status) VALUES ($1,$2,'pending')`, d, c)
	// (pending connections hide people from the deck, but a direct swipe is still legal)
	w.svc.Swipe(w.ctx, c, d, "wave")
	if r, _ := w.svc.Swipe(w.ctx, d, c, "wave"); !r.Matched {
		t.Fatal("mutual wave matches")
	}
	var n int
	w.pool.QueryRow(w.ctx, `SELECT count(*) FROM connections WHERE (requester_id=$1 AND recipient_id=$2) OR (requester_id=$2 AND recipient_id=$1)`, c, d).Scan(&n)
	if n != 1 {
		t.Fatalf("must reuse the existing pair row, found %d", n)
	}
}

func TestSwipe_Validation(t *testing.T) {
	w := newWorld(t)
	a, blocked, gone := w.user("a"), w.user("blocked"), w.user("gone", suspended())
	w.exec(`INSERT INTO blocks (user_id, blocked_user_id) VALUES ($1,$2)`, blocked, a)
	for name, c := range map[string]struct {
		target, action string
		want           error
	}{
		"self":       {a, "wave", ErrInvalidInput},
		"bad action": {blocked, "like", ErrInvalidInput},
		"blocked":    {blocked, "wave", ErrTargetNotFound},
		"suspended":  {gone, "wave", ErrTargetNotFound},
		"unknown":    {"00000000-0000-0000-0000-000000000000", "wave", ErrTargetNotFound},
		"not a uuid": {"nope", "wave", ErrTargetNotFound},
	} {
		if _, err := w.svc.Swipe(w.ctx, a, c.target, c.action); err != c.want {
			t.Errorf("%s: got %v want %v", name, err, c.want)
		}
	}
}

func TestSwipe_DailyLimits(t *testing.T) {
	w := newWorld(t)
	a := w.user("a")
	targets := make([]string, dailySupers+1)
	for i := range targets {
		targets[i] = w.user(fmt.Sprintf("t%d", i))
	}
	for i := 0; i < dailySupers; i++ {
		if _, err := w.svc.Swipe(w.ctx, a, targets[i], "super"); err != nil {
			t.Fatalf("super %d: %v", i, err)
		}
	}
	if _, err := w.svc.Swipe(w.ctx, a, targets[dailySupers], "super"); err != ErrRateLimited {
		t.Fatalf("the 6th super wave in a day must be limited, got %v", err)
	}
	if _, err := w.svc.Swipe(w.ctx, a, targets[dailySupers], "wave"); err != nil {
		t.Fatalf("an ordinary wave is still allowed: %v", err)
	}
}

func TestBoost_FreeThenPaidAndGuards(t *testing.T) {
	w := newWorld(t)
	u := w.user("booster")

	st, _ := w.svc.BoostStatus(w.ctx, u)
	if st.Active || !st.FreeAvailable {
		t.Fatalf("a fresh user has a free boost: %+v", st)
	}
	st, err := w.svc.ActivateBoost(w.ctx, u)
	if err != nil || !st.Active || st.FreeAvailable || time.Until(st.EndsAt) < 29*time.Minute {
		t.Fatalf("free boost: %+v err=%v", st, err)
	}
	if _, err := w.svc.ActivateBoost(w.ctx, u); err != ErrAlreadyBoosted {
		t.Fatalf("can't stack boosts, got %v", err)
	}
	var charged int64
	w.pool.QueryRow(w.ctx, `SELECT COALESCE(sum(amount_minor),0) FROM credit_ledger WHERE user_id=$1`, u).Scan(&charged)
	if charged != 0 {
		t.Fatalf("the free boost costs nothing, ledger=%d", charged)
	}

	// end it, then a second boost this week needs credits
	w.exec(`UPDATE profile_boosts SET starts_at = now() - interval '2 hours', ends_at = now() - interval '90 minutes' WHERE user_id = $1`, u)
	w.exec(`UPDATE profile_boosts SET starts_at = now() - interval '2 days', ends_at = now() - interval '2 days' + interval '30 minutes' WHERE user_id = $1`, u)
	if _, err := w.svc.ActivateBoost(w.ctx, u); err != ErrInsufficientCredits {
		t.Fatalf("no credits → refused, got %v", err)
	}
	w.exec(`INSERT INTO credit_ledger (user_id, amount_minor, reason) VALUES ($1, 5000, 'test')`, u)
	st, err = w.svc.ActivateBoost(w.ctx, u)
	if err != nil || !st.Active || st.BalanceMinor != 5000-BoostCostMinor {
		t.Fatalf("paid boost charges ₹19: %+v err=%v", st, err)
	}

	// a double tap can't charge twice
	v := w.user("racer")
	w.exec(`INSERT INTO profile_boosts (user_id, starts_at, ends_at) VALUES ($1, now() - interval '3 days', now() - interval '3 days' + interval '30 minutes')`, v)
	w.exec(`INSERT INTO credit_ledger (user_id, amount_minor, reason) VALUES ($1, 10000, 'test')`, v)
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := w.svc.ActivateBoost(w.ctx, v); done <- err }()
	}
	e1, e2 := <-done, <-done
	if (e1 == nil) == (e2 == nil) {
		t.Fatalf("exactly one of two concurrent activations wins: %v / %v", e1, e2)
	}
	var bal int64
	w.pool.QueryRow(w.ctx, `SELECT sum(amount_minor) FROM credit_ledger WHERE user_id=$1`, v).Scan(&bal)
	if bal != 10000-BoostCostMinor {
		t.Fatalf("charged exactly once, balance=%d", bal)
	}
}
