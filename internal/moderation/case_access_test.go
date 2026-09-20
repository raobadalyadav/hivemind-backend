package moderation

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGetCase_OnlyTheReporterOrStaff(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hivemind:hivemind@localhost:5432/hivemind?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil || pool.Ping(context.Background()) != nil {
		t.Skip("postgres not reachable")
	}
	defer pool.Close()
	ctx := context.Background()
	sfx := time.Now().Format("150405.000000000")
	var reporter, subject, stranger string
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "case-r-"+sfx+"@example.com").Scan(&reporter)
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "case-s-"+sfx+"@example.com").Scan(&subject)
	pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ($1) RETURNING id`, "case-x-"+sfx+"@example.com").Scan(&stranger)
	svc := NewService(NewRepository(pool))
	c, err := svc.SubmitReport(ctx, &Case{ReporterID: reporter, SubjectType: "user", SubjectID: subject, Reason: "spam"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetCase(ctx, c.ID, reporter, false); err != nil || got.ID != c.ID {
		t.Fatalf("the reporter sees their case: %v", err)
	}
	if _, err := svc.GetCase(ctx, c.ID, stranger, false); err != ErrCaseNotFound {
		t.Fatalf("a stranger must not read someone else's report, got %v", err)
	}
	if _, err := svc.GetCase(ctx, c.ID, stranger, true); err != nil {
		t.Fatalf("staff can read any case: %v", err)
	}
}

func TestSubmitReport_ValidatesWhatCanBeReported(t *testing.T) {
	svc := NewService(nil)
	for name, c := range map[string]*Case{
		"unknown type": {ReporterID: "a", SubjectType: "spaceship", SubjectID: "b", Reason: "x"},
		"no reason":    {ReporterID: "a", SubjectType: "post", SubjectID: "b", Reason: "   "},
		"huge reason":  {ReporterID: "a", SubjectType: "post", SubjectID: "b", Reason: string(make([]byte, 1001))},
		"yourself":     {ReporterID: "a", SubjectType: "user", SubjectID: "a", Reason: "x"},
	} {
		if _, err := svc.SubmitReport(context.Background(), c); err != ErrInvalidInput {
			t.Errorf("%s must be rejected, got %v", name, err)
		}
	}
}
