package communities

import (
	"context"
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

func seedUser(t *testing.T, pool *pgxpool.Pool, label string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO users (email) VALUES ($1) RETURNING id`,
		"cm-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

type fakeEnt struct{ granted bool }

func (f fakeEnt) HasEntitlement(context.Context, string, string) (bool, error) { return f.granted, nil }

func TestApprovalCommunity_PendingThenApprovedByOwnerOnly(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))

	owner, joiner := seedUser(t, pool, "owner"), seedUser(t, pool, "joiner")
	c, err := svc.CreateCommunity(ctx, &Community{Name: "Approval Club", OwnerID: owner, MembershipType: "approval"})
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	m, err := svc.JoinCommunity(ctx, c.ID, joiner)
	if err != nil || m.Status != "pending" {
		t.Fatalf("approval community → pending, got %+v err=%v", m, err)
	}
	reqs, err := svc.ListJoinRequests(ctx, c.ID, "pending", owner)
	if err != nil || len(reqs) != 1 {
		t.Fatalf("owner sees one pending request: %v err=%v", reqs, err)
	}
	if _, err := svc.ListJoinRequests(ctx, c.ID, "pending", joiner); err != ErrForbidden {
		t.Fatalf("a non-manager must not list requests, got %v", err)
	}
	if _, err := svc.RespondJoinRequest(ctx, reqs[0].ID, joiner, true); err != ErrForbidden {
		t.Fatalf("a non-manager must not approve, got %v", err)
	}
	if r, err := svc.RespondJoinRequest(ctx, reqs[0].ID, owner, true); err != nil || r.Status != "approved" {
		t.Fatalf("owner approves: %+v err=%v", r, err)
	}
	if role, _ := svc.repo.MemberRole(ctx, c.ID, joiner); role == "" {
		t.Fatal("approval must insert the member")
	}
	if _, err := svc.RespondJoinRequest(ctx, reqs[0].ID, owner, false); err != ErrAlreadyDecided {
		t.Fatalf("opposite decision must be refused, got %v", err)
	}
}

func TestPaidCommunity_RequiresEntitlement(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	owner, user := seedUser(t, pool, "powner"), seedUser(t, pool, "puser")

	if _, err := NewService(NewRepository(pool)).CreateCommunity(ctx,
		&Community{Name: "x", OwnerID: owner, MembershipType: "paid"}); err != ErrInvalidInput {
		t.Fatalf("paid without required_entitlement must be rejected, got %v", err)
	}
	product := "test_comm_" + time.Now().Format("150405.000000000")
	if _, err := pool.Exec(ctx, `INSERT INTO subscription_products (name, price_minor) VALUES ($1, 100)`, product); err != nil {
		t.Fatalf("seed product: %v", err)
	}
	no := NewService(NewRepository(pool)).WithEntitlements(fakeEnt{false})
	c, err := no.CreateCommunity(ctx, &Community{Name: "Paid Club", OwnerID: owner, MembershipType: "paid", RequiredEntitlement: product})
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	if _, err := no.JoinCommunity(ctx, c.ID, user); err != ErrEntitlementRequired {
		t.Fatalf("no entitlement → ErrEntitlementRequired, got %v", err)
	}
	yes := NewService(NewRepository(pool)).WithEntitlements(fakeEnt{true})
	if m, err := yes.JoinCommunity(ctx, c.ID, user); err != nil || m.Status != "active" {
		t.Fatalf("entitled user joins: %+v err=%v", m, err)
	}
}

func TestPrivateCommunity_HiddenUntilInvited(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	owner, stranger := seedUser(t, pool, "vowner"), seedUser(t, pool, "vstranger")

	c, err := svc.CreateCommunity(ctx, &Community{Name: "Secret Club", OwnerID: owner, MembershipType: "private"})
	if err != nil {
		t.Fatalf("CreateCommunity: %v", err)
	}
	if _, err := svc.GetCommunity(ctx, c.ID, stranger); err != ErrCommunityNotFound {
		t.Fatalf("private community must look nonexistent, got %v", err)
	}
	if _, err := svc.JoinCommunity(ctx, c.ID, stranger); err != ErrCommunityNotFound {
		t.Fatalf("uninvited join must look nonexistent, got %v", err)
	}
	if err := svc.InviteToCommunity(ctx, c.ID, stranger, stranger); err != ErrForbidden {
		t.Fatalf("a non-manager must not invite, got %v", err)
	}
	if err := svc.InviteToCommunity(ctx, c.ID, owner, stranger); err != nil {
		t.Fatalf("owner invites: %v", err)
	}
	if _, err := svc.GetCommunity(ctx, c.ID, stranger); err != nil {
		t.Fatalf("invitee can see it: %v", err)
	}
	if m, err := svc.JoinCommunity(ctx, c.ID, stranger); err != nil || m.Status != "active" {
		t.Fatalf("invitee joins: %+v err=%v", m, err)
	}
}

func TestListCommunities_OnlyMineAndIsMember(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	owner, me := seedUser(t, pool, "cmowner"), seedUser(t, pool, "cmme")
	svc := NewService(NewRepository(pool))
	run := time.Now().Format("150405.000000000") // one suffix for the run, so the list can be filtered to this run's rows
	mk := func(name, typ string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO communities (name, owner_id, membership_type) VALUES ($1,$2,$3::community_type) RETURNING id`, name+run, owner, typ).Scan(&id); err != nil {
			t.Fatalf("community: %v", err)
		}
		return id
	}
	joined, notJoined, private := mk("Joined ", "public"), mk("NotJoined ", "public"), mk("Private ", "private")
	pool.Exec(ctx, `INSERT INTO community_members (community_id, user_id) VALUES ($1,$2),($3,$2)`, joined, me, private)

	all, err := svc.ListCommunities(ctx, "", "", run, me, false) // the list is capped, so look only at this run's communities
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	member := map[string]bool{}
	for _, c := range all {
		member[c.ID] = c.IsMember
	}
	if !member[joined] || member[notJoined] {
		t.Errorf("is_member flags: joined=%v notJoined=%v", member[joined], member[notJoined])
	}
	if _, seen := member[private]; seen {
		t.Error("a private community stays out of the public list")
	}
	mine, _ := svc.ListCommunities(ctx, "", "", "", me, true)
	ids := map[string]bool{}
	for _, c := range mine {
		ids[c.ID] = true
		if !c.IsMember {
			t.Errorf("only_mine returns members: %s", c.Name)
		}
	}
	if !ids[joined] || !ids[private] || ids[notJoined] {
		t.Errorf("only_mine = my communities incl. my private one: %v", ids)
	}
	if _, err := svc.ListCommunities(ctx, "", "", "", "", true); err != ErrInvalidInput {
		t.Errorf("only_mine needs a caller: %v", err)
	}
}

