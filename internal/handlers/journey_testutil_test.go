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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// journeyTestPool returns the package's single shared pool (opened once in
// TestMain — #0091) or skips if TEST_DATABASE_URL was unset.
//
// #0091 round two: this used to TRUNCATE subscribers/subscriber_interests/
// audit_log on every call. It no longer does — every test in this batch
// already seeds through journeyUniqueEmail(t), so no two tests' rows
// collide, and the tables only need to start clean once (main_test.go's
// TestMain), not before every test. `interests` was never truncated here to
// begin with — it carries the seeded taxonomy every test in this batch
// resolves slugs against.
func journeyTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
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
	return fmt.Sprintf("zz-journeytest-%d@example.com", testdb.Unique())
}
