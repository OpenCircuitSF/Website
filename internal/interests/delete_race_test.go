package interests

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
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
// #0477 moved Delete from a single atomic statement to a transaction that
// locks the interests row with FOR UPDATE before checking it, so this test's
// session B now blocks on that lock statement rather than on the delete CTE.
// Once session A's own DELETE commits, session B's lock statement finds no
// row left to lock and returns pgx.ErrNoRows, which Delete maps directly to
// ErrNotFound -- the same sentinel the admin handler needs to answer 404
// rather than falling into its default case and answering 500.
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

// TestDelete_RaceWithConcurrentTagRefusesRatherThanCascading pins the fix for
// #0477: an admin tagging a workshop, targeting a campaign, or subscribing to
// an interest at the same moment another admin deletes it must not lose that
// reference silently. Before #0477, Delete's own NOT EXISTS check ran from a
// snapshot taken before the tagging transaction committed, so the delete
// proceeded, reported success, and the just-committed reference was cascaded
// away by the referencing table's ON DELETE CASCADE. #0477 makes Delete lock
// the interests row with FOR UPDATE before checking it, so the tagging
// transaction's insert -- which takes a conflicting FOR KEY SHARE lock on the
// same row to enforce its foreign key -- cannot commit underneath the
// delete's snapshot: Delete blocks until the insert commits, then its check
// sees the now-committed reference and refuses instead of cascading it away.
//
// Table-driven over all three referencing tables per this issue's criterion
// 3, so the fix is proven on subscriber_interests and campaign_interests too,
// not assumed from the workshop case alone -- the planning pass's own
// reproduction found the window identical on all three.
func TestDelete_RaceWithConcurrentTagRefusesRatherThanCascading(t *testing.T) {
	cases := []struct {
		name string
		// link inserts a throwaway parent row and the join row referencing
		// interestID, both via tx so the reference is held uncommitted until
		// the test itself commits tx. Returns the parent row's id, for
		// cleanup once the subtest is done with it.
		link func(t *testing.T, ctx context.Context, tx pgx.Tx, interestID int64) (parentID int64)
		// cleanupParent deletes the parent row seeded by link; the join row
		// follows via that table's ON DELETE CASCADE.
		cleanupParent func(t *testing.T, pool *pgxpool.Pool, parentID int64)
		// refExists reports whether the join row still references
		// interestID -- the thing this test exists to prove was NOT
		// cascaded away by a successful delete.
		refExists func(t *testing.T, pool *pgxpool.Pool, interestID int64) bool
		wantErr   error
	}{
		{
			name: "workshop_interests",
			link: func(t *testing.T, ctx context.Context, tx pgx.Tx, interestID int64) int64 {
				t.Helper()
				slug := fmt.Sprintf("zz-test-workshop-race-%d", testdb.Unique())
				var workshopID int64
				if err := tx.QueryRow(ctx,
					`INSERT INTO workshops (slug, title) VALUES ($1, 'zz-test workshop') RETURNING id`,
					slug,
				).Scan(&workshopID); err != nil {
					t.Fatalf("seed workshop in tx: %v", err)
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO workshop_interests (workshop_id, interest_id) VALUES ($1, $2)`,
					workshopID, interestID,
				); err != nil {
					t.Fatalf("link workshop to interest in tx: %v", err)
				}
				return workshopID
			},
			cleanupParent: func(t *testing.T, pool *pgxpool.Pool, parentID int64) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, parentID); err != nil {
					t.Errorf("cleanup workshop %d: %v", parentID, err)
				}
			},
			refExists: func(t *testing.T, pool *pgxpool.Pool, interestID int64) bool {
				t.Helper()
				var exists bool
				if err := pool.QueryRow(context.Background(),
					`SELECT EXISTS (SELECT 1 FROM workshop_interests WHERE interest_id = $1)`, interestID,
				).Scan(&exists); err != nil {
					t.Fatalf("workshop_interests existence check: %v", err)
				}
				return exists
			},
			wantErr: ErrHasWorkshops,
		},
		{
			name: "campaign_interests",
			link: func(t *testing.T, ctx context.Context, tx pgx.Tx, interestID int64) int64 {
				t.Helper()
				slug := fmt.Sprintf("zz-test-campaign-race-%d", testdb.Unique())
				var campaignID int64
				if err := tx.QueryRow(ctx,
					`INSERT INTO email_campaigns (name, subject, body_md, slug)
					 VALUES ('zz-test campaign', 'zz-test subject', 'body', $1) RETURNING id`,
					slug,
				).Scan(&campaignID); err != nil {
					t.Fatalf("seed campaign in tx: %v", err)
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO campaign_interests (campaign_id, interest_id) VALUES ($1, $2)`,
					campaignID, interestID,
				); err != nil {
					t.Fatalf("link campaign to interest in tx: %v", err)
				}
				return campaignID
			},
			cleanupParent: func(t *testing.T, pool *pgxpool.Pool, parentID int64) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `DELETE FROM email_campaigns WHERE id = $1`, parentID); err != nil {
					t.Errorf("cleanup campaign %d: %v", parentID, err)
				}
			},
			refExists: func(t *testing.T, pool *pgxpool.Pool, interestID int64) bool {
				t.Helper()
				var exists bool
				if err := pool.QueryRow(context.Background(),
					`SELECT EXISTS (SELECT 1 FROM campaign_interests WHERE interest_id = $1)`, interestID,
				).Scan(&exists); err != nil {
					t.Fatalf("campaign_interests existence check: %v", err)
				}
				return exists
			},
			wantErr: ErrHasCampaigns,
		},
		{
			name: "subscriber_interests",
			link: func(t *testing.T, ctx context.Context, tx pgx.Tx, interestID int64) int64 {
				t.Helper()
				email := fmt.Sprintf("zz-test-sub-race-%d@example.com", testdb.Unique())
				var subID int64
				if err := tx.QueryRow(ctx,
					`INSERT INTO subscribers (email, manage_token) VALUES ($1, $2) RETURNING id`,
					email, fmt.Sprintf("zz-token-race-%d", testdb.Unique()),
				).Scan(&subID); err != nil {
					t.Fatalf("seed subscriber in tx: %v", err)
				}
				if _, err := tx.Exec(ctx,
					`INSERT INTO subscriber_interests (subscriber_id, interest_id) VALUES ($1, $2)`,
					subID, interestID,
				); err != nil {
					t.Fatalf("link subscriber to interest in tx: %v", err)
				}
				return subID
			},
			cleanupParent: func(t *testing.T, pool *pgxpool.Pool, parentID int64) {
				t.Helper()
				if _, err := pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, parentID); err != nil {
					t.Errorf("cleanup subscriber %d: %v", parentID, err)
				}
			},
			refExists: func(t *testing.T, pool *pgxpool.Pool, interestID int64) bool {
				t.Helper()
				var exists bool
				if err := pool.QueryRow(context.Background(),
					`SELECT EXISTS (SELECT 1 FROM subscriber_interests WHERE interest_id = $1)`, interestID,
				).Scan(&exists); err != nil {
					t.Fatalf("subscriber_interests existence check: %v", err)
				}
				return exists
			},
			wantErr: ErrHasSubscribers,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := testPool(t)
			store := NewStore(pool)
			ctx := context.Background()

			slug := testSlug(t, pool)
			created, err := store.Create(ctx, slug, "zz-test tag race", nil, 0)
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			dsn := pool.Config().ConnString()

			obs, err := pgx.Connect(ctx, dsn)
			if err != nil {
				t.Fatalf("observer connect: %v", err)
			}
			defer obs.Close(ctx)

			// Session A: the concurrent tagger. Inserts the reference and
			// holds the transaction open, so the FOR KEY SHARE lock its
			// insert takes on the interests row (to enforce the foreign
			// key) is held for as long as this test wants it held.
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
			parentID := tc.link(t, ctx, txA, created.ID)
			t.Cleanup(func() { tc.cleanupParent(t, pool, parentID) })

			// Session B: the real production code path, which will block on
			// A's FOR KEY SHARE lock when it tries to take FOR UPDATE.
			done := make(chan error, 1)
			start := time.Now()
			go func() {
				done <- store.Delete(ctx, created.ID)
			}()

			pidB, queryB, waited := waitUntilBlockedBy(t, obs, pidA, start)
			t.Logf("observed from a third connection: pid %d blocked by pid %d after %s running %s",
				pidB, pidA, waited.Truncate(time.Millisecond), queryB)

			select {
			case got := <-done:
				t.Fatalf("Delete returned (%v) while session A still held its lock: the sessions did not race", got)
			default:
			}

			if err := txA.Commit(ctx); err != nil {
				t.Fatalf("session A commit: %v", err)
			}

			got := <-done
			if !errors.Is(got, tc.wantErr) {
				t.Fatalf("Delete after losing the race to a committed %s reference: got %v, want %v",
					tc.name, got, tc.wantErr)
			}

			// Both the interest and the reference must have survived. The
			// defect this test pins is the delete succeeding and the
			// just-committed reference being cascaded away underneath it.
			if _, err := store.GetByID(ctx, created.ID); err != nil {
				t.Fatalf("interest %d missing after refused delete: %v", created.ID, err)
			}
			if !tc.refExists(t, pool, created.ID) {
				t.Fatalf("%s reference to interest %d gone after refused delete -- cascaded away", tc.name, created.ID)
			}
		})
	}
}
