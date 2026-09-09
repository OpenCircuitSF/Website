package interests

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// waitUntilBlockedBy polls obs -- a third connection, independent of both
// racing sessions -- until the server itself reports some other backend
// waiting on blockerPID, and returns that backend's pid and query text.
//
// This is what makes the race test deterministic rather than timing
// dependent. It contains no sleep-and-hope: the blocking transaction holds
// its lock until the test commits it, so the condition polled for is
// guaranteed to become true and to stay true once it does. If it somehow
// does not, the test fails here with an explicit message rather than
// proceeding to assert against a run in which nothing actually raced -- a
// green result from a race test that never raced would be worse than no
// test at all.
func waitUntilBlockedBy(t *testing.T, obs *pgx.Conn, blockerPID uint32, since time.Time) (uint32, string, time.Duration) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		var pid uint32
		var q string
		err := obs.QueryRow(ctx,
			`SELECT pid, left(regexp_replace(query, '\s+', ' ', 'g'), 78)
			   FROM pg_stat_activity
			  WHERE $1 = ANY (pg_blocking_pids(pid))`, blockerPID).Scan(&pid, &q)
		if err == nil {
			return pid, q, time.Since(since)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("observer query: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no backend ever blocked on pid %d: the two sessions did not interleave, so this test proved nothing", blockerPID)
	return 0, "", 0
}

// TestDelete_RaceWithConcurrentDeleteReportsNotFound pins the fix for the
// regression #0474's first review measured: two admins deleting the same
// unused interest at the same time must get 404, not 500.
//
// Delete is one statement, so under READ COMMITTED its outer SELECT reads
// the pre-statement snapshot while the DELETE inside the CTE re-checks the
// target row after blocking on the concurrent writer. The result is that
// the DELETE affects zero rows while the SELECT still reports the row
// present and unreferenced, falling through to deleteOutcomeAfterRace.
// Without that helper Delete returns a bare error matching no sentinel, and
// the admin handler's default case turns a correct 404 into a 500. Nothing
// else in the suite reaches deleteOutcomeAfterRace, so without this test the
// helper reads as dead code on an unreachable path -- which is exactly the
// belief that produced the regression in the first place.
func TestDelete_RaceWithConcurrentDeleteReportsNotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()

	// A throwaway row of this test's own making, never a literal or seeded id.
	slug := testSlug(t, pool)
	var id int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO interests (slug, name, active) VALUES ($1, 'zz-test race probe', true) RETURNING id`,
		slug).Scan(&id); err != nil {
		t.Fatalf("seed interest: %v", err)
	}

	dsn := pool.Config().ConnString()

	obs, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("observer connect: %v", err)
	}
	defer obs.Close(ctx)

	// Session A: deletes the same row and holds the transaction open, so its
	// row lock is held for as long as this test wants it held.
	connA, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("session A connect: %v", err)
	}
	defer connA.Close(ctx)
	pidA := connA.PgConn().PID()
	txA, err := connA.Begin(ctx)
	if err != nil {
		t.Fatalf("session A begin: %v", err)
	}
	if _, err := txA.Exec(ctx, `DELETE FROM interests WHERE id = $1`, id); err != nil {
		t.Fatalf("session A delete: %v", err)
	}

	// Session B: the real production code path, which will block on A.
	type outcome struct {
		err error
		at  time.Time
	}
	done := make(chan outcome, 1)
	start := time.Now()
	go func() {
		done <- outcome{store.Delete(ctx, id), time.Now()}
	}()

	pidB, queryB, waited := waitUntilBlockedBy(t, obs, pidA, start)
	t.Logf("observed from a third connection: pid %d blocked by pid %d after %s running %s",
		pidB, pidA, waited.Truncate(time.Millisecond), queryB)

	select {
	case got := <-done:
		t.Fatalf("Delete returned (%v) while session A still held the lock: the sessions did not race", got.err)
	default:
	}

	if err := txA.Commit(ctx); err != nil {
		t.Fatalf("session A commit: %v", err)
	}

	got := <-done
	if !errors.Is(got.err, ErrNotFound) {
		t.Fatalf("Delete after losing the race: got %v, want ErrNotFound so the admin handler answers 404", got.err)
	}

	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM interests WHERE id = $1)`, id).Scan(&exists); err != nil {
		t.Fatalf("existence check: %v", err)
	}
	if exists {
		t.Fatalf("interest %d still present after both deletes", id)
	}
}
