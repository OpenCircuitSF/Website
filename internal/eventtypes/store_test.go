package eventtypes

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

func seedType(t *testing.T, store *Store, sortOrder int) EventType {
	t.Helper()
	slug := fmt.Sprintf("zz-test-%d", testdb.Unique())
	et, err := store.Create(context.Background(), slug, "Zz "+slug, sortOrder)
	if err != nil {
		t.Fatalf("seed type: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM workshops WHERE event_type_id = $1`, et.ID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM event_types WHERE id = $1`, et.ID)
	})
	return et
}

func seedWorkshop(t *testing.T, pool *pgxpool.Pool, typeID int64) {
	t.Helper()
	var id int64
	slug := fmt.Sprintf("zz-test-%d", testdb.Unique())
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO workshops (slug, title, event_type_id) VALUES ($1, $1, $2) RETURNING id`, slug, typeID).Scan(&id); err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, id) })
}

func seededDefault(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM event_types WHERE slug = 'workshop'`).Scan(&id); err != nil {
		t.Fatalf("finding seeded workshop type: %v", err)
	}
	return id
}

// restoreDefault puts the seeded default back, in two statements because the
// unique index refuses a single-statement swap.
func restoreDefault(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `UPDATE event_types SET is_default = FALSE WHERE is_default`)
		_, _ = pool.Exec(ctx, `UPDATE event_types SET is_default = TRUE WHERE slug = 'workshop'`)
	})
}

func wantConstraint(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != constraint {
		t.Fatalf("err = %v, want violation of %s", err, constraint)
	}
}

func TestSchemaRefusesSecondDefault(t *testing.T) {
	pool := testPool(t)
	et := seedType(t, NewStore(pool), 0)
	_, err := pool.Exec(context.Background(), `UPDATE event_types SET is_default = TRUE WHERE id = $1`, et.ID)
	wantConstraint(t, err, "event_types_one_default")
}

func TestSchemaRefusesDisabledDefault(t *testing.T) {
	pool := testPool(t)
	_, err := pool.Exec(context.Background(), `UPDATE event_types SET enabled = FALSE WHERE is_default`)
	wantConstraint(t, err, "event_types_default_enabled")
}

func TestSchemaRefusesDeletingTypeInUse(t *testing.T) {
	pool := testPool(t)
	et := seedType(t, NewStore(pool), 0)
	seedWorkshop(t, pool, et.ID)
	_, err := pool.Exec(context.Background(), `DELETE FROM event_types WHERE id = $1`, et.ID)
	wantConstraint(t, err, "workshops_event_type_id_fkey")
}

func TestCreateRejectsInvalidAndDuplicateSlug(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()
	if _, err := store.Create(ctx, "Not A Slug", "x", 0); !errors.Is(err, ErrInvalidSlug) {
		t.Errorf("invalid slug err = %v", err)
	}
	et := seedType(t, store, 0)
	if _, err := store.Create(ctx, et.Slug, "x", 0); !errors.Is(err, ErrDuplicateSlug) {
		t.Errorf("duplicate slug err = %v", err)
	}
}

func TestListAllOrdersBySortOrder(t *testing.T) {
	store := NewStore(testPool(t))
	last := seedType(t, store, 100002)
	first := seedType(t, store, 100001)
	all, err := store.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 7 || all[0].Slug != "workshop" || all[1].Slug != "hack-night" {
		t.Fatalf("seeded order wrong: %+v", all)
	}
	if all[len(all)-2].ID != first.ID || all[len(all)-1].ID != last.ID {
		t.Errorf("tail = %+v, want %d then %d", all[len(all)-2:], first.ID, last.ID)
	}
}

func TestUpdateRenamesReordersAndDisables(t *testing.T) {
	store := NewStore(testPool(t))
	et := seedType(t, store, 5)
	got, err := store.Update(context.Background(), et.ID, "Renamed", 6, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || got.SortOrder != 6 || got.Enabled || got.Slug != et.Slug {
		t.Errorf("updated = %+v", got)
	}
	if _, err := store.Update(context.Background(), -1, "x", 0, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id err = %v", err)
	}
}

func TestUpdateRefusesDisablingDefault(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	id := seededDefault(t, pool)
	cur, err := store.GetByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(context.Background(), id, cur.Name, cur.SortOrder, false); !errors.Is(err, ErrIsDefault) {
		t.Errorf("err = %v, want ErrIsDefault", err)
	}
}

func TestSetDefaultMovesTheFlag(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	restoreDefault(t, pool)
	et := seedType(t, store, 0)
	if err := store.SetDefault(context.Background(), et.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetByID(context.Background(), et.ID)
	old, _ := store.GetByID(context.Background(), seededDefault(t, pool))
	if !got.IsDefault || old.IsDefault {
		t.Errorf("new.IsDefault=%v old.IsDefault=%v", got.IsDefault, old.IsDefault)
	}
	if err := store.SetDefault(context.Background(), -1); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id err = %v", err)
	}
}

func TestSetDefaultRefusesDisabledType(t *testing.T) {
	store := NewStore(testPool(t))
	et := seedType(t, store, 0)
	if _, err := store.Update(context.Background(), et.ID, et.Name, 0, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefault(context.Background(), et.ID); !errors.Is(err, ErrDisabled) {
		t.Errorf("err = %v, want ErrDisabled", err)
	}
}

func TestSetDefaultConcurrentCallersLeaveOneDefault(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	restoreDefault(t, pool)
	a, b := seedType(t, store, 0), seedType(t, store, 0)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		id := a.ID
		if i%2 == 1 {
			id = b.ID
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- store.SetDefault(context.Background(), id)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("SetDefault: %v", err)
		}
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM event_types WHERE is_default`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("defaults = %d, want 1", n)
	}
}

func TestDeleteRefusesDefault(t *testing.T) {
	pool := testPool(t)
	if err := NewStore(pool).Delete(context.Background(), seededDefault(t, pool)); !errors.Is(err, ErrIsDefault) {
		t.Errorf("err = %v, want ErrIsDefault", err)
	}
}

func TestDeleteRefusesTypeInUse(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	et := seedType(t, store, 0)
	seedWorkshop(t, pool, et.ID)
	if err := store.Delete(context.Background(), et.ID); !errors.Is(err, ErrInUse) {
		t.Errorf("err = %v, want ErrInUse", err)
	}
}

func TestDeleteRemovesUnusedType(t *testing.T) {
	store := NewStore(testPool(t))
	et := seedType(t, store, 0)
	ctx := context.Background()
	if err := store.Delete(ctx, et.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetByID(ctx, et.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete err = %v", err)
	}
	if err := store.Delete(ctx, et.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete err = %v", err)
	}
}

func TestUsageCounts(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	used, unused := seedType(t, store, 0), seedType(t, store, 0)
	seedWorkshop(t, pool, used.ID)
	seedWorkshop(t, pool, used.ID)
	counts, err := store.UsageCounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts[used.ID] != 2 || counts[unused.ID] != 0 {
		t.Errorf("counts = %v", counts)
	}
}
