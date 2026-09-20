package connections

import (
	"context"
	"testing"
)

func TestConnection_NotifiesBothSides(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	svc := NewService(NewRepository(pool))
	a, b, c := seedUser(t, pool, "na"), seedUser(t, pool, "nb"), seedUser(t, pool, "nc")
	count := func(user, typ string) int {
		var n int
		pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE user_id=$1 AND type=$2`, user, typ).Scan(&n)
		return n
	}

	req, err := svc.RequestConnection(ctx, &Connection{RequesterID: a, RecipientID: b})
	if err != nil {
		t.Fatal(err)
	}
	svc.RequestConnection(ctx, &Connection{RequesterID: a, RecipientID: b}) // repeat: still one
	if count(b, "connection_request") != 1 {
		t.Fatalf("recipient is told once, got %d", count(b, "connection_request"))
	}
	if st, id, _ := svc.GetStatus(ctx, a, b); st != "pending_outgoing" || id != req.ID {
		t.Fatalf("requester sees pending_outgoing: %s", st)
	}
	if st, _, _ := svc.GetStatus(ctx, b, a); st != "pending_incoming" {
		t.Fatalf("recipient sees pending_incoming: %s", st)
	}
	if st, _, _ := svc.GetStatus(ctx, a, c); st != "none" {
		t.Fatalf("strangers are none: %s", st)
	}
	if _, err := svc.RespondConnection(ctx, req.ID, b, true); err != nil {
		t.Fatal(err)
	}
	if count(a, "connection_accepted") != 1 {
		t.Fatal("the requester is told it was accepted")
	}
	if st, _, _ := svc.GetStatus(ctx, a, b); st != "connected" {
		t.Fatalf("connected: %s", st)
	}

	// mutual intent (both ask) accepts and tells the first asker
	x, err := svc.RequestConnection(ctx, &Connection{RequesterID: a, RecipientID: c})
	if err != nil || x.Status != "pending" {
		t.Fatal(err)
	}
	if y, err := svc.RequestConnection(ctx, &Connection{RequesterID: c, RecipientID: a}); err != nil || y.Status != "accepted" {
		t.Fatalf("mutual: %+v %v", y, err)
	}
	if count(a, "connection_accepted") != 2 {
		t.Fatalf("mutual acceptance notifies the first asker, got %d", count(a, "connection_accepted"))
	}
}