func TestJoinApprovalCommunity_PendingSurvivesReopenAndDeclinedIsExplained(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	owner, me := seedUser(t, pool, "apowner"), seedUser(t, pool, "apme")
	svc := NewService(NewRepository(pool))
	var comm string
	if err := pool.QueryRow(ctx, `INSERT INTO communities (name, owner_id, membership_type) VALUES ($1,$2,'approval') RETURNING id`, "Approval Club "+time.Now().Format("150405.000000000"), owner).Scan(&comm); err != nil {
		t.Fatalf("community: %v", err)
	}
	m, err := svc.JoinCommunity(ctx, comm, me)
	if err != nil || m.Status != "pending" {
		t.Fatalf("first join files a request: %+v %v", m, err)
	}
	c, err := svc.GetCommunity(ctx, comm, me)
	if err != nil || c.IsMember || !c.JoinPending {
		t.Fatalf("reopening shows 'request sent': member=%v pending=%v err=%v", c != nil && c.IsMember, c != nil && c.JoinPending, err)
	}
	list, _ := svc.ListCommunities(ctx, "", "", "Approval Club", me, false)
	found := false
	for _, x := range list {
		if x.ID == comm {
			found = true
			if !x.JoinPending {
				t.Error("the list carries join_pending too")
			}
		}
	}
	if !found {
		t.Fatal("approval community is listed")
	}
	// the owners decline
	if _, err := pool.Exec(ctx, `UPDATE community_join_requests SET status = 'rejected' WHERE community_id = $1 AND user_id = $2`, comm, me); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if _, err := svc.JoinCommunity(ctx, comm, me); err != ErrJoinDeclined {
		t.Errorf("a declined request is explained, not silently ignored: %v", err)
	}
	if c, _ := svc.GetCommunity(ctx, comm, me); c.JoinPending {
		t.Error("declined is no longer pending")
	}
}

func TestCommunity_NotifiesOwnerAndDecidedUser(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	owner, asker, joiner := seedUser(t, pool, "no"), seedUser(t, pool, "na"), seedUser(t, pool, "nj")
	count := func(user, typ string) int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND type=$2`, user, typ).Scan(&n)
		return n
	}
	appr, _ := svc.CreateCommunity(ctx, &Community{Name: "Notify Approval " + time.Now().Format("150405.000000000"), OwnerID: owner, MembershipType: "approval"})
	open, _ := svc.CreateCommunity(ctx, &Community{Name: "Notify Open " + time.Now().Format("150405.000000000"), OwnerID: owner, MembershipType: "public"})

	svc.JoinCommunity(ctx, appr.ID, asker)
	svc.JoinCommunity(ctx, appr.ID, asker) // repeat: no second notification
	if count(owner, "community_join_request") != 1 {
		t.Fatalf("owner is told of a join request once, got %d", count(owner, "community_join_request"))
	}
	reqs, _ := svc.ListJoinRequests(ctx, appr.ID, "pending", owner)
	if _, err := svc.RespondJoinRequest(ctx, reqs[0].ID, owner, true); err != nil {
		t.Fatal(err)
	}
	if count(asker, "community_approved") != 1 {
		t.Fatal("the requester is told they were approved")
	}
	svc.JoinCommunity(ctx, open.ID, joiner)
	if count(owner, "community_member_joined") != 1 {
		t.Fatal("owner is told when someone joins an open community")
	}
}
