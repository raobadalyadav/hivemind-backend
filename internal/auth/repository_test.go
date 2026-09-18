package auth

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
		t.Skipf("skipping: cannot connect to postgres: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("skipping: postgres not reachable: %v", err)
	}
	return pool
}

func uniqueEmail(t *testing.T, label string) string {
	t.Helper()
	return "auth-test-" + label + "-" + time.Now().Format("150405.000000000") + "@example.com"
}

// uniqueSub returns a provider_user_id that won't collide with a previous
// test run — these tests commit to the real dev DB, not a rolled-back
// transaction, so hardcoded literals would fail on a second run.
func uniqueSub(t *testing.T, label string) string {
	t.Helper()
	return "sub-" + label + "-" + time.Now().Format("150405.000000000")
}

func TestRepository_CreateUserFromOAuth_FindByProviderIdentity(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	email := uniqueEmail(t, "oauth")
	sub := uniqueSub(t, "oauth")
	userID, err := repo.CreateUserFromOAuth(ctx, email, true, "google", sub, email)
	if err != nil {
		t.Fatalf("CreateUserFromOAuth: %v", err)
	}
	if userID == "" {
		t.Fatal("expected non-empty user id")
	}

	u, err := repo.FindByProviderIdentity(ctx, "google", sub)
	if err != nil {
		t.Fatalf("FindByProviderIdentity: %v", err)
	}
	if u.ID != userID {
		t.Errorf("expected user id %q, got %q", userID, u.ID)
	}
	if u.Status != "active" {
		t.Errorf("expected status 'active', got %q", u.Status)
	}

	// A different provider_user_id for the same provider should not match.
	if _, err := repo.FindByProviderIdentity(ctx, "google", uniqueSub(t, "unrelated")); err != ErrUserNotFound {
		t.Errorf("expected ErrUserNotFound for unrelated provider_user_id, got %v", err)
	}
}

func TestRepository_LinkOAuthIdentity_AddsSecondProvider(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	email := uniqueEmail(t, "link")
	googleSub := uniqueSub(t, "link-google")
	userID, err := repo.CreateUserFromOAuth(ctx, email, true, "google", googleSub, email)
	if err != nil {
		t.Fatalf("CreateUserFromOAuth: %v", err)
	}

	appleSub := uniqueSub(t, "link-apple")
	if err := repo.LinkOAuthIdentity(ctx, userID, "apple", appleSub, email); err != nil {
		t.Fatalf("LinkOAuthIdentity: %v", err)
	}

	u, err := repo.FindByProviderIdentity(ctx, "apple", appleSub)
	if err != nil {
		t.Fatalf("FindByProviderIdentity after link: %v", err)
	}
	if u.ID != userID {
		t.Errorf("expected linked identity to resolve to same user %q, got %q", userID, u.ID)
	}
}

func TestRepository_RecoveryEmailAndCodeRoundTrip(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)

	email := uniqueEmail(t, "recovery")
	userID, err := repo.CreateUserFromOAuth(ctx, email, true, "google", uniqueSub(t, "recovery"), email)
	if err != nil {
		t.Fatalf("CreateUserFromOAuth: %v", err)
	}

	recoveryEmail := uniqueEmail(t, "recovery-backup")
	if err := repo.SetPendingRecoveryEmail(ctx, userID, recoveryEmail); err != nil {
		t.Fatalf("SetPendingRecoveryEmail: %v", err)
	}

	// Not verified yet — must not be findable via FindByVerifiedRecoveryEmail.
	if _, err := repo.FindByVerifiedRecoveryEmail(ctx, recoveryEmail); err != ErrUserNotFound {
		t.Errorf("expected ErrUserNotFound before verification, got %v", err)
	}

	codeHash := "test-hash-" + time.Now().Format("150405.000000000")
	if err := repo.CreateRecoveryCode(ctx, userID, codeHash, purposeVerifyEmail, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("CreateRecoveryCode: %v", err)
	}

	consumedBy, err := repo.ConsumeRecoveryCode(ctx, codeHash, purposeVerifyEmail)
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if consumedBy != userID {
		t.Errorf("expected code owner %q, got %q", userID, consumedBy)
	}

	// Single-use: consuming again must fail.
	if _, err := repo.ConsumeRecoveryCode(ctx, codeHash, purposeVerifyEmail); err != ErrRecoveryCodeInvalid {
		t.Errorf("expected ErrRecoveryCodeInvalid on reuse, got %v", err)
	}

	if err := repo.MarkRecoveryEmailVerified(ctx, userID); err != nil {
		t.Fatalf("MarkRecoveryEmailVerified: %v", err)
	}

	u, err := repo.FindByVerifiedRecoveryEmail(ctx, recoveryEmail)
	if err != nil {
		t.Fatalf("FindByVerifiedRecoveryEmail after verification: %v", err)
	}
	if u.ID != userID {
		t.Errorf("expected user id %q, got %q", userID, u.ID)
	}
}
