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
