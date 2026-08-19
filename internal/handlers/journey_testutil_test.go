package handlers

// Shared test helpers for the public mailing-list journey batch
// (#0029-#0031: public_interests_test.go, confirm_test.go,
// preferences_test.go). Named distinctly from subscribe_test.go's own
// subscribeTestPool/truncateSubscribeTables so nothing in this batch depends
// on symbols defined in a file #0081 may be concurrently editing.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// journeyTestPool returns the package's single shared pool (opened once in
// TestMain — #0091) or skips if TEST_DATABASE_URL was unset, truncating
// subscribers/subscriber_interests/audit_log on entry only (never
// `interests` -- it carries the seeded taxonomy every test in this batch
// resolves slugs against, mirroring subscribeTestPool/interestsTestPool's
// shared convention).
func journeyTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	truncateJourneyTables(t, testDBPool)
	return testDBPool
}

func truncateJourneyTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`TRUNCATE subscriber_interests, subscribers, audit_log RESTART IDENTITY CASCADE`,
	); err != nil {
		t.Fatalf("truncate journey tables: %v", err)
	}
}

// journeySeededInterestID resolves a production taxonomy slug (seeded by
// migrations/000009) to its id.
func journeySeededInterestID(t *testing.T, pool *pgxpool.Pool, slug string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(),
		`SELECT id FROM interests WHERE slug = $1`, slug,
	).Scan(&id); err != nil {
		t.Fatalf("resolve seeded interest %q: %v (has migration 000009 run?)", slug, err)
	}
	return id
}

func journeyUniqueEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-journeytest-%d@example.com", time.Now().UnixNano())
}
