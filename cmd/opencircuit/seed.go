package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
)

// seed bootstraps a fresh install: it ensures the admin user (from ADMIN_EMAIL)
// exists. Idempotent, so `opencircuit seed` is safe to re-run without creating
// duplicate rows.
//
// This is a minimal placeholder pending #0010 ("Add the seed command for the
// bootstrap admin"), which extends this with the fuller bootstrap behavior the
// PRD describes. The shortener's test-link seeding step (ensureTestLink) was
// removed with internal/links in #0002 — this project has no links table.
func seed() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	email := strings.ToLower(strings.TrimSpace(cfg.AdminEmail))
	if email == "" {
		return errors.New("seed: ADMIN_EMAIL is empty")
	}

	adminID, err := ensureAdminUser(ctx, pool, email)
	if err != nil {
		return err
	}

	fmt.Printf("Seed admin: %s (id=%d)\n", email, adminID)
	fmt.Println("Hint: to enroll the admin passkey, use \"Recover account\" on the login page (not Register).")
	return nil
}

// ensureAdminUser inserts the admin user if it does not already exist and
// returns its id. The insert uses ON CONFLICT (email) DO NOTHING for
// idempotency; when a row already exists it is reused and (re)promoted to an
// active admin so re-running seed converges on the intended state.
func ensureAdminUser(ctx context.Context, pool *pgxpool.Pool, email string) (int64, error) {
	// Insert-or-ignore. RETURNING yields no row when the conflict fires, so the
	// id is fetched with a follow-up SELECT below.
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, is_admin, active, created_at)
		 VALUES ($1, TRUE, TRUE, now())
		 ON CONFLICT (email) DO NOTHING`,
		email,
	); err != nil {
		return 0, fmt.Errorf("seed: inserting admin user: %w", err)
	}

	// Ensure an existing user is an active admin, then read back the id. This
	// also covers the case where the row pre-existed as a non-admin.
	var id int64
	if err := pool.QueryRow(ctx,
		`UPDATE users SET is_admin = TRUE, active = TRUE
		 WHERE email = $1
		 RETURNING id`,
		email,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("seed: loading admin user: %w", err)
	}

	return id, nil
}
