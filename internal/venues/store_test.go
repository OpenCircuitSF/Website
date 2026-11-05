package venues

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"

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

func strp(s string) *string { return &s }

func seedVenue(t *testing.T, store *Store) Venue {
	t.Helper()
	v, err := store.Create(context.Background(), CreateInput{Name: fmt.Sprintf("Zz venue %d", testdb.Unique()), Address: strp("1 Test St"), Note: strp("Test note"), SortOrder: 900})
	if err != nil {
		t.Fatalf("seed venue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM workshops WHERE venue_id = $1`, v.ID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM venues WHERE id = $1`, v.ID)
	})
	return v
}

func seedWorkshop(t *testing.T, pool *pgxpool.Pool, venueID *int64) {
	t.Helper()
	var id int64
	slug := fmt.Sprintf("zz-test-%d", testdb.Unique())
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO workshops (slug, title, venue_id) VALUES ($1, $1, $2) RETURNING id`, slug, venueID).Scan(&id); err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM workshops WHERE id = $1`, id) })
}

func seededDefault(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM venues WHERE name = 'Sequoia Fabrica' ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("finding seeded venue: %v", err)
	}
	return id
}

// restoreDefault puts the seeded default back at cleanup.
func restoreDefault(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `INSERT INTO settings (key, value, updated_at)
			SELECT 'default_venue_id', id::text, now() FROM venues WHERE name = 'Sequoia Fabrica'
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	})
}

func defaultSetting(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var v string
	if err := pool.QueryRow(context.Background(), `SELECT value FROM settings WHERE key = 'default_venue_id'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func setDefaultSetting(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE settings SET value = $1 WHERE key = 'default_venue_id'`, strconv.FormatInt(id, 10)); err != nil {
		t.Fatal(err)
	}
}

func removeDefaultSetting(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	restoreDefault(t, pool)
	if _, err := pool.Exec(context.Background(), `DELETE FROM settings WHERE key = 'default_venue_id'`); err != nil {
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

func input(v Venue, enabled bool) UpdateInput {
	return UpdateInput{Name: v.Name, Address: v.Address, Note: v.Note, SortOrder: v.SortOrder, Enabled: enabled}
}

func TestCreateAndUpdateRoundTripFields(t *testing.T) {
	store := NewStore(testPool(t))
	ctx := context.Background()
	v := seedVenue(t, store)
	if v.Address == nil || *v.Address != "1 Test St" || v.Note == nil || *v.Note != "Test note" || v.SortOrder != 900 || !v.Enabled || v.IsDefault {
		t.Fatalf("created = %+v", v)
	}
	got, err := store.Update(ctx, v.ID, UpdateInput{Name: "Zz renamed", Address: strp("2 Test St"), Note: strp("Other"), SortOrder: 901, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Zz renamed" || *got.Address != "2 Test St" || *got.Note != "Other" || got.SortOrder != 901 || got.Enabled {
		t.Errorf("updated = %+v", got)
	}
	if got.UpdatedAt.Before(v.UpdatedAt) || got.UpdatedAt.Equal(v.UpdatedAt) {
		t.Errorf("updated_at did not move: %v then %v", v.UpdatedAt, got.UpdatedAt)
	}
	read, err := store.GetByID(ctx, v.ID)
	if err != nil || read.Name != "Zz renamed" {
		t.Errorf("read = %+v, %v", read, err)
	}
	if _, err := store.Update(ctx, -1, UpdateInput{Name: "x", Enabled: true}); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id err = %v", err)
	}
}

func TestUpdateClearsAddressAndNote(t *testing.T) {
	store := NewStore(testPool(t))
	v := seedVenue(t, store)
	got, err := store.Update(context.Background(), v.ID, UpdateInput{Name: v.Name, SortOrder: v.SortOrder, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != nil || got.Note != nil {
		t.Errorf("address=%v note=%v, want both nil", got.Address, got.Note)
	}
}

func TestSetDefaultMovesTheSetting(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	restoreDefault(t, pool)
	v := seedVenue(t, store)
	if err := store.SetDefault(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := store.GetByID(context.Background(), v.ID)
	old, _ := store.GetByID(context.Background(), seededDefault(t, pool))
	if !got.IsDefault || old.IsDefault {
		t.Errorf("new.IsDefault=%v old.IsDefault=%v", got.IsDefault, old.IsDefault)
	}
	if err := store.SetDefault(context.Background(), -1); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing id err = %v", err)
	}
}

func TestSetDefaultRefusesDisabled(t *testing.T) {
	store := NewStore(testPool(t))
	v := seedVenue(t, store)
	if _, err := store.Update(context.Background(), v.ID, input(v, false)); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefault(context.Background(), v.ID); !errors.Is(err, ErrDisabled) {
		t.Errorf("err = %v, want ErrDisabled", err)
	}
}

func TestUpdateRefusesDisablingDefault(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	cur, err := store.GetByID(context.Background(), seededDefault(t, pool))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(context.Background(), cur.ID, input(cur, false)); !errors.Is(err, ErrIsDefault) {
		t.Errorf("err = %v, want ErrIsDefault", err)
	}
}

func TestDeleteRefusesDefault(t *testing.T) {
	pool := testPool(t)
	if err := NewStore(pool).Delete(context.Background(), seededDefault(t, pool)); !errors.Is(err, ErrIsDefault) {
		t.Errorf("err = %v, want ErrIsDefault", err)
	}
}

func TestDeleteRefusesInUse(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	v := seedVenue(t, store)
	seedWorkshop(t, pool, &v.ID)
	if err := store.Delete(ctx, v.ID); !errors.Is(err, ErrInUse) {
		t.Errorf("err = %v, want ErrInUse", err)
	}
	unused := seedVenue(t, store)
	if err := store.Delete(ctx, unused.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetByID(ctx, unused.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete err = %v", err)
	}
}

func TestSetDefaultRepairsAMissingSetting(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	removeDefaultSetting(t, pool)
	v := seedVenue(t, store)
	if err := store.SetDefault(context.Background(), v.ID); err != nil {
		t.Fatal(err)
	}
	if got := defaultSetting(t, pool); got != strconv.FormatInt(v.ID, 10) {
		t.Errorf("setting = %q, want %d", got, v.ID)
	}
}

func TestUpdateAndDeleteRefuseWhenSettingIsMissing(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	v := seedVenue(t, store)
	removeDefaultSetting(t, pool)
	if _, err := store.Update(ctx, v.ID, input(v, false)); !errors.Is(err, defaults.ErrUnset) {
		t.Errorf("disable err = %v, want ErrUnset", err)
	}
	if err := store.Delete(ctx, v.ID); !errors.Is(err, defaults.ErrUnset) {
		t.Errorf("delete err = %v, want ErrUnset", err)
	}
}

func TestUpdateRenameWorksWhenSettingIsMissing(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	v := seedVenue(t, store)
	removeDefaultSetting(t, pool)
	in := input(v, true)
	in.Name = "Zz renamed"
	got, err := store.Update(context.Background(), v.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Zz renamed" || got.IsDefault {
		t.Errorf("updated = %+v", got)
	}
}

func TestUsageCountsIgnoresListingsWithNoVenue(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	used, unused := seedVenue(t, store), seedVenue(t, store)
	seedWorkshop(t, pool, &used.ID)
	seedWorkshop(t, pool, &used.ID)
	seedWorkshop(t, pool, nil)
	counts, err := store.UsageCounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if counts[used.ID] != 2 || counts[unused.ID] != 0 {
		t.Errorf("counts = %v", counts)
	}
	if _, ok := counts[0]; ok {
		t.Errorf("counts has a zero key: %v", counts)
	}
}

func TestConcurrentSetDefaultAndDisableLeaveAnEnabledDefault(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	ctx := context.Background()
	restoreDefault(t, pool)
	fallback := seededDefault(t, pool)
	x := seedVenue(t, store)
	for round := 0; round < 20; round++ {
		setDefaultSetting(t, pool, fallback)
		if _, err := store.Update(ctx, x.ID, input(x, true)); err != nil {
			t.Fatal(err)
		}
		var setErr, updateErr error
		race(
			func() { setErr = store.SetDefault(ctx, x.ID) },
			func() { _, updateErr = store.Update(ctx, x.ID, input(x, false)) },
		)
		if setErr != nil && !errors.Is(setErr, ErrDisabled) {
			t.Errorf("round %d: SetDefault: %v", round, setErr)
		}
		if updateErr != nil && !errors.Is(updateErr, ErrIsDefault) {
			t.Errorf("round %d: Update: %v", round, updateErr)
		}
		var enabled bool
		if err := pool.QueryRow(ctx, `SELECT enabled FROM venues WHERE id::text = $1`, defaultSetting(t, pool)).Scan(&enabled); err != nil {
			t.Fatalf("round %d: named venue: %v", round, err)
		}
		if !enabled {
			t.Fatalf("round %d: the setting names a disabled venue", round)
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
		x := seedVenue(t, store)
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
		if err := pool.QueryRow(ctx, `SELECT enabled FROM venues WHERE id::text = $1`, defaultSetting(t, pool)).Scan(&enabled); err != nil {
			t.Fatalf("round %d: the setting names no row: %v", round, err)
		}
	}
	setDefaultSetting(t, pool, fallback)
}
