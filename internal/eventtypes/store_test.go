package eventtypes

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/defaults"
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

// restoreDefault puts the seeded default back, recreating the row if a test
// removed it.
func restoreDefault(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `INSERT INTO settings (key, value, updated_at)
			SELECT 'default_event_type_id', id::text, now() FROM event_types WHERE slug = 'workshop'
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	})
}

func defaultSetting(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var v string
	if err := pool.QueryRow(context.Background(), `SELECT value FROM settings WHERE key = 'default_event_type_id'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func setDefaultSetting(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE settings SET value = $1 WHERE key = 'default_event_type_id'`, strconv.FormatInt(id, 10)); err != nil {
		t.Fatal(err)
	}
}

func removeDefaultSetting(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	restoreDefault(t, pool)
	if _, err := pool.Exec(context.Background(), `DELETE FROM settings WHERE key = 'default_event_type_id'`); err != nil {
		t.Fatal(err)
	}
}

// race runs both functions behind one start barrier and waits for them.
func race(first, second func()) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, f := range []func(){first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			f()
		}()
	}
	close(start)
	wg.Wait()
}

func wantConstraint(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName != constraint {
		t.Fatalf("err = %v, want violation of %s", err, constraint)
	}
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

func TestSetDefaultMovesTheSetting(t *testing.T) {
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
	all, err := store.ListAll(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var flagged []int64
	for _, et := range all {
		if et.IsDefault {
			flagged = append(flagged, et.ID)
		}
	}
	if len(flagged) != 1 {
		t.Fatalf("flagged = %v, want one", flagged)
	}
	if got := defaultSetting(t, pool); got != strconv.FormatInt(a.ID, 10) && got != strconv.FormatInt(b.ID, 10) {
		t.Errorf("setting = %q, want %d or %d", got, a.ID, b.ID)
	}
}

func TestConcurrentSetDefaultAndDisableLeaveAnEnabledDefault(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	restoreDefault(t, pool)
	fallback := seededDefault(t, pool)
	x := seedType(t, store, 0)
	for round := 0; round < 20; round++ {
		setDefaultSetting(t, pool, fallback)
		if _, err := store.Update(ctx, x.ID, x.Name, 0, true); err != nil {
			t.Fatal(err)
		}
		var setErr, updateErr error
		race(
			func() { setErr = store.SetDefault(ctx, x.ID) },
			func() { _, updateErr = store.Update(ctx, x.ID, x.Name, 0, false) },
		)
		if setErr != nil && !errors.Is(setErr, ErrDisabled) {
			t.Errorf("round %d: SetDefault: %v", round, setErr)
		}
		if updateErr != nil && !errors.Is(updateErr, ErrIsDefault) {
			t.Errorf("round %d: Update: %v", round, updateErr)
		}
		var enabled bool
		if err := pool.QueryRow(ctx, `SELECT enabled FROM event_types WHERE id::text = $1`, defaultSetting(t, pool)).Scan(&enabled); err != nil {
			t.Fatalf("round %d: named type: %v", round, err)
		}
		if !enabled {
			t.Fatalf("round %d: the setting names a disabled type", round)
		}
	}
}

func TestConcurrentSetDefaultAndDeleteLeaveAnExistingDefault(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	restoreDefault(t, pool)
	fallback := seededDefault(t, pool)
	for round := 0; round < 20; round++ {
		setDefaultSetting(t, pool, fallback)
		x := seedType(t, store, 0)
		var setErr, deleteErr error
		race(
			func() { setErr = store.SetDefault(ctx, x.ID) },
			func() { deleteErr = store.Delete(ctx, x.ID) },
		)
		if setErr != nil && !errors.Is(setErr, ErrNotFound) {
			t.Errorf("round %d: SetDefault: %v", round, setErr)
		}
		if deleteErr != nil && !errors.Is(deleteErr, ErrIsDefault) && !errors.Is(deleteErr, ErrNotFound) {
			t.Errorf("round %d: Delete: %v", round, deleteErr)
		}
		var enabled bool
		if err := pool.QueryRow(ctx, `SELECT enabled FROM event_types WHERE id::text = $1`, defaultSetting(t, pool)).Scan(&enabled); err != nil {
			t.Fatalf("round %d: the setting names no row: %v", round, err)
		}
	}
	setDefaultSetting(t, pool, fallback)
}

func TestSetDefaultRepairsAMissingSetting(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	removeDefaultSetting(t, pool)
	et := seedType(t, store, 0)
	if err := store.SetDefault(context.Background(), et.ID); err != nil {
		t.Fatal(err)
	}
	if got := defaultSetting(t, pool); got != strconv.FormatInt(et.ID, 10) {
		t.Errorf("setting = %q, want %d", got, et.ID)
	}
}

func TestUpdateAndDeleteRefuseWhenSettingIsMissing(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	et := seedType(t, store, 0)
	removeDefaultSetting(t, pool)
	if _, err := store.Update(ctx, et.ID, et.Name, 0, false); !errors.Is(err, defaults.ErrUnset) {
		t.Errorf("disable err = %v, want ErrUnset", err)
	}
	if err := store.Delete(ctx, et.ID); !errors.Is(err, defaults.ErrUnset) {
		t.Errorf("delete err = %v, want ErrUnset", err)
	}
}

func TestUpdateRenameWorksWhenSettingIsMissing(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	et := seedType(t, store, 0)
	removeDefaultSetting(t, pool)
	got, err := store.Update(context.Background(), et.ID, "Renamed", 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || got.IsDefault {
		t.Errorf("updated = %+v", got)
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
