// Command devtoken prints a user id and access token for signing in to the
// mobile app's "Developer sign-in" against a LOCAL backend, without Google or
// Apple. It finds or creates a user by email and signs a token with the same
// JWT_SECRET the API uses.
//
//	go run ./cmd/devtoken -email dev@hivemind.local -onboarded
//
// Development only: it refuses to run unless APP_ENV is empty or "dev".
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hivemind/backend/config"
	"github.com/hivemind/backend/pkg/security"
)

func main() {
	email := flag.String("email", "dev@hivemind.local", "user to find or create")
	name := flag.String("name", "Dev User", "display name for a newly created user")
	ttl := flag.Duration("ttl", 24*time.Hour, "token lifetime")
	onboarded := flag.Bool("onboarded", false, "give the user a city and 5 interests (if missing) so the app skips onboarding")
	flag.Parse()

	if env := os.Getenv("APP_ENV"); env != "" && env != "dev" {
		fmt.Fprintln(os.Stderr, "devtoken refuses to run when APP_ENV="+env)
		os.Exit(1)
	}
	cfg := config.Load()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		fatal(err)
	}
	defer pool.Close()

	var id string
	err = pool.QueryRow(ctx, `SELECT id::text FROM users WHERE email = $1`, *email).Scan(&id)
	if err != nil {
		if err := pool.QueryRow(ctx, `INSERT INTO users (email, age_verified) VALUES ($1, true) RETURNING id::text`, *email).Scan(&id); err != nil {
			fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO user_profiles (user_id, display_name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, *name); err != nil {
			fatal(err)
		}
	}
	if *onboarded { // fills only what's missing, so it's safe on an existing user
		if _, err := pool.Exec(ctx, `UPDATE users SET city_id = COALESCE(city_id, (SELECT id FROM cities WHERE active ORDER BY name LIMIT 1)) WHERE id = $1`, id); err != nil {
			fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE user_profiles SET interests = (SELECT array_agg(name) FROM (SELECT name FROM interests ORDER BY name LIMIT 5) x) WHERE user_id = $1 AND cardinality(interests) < 5`, id); err != nil {
			fatal(err)
		}
	}

	token, _, err := security.NewTokenIssuer(cfg.JWTSecret, *ttl).Issue(id, "user")
	if err != nil {
		fatal(err)
	}
	fmt.Printf("USER_ID=%s\nTOKEN=%s\n", id, token)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "devtoken:", err)
	os.Exit(1)
}
