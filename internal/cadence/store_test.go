package cadence

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/eventtypes"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/venues"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	return testDBPool
}

// seedType makes an event type this test owns (AGENTS.md §8b).
func seedType(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	slug := fmt.Sprintf("zz-cadence-%d", testdb.Unique())
	et, err := eventtypes.NewStore(pool).Create(context.Background(), slug, "Zz "+slug, 0)
	if err != nil {
		t.Fatalf("seed type: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM event_cadence_rules WHERE event_type_id = $1`, et.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM event_types WHERE id = $1`, et.ID)
	})
	return et.ID
}

func seedVenue(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	v, err := venues.NewStore(pool).Create(context.Background(), venues.CreateInput{Name: fmt.Sprintf("Zz cadence venue %d", testdb.Unique())})
	if err != nil {
		t.Fatalf("seed venue: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM venues WHERE id = $1`, v.ID) })
	return v.ID
}

func cleanupRule(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM event_cadence_rules WHERE id = $1`, id) })
}

func TestStoreCRUD(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := NewStore(pool)
	typeID := seedType(t, pool)
	venueID := seedVenue(t, pool)

	w, err := s.Create(ctx, Rule{Name: "Hack night", EventTypeID: &typeID, Frequency: Weekly, Weekday: ptr(time.Tuesday), VenueID: &venueID, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cleanupRule(t, pool, w.ID)
	if w.ID == 0 || w.Weekday == nil || *w.Weekday != time.Tuesday || w.PerMonth != 0 || w.TitlePrefix != "" || *w.VenueID != venueID || !w.Active {
		t.Fatalf("weekly round trip %+v", w)
	}
	m, err := s.Create(ctx, Rule{Name: "LEDs", TitlePrefix: "Programming LEDs", Frequency: Monthly, PerMonth: 1, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cleanupRule(t, pool, m.ID)
	if m.EventTypeID != nil || m.Weekday != nil || m.PerMonth != 1 || m.TitlePrefix != "Programming LEDs" {
		t.Fatalf("monthly round trip %+v", m)
	}

	got, err := s.Get(ctx, m.ID)
	if err != nil || got.Name != "LEDs" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	upd, err := s.Update(ctx, m.ID, Rule{Name: "LEDs twice", TitlePrefix: "Programming LEDs", Frequency: Monthly, PerMonth: 2, Active: false})
	if err != nil || upd.PerMonth != 2 || upd.Active || upd.Name != "LEDs twice" || upd.UpdatedAt.Before(m.UpdatedAt) {
		t.Fatalf("Update = %+v, %v", upd, err)
	}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for i, r := range all {
		if i > 0 && all[i-1].ID >= r.ID {
			t.Errorf("List not ordered by id")
		}
		if r.ID == w.ID || r.ID == m.ID {
			seen++
		}
	}
	if seen != 2 {
		t.Errorf("List saw %d of 2 rules", seen)
	}
	if err := s.Delete(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after delete: %v", err)
	}
	if err := s.Delete(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete twice: %v", err)
	}
	if _, err := s.Update(ctx, m.ID, Rule{Name: "x", TitlePrefix: "x", Frequency: Monthly, PerMonth: 1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update missing: %v", err)
	}
}

func TestStoreConstraintErrors(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := NewStore(pool)
	var maxType, maxVenue int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) + 1000 FROM event_types`).Scan(&maxType); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT COALESCE(max(id), 0) + 1000 FROM venues`).Scan(&maxVenue); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, Rule{Name: "x", EventTypeID: &maxType, Frequency: Monthly, PerMonth: 1}); !errors.Is(err, ErrEventTypeNotFound) {
		t.Errorf("unknown type: %v", err)
	}
	if _, err := s.Create(ctx, Rule{Name: "x", TitlePrefix: "x", VenueID: &maxVenue, Frequency: Monthly, PerMonth: 1}); !errors.Is(err, ErrVenueNotFound) {
		t.Errorf("unknown venue: %v", err)
	}
	// The CHECKs back Validate up: a weekly rule with no weekday is refused.
	if _, err := s.Create(ctx, Rule{Name: "x", TitlePrefix: "x", Frequency: Weekly}); err == nil {
		t.Error("weekly rule with no weekday accepted by the table")
	}
	if _, err := s.Create(ctx, Rule{Name: "x", Frequency: Monthly, PerMonth: 1}); err == nil {
		t.Error("rule with no match accepted by the table")
	}
}

func TestStoreEventTypeDeleteCascades(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := NewStore(pool)
	typeID := seedType(t, pool)
	r, err := s.Create(ctx, Rule{Name: "x", EventTypeID: &typeID, Frequency: Monthly, PerMonth: 1, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cleanupRule(t, pool, r.ID)
	if _, err := pool.Exec(ctx, `DELETE FROM event_types WHERE id = $1`, typeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, r.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("rule survived its type: %v", err)
	}
}

func TestStoreVenueDeleteClearsFilter(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	s := NewStore(pool)
	venueID := seedVenue(t, pool)
	r, err := s.Create(ctx, Rule{Name: "x", TitlePrefix: "x", VenueID: &venueID, Frequency: Monthly, PerMonth: 1, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	cleanupRule(t, pool, r.ID)
	if _, err := pool.Exec(ctx, `DELETE FROM venues WHERE id = $1`, venueID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, r.ID)
	if err != nil || got.VenueID != nil {
		t.Errorf("after venue delete: %+v, %v", got, err)
	}
}
