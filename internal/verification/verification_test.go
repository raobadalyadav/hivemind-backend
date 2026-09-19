package verification

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/internal/admin"
	"github.com/hivemind/backend/pkg/media/mediatest"
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
		"vf-"+label+"-"+time.Now().Format("150405.000000000")+"@example.com").Scan(&id); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	pool.Exec(context.Background(), `INSERT INTO user_profiles (user_id, display_name) VALUES ($1,$2)`, id, label)
	return id
}

func TestVerification_LifecycleAndAdminReview(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	fm := mediatest.New(pool)
	svc := NewService(pool).WithMedia(fm)
	adm := admin.NewService(admin.NewRepository(pool)).WithMediaBaseURL("http://host:8080/")
	user, other, reviewer := seedUser(t, pool, "user"), seedUser(t, pool, "other"), seedUser(t, pool, "reviewer")

	st, _ := svc.Get(ctx, user)
	if st.Status != "none" || st.Verified {
		t.Fatalf("starts unverified: %+v", st)
	}
	if _, err := svc.Submit(ctx, user, fm.Add(user, "image").ID); err != ErrNoChallenge {
		t.Fatalf("can't submit without a challenge, got %v", err)
	}

	st, err := svc.Start(ctx, user)
	if err != nil || st.Status != "issued" || st.Challenge == "" {
		t.Fatalf("start: %+v err=%v", st, err)
	}
	valid := false
	for _, p := range Poses {
		valid = valid || p == st.Challenge
	}
	if !valid || !st.ExpiresAt.After(time.Now()) {
		t.Fatalf("the pose is one of the known ones and expires in the future: %+v", st)
	}
	again, _ := svc.Start(ctx, user)
	if again.Challenge != st.Challenge {
		t.Fatal("starting twice re-shows the same open challenge (one per user)")
	}

	if _, err := svc.Submit(ctx, user, fm.Add(other, "image").ID); err != ErrInvalidInput {
		t.Fatalf("someone else's photo can't be my selfie, got %v", err)
	}
	if _, err := svc.Submit(ctx, user, fm.Add(user, "video").ID); err != ErrInvalidInput {
		t.Fatalf("a video isn't a selfie, got %v", err)
	}
	selfie := fm.Add(user, "image")
	st, err = svc.Submit(ctx, user, selfie.ID)
	if err != nil || st.Status != "pending" {
		t.Fatalf("submit: %+v err=%v", st, err)
	}
	if _, err := svc.Submit(ctx, user, fm.Add(user, "image").ID); err != ErrNoChallenge {
		t.Fatalf("no second submission while one is pending, got %v", err)
	}

	queue, err := adm.ListVerificationRequests(ctx, 100)
	var req admin.VerificationItem
	for _, q := range queue {
		if q.UserID == user {
			req = q
		}
	}
	if err != nil || req.ID == "" || req.Challenge != st.Challenge || req.UserName != "user" || req.ObjectKey == "" {
		t.Fatalf("the admin sees the selfie with its challenge: %+v err=%v", req, err)
	}
	if want := "http://host:8080/media/u/"; len(req.ObjectKey) < len(want) || req.ObjectKey[:len(want)] != want {
		t.Fatalf("selfie is returned as a URL: %s", req.ObjectKey)
	}

	if err := adm.ReviewVerification(ctx, req.ID, false, "Face not visible.", reviewer); err != nil {
		t.Fatalf("reject: %v", err)
	}
	st, _ = svc.Get(ctx, user)
	if st.Status != "rejected" || st.RejectReason != "Face not visible." || st.Verified {
		t.Fatalf("the user sees the rejection reason: %+v", st)
	}
	var detached bool
	pool.QueryRow(ctx, `SELECT media_id IS NULL FROM verification_requests WHERE id = $1`, req.ID).Scan(&detached)
	if !detached {
		t.Fatal("the selfie is detached after review so the GC can delete it")
	}
	if err := adm.ReviewVerification(ctx, req.ID, true, "", reviewer); err != admin.ErrNotFound {
		t.Fatalf("a decided request can't be decided again, got %v", err)
	}

	// try again → approve
	if st, err = svc.Start(ctx, user); err != nil || st.Status != "issued" {
		t.Fatalf("after a rejection they can start over: %+v %v", st, err)
	}
	svc.Submit(ctx, user, fm.Add(user, "image").ID)
	queue, _ = adm.ListVerificationRequests(ctx, 100)
	for _, q := range queue {
		if q.UserID == user {
			req = q
		}
	}
	if err := adm.ReviewVerification(ctx, req.ID, true, "", reviewer); err != nil {
		t.Fatalf("approve: %v", err)
	}
	st, _ = svc.Get(ctx, user)
	if !st.Verified || st.Status != "approved" {
		t.Fatalf("approved → blue tick: %+v", st)
	}
	if _, err := svc.Start(ctx, user); err != ErrAlreadyVerified {
		t.Fatalf("no need to verify twice, got %v", err)
	}
	var audits, notes int
	pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE subject_id = $1 AND action LIKE 'verification_%'`, user).Scan(&audits)
	pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type = 'NOTIFY_USER' AND subject_id = $1`, user).Scan(&notes)
	if audits != 2 || notes != 2 {
		t.Fatalf("both decisions are audited and notified: audits=%d notes=%d", audits, notes)
	}
}

func TestVerification_ChallengeExpires(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	fm := mediatest.New(pool)
	svc := NewService(pool).WithMedia(fm)
	u := seedUser(t, pool, "slow")
	svc.Start(ctx, u)
	pool.Exec(ctx, `UPDATE verification_requests SET expires_at = now() - interval '1 minute' WHERE user_id = $1`, u)

	if st, _ := svc.Get(ctx, u); st.Status != "none" {
		t.Fatalf("an expired challenge is gone: %+v", st)
	}
	if _, err := svc.Submit(ctx, u, fm.Add(u, "image").ID); err != ErrNoChallenge {
		t.Fatalf("can't submit against an expired challenge, got %v", err)
	}
	if st, err := svc.Start(ctx, u); err != nil || st.Status != "issued" {
		t.Fatalf("they can start again: %+v %v", st, err)
	}
}
