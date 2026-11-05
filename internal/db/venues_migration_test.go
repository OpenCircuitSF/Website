package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// migration000048 opens a transaction that the test's end rolls back, returns
// the migration's own SQL, and leaves the database at version 47 holding one
// listing for each shape the consolidation has to handle.
func migration000048(t *testing.T) (ctx context.Context, tx pgx.Tx, up, down string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	upB, err := os.ReadFile(filepath.Join(dir, "000048_create_venues.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	downB, err := os.ReadFile(filepath.Join(dir, "000048_create_venues.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	if _, err := tx.Exec(ctx, string(downB)); err != nil {
		t.Fatalf("down: %v", err)
	}
	for _, f := range venueFixtures {
		if _, err := tx.Exec(ctx,
			`INSERT INTO workshops (slug, title, status, location_name, location_address, location_note, updated_at)
			 VALUES ($1, $1, $2, $3, $4, $5, $6)`,
			f.slug, f.status, f.name, f.address, f.note, f.updatedAt); err != nil {
			t.Fatalf("seed %s: %v", f.slug, err)
		}
	}
	return ctx, tx, string(upB), string(downB)
}

func textPtr(s string) *string { return &s }

type venueFixture struct {
	slug, status        string
	name, address, note *string
	updatedAt           time.Time
}

var venueFixtures = []venueFixture{
	{"zz-0617-seq-1", "published", textPtr("Sequoia Fabrica"), textPtr("1736 18th St"), textPtr("Foot of Potrero Hill"), day(1)},
	{"zz-0617-seq-2", "published", textPtr("Sequoia Fabrica"), textPtr("1736 18th Street, SF"), textPtr("Foot of Potrero Hill"), day(2)},
	{"zz-0617-seq-3", "published", textPtr("Sequoia Fabrica"), textPtr("1736 18th St., San Francisco"), nil, day(3)},
	{"zz-0617-seq-draft", "draft", textPtr("  sequoia FABRICA\n"), textPtr("elsewhere"), textPtr("Use the side door tonight"), day(4)},
	{"zz-0617-shop-1", "published", textPtr("The Shop"), textPtr("1 Main"), textPtr("Upstairs"), day(5)},
	{"zz-0617-shop-2", "draft", textPtr("  the shop "), textPtr("1 Main"), nil, day(6)},
	{"zz-0617-shop-3", "draft", textPtr("THE SHOP"), textPtr("2 Other"), textPtr("   "), day(7)},
	{"zz-0617-address-only", "draft", nil, textPtr(" 99 Lonely Road "), nil, day(8)},
	{"zz-0617-note-only", "draft", textPtr(""), textPtr(" "), textPtr("Ask at the desk"), day(9)},
	{"zz-0617-blank", "draft", textPtr("  "), textPtr(""), textPtr("\t\n"), day(10)},
	{"zz-0617-null", "draft", nil, nil, nil, day(11)},
	{"zz-0617-canceled", "canceled", textPtr("Canceled Place"), nil, nil, day(12)},
}

func day(n int) time.Time { return time.Date(2020, 1, n, 12, 0, 0, 0, time.UTC) }

type venueRow struct {
	id            int64
	name          string
	address, note *string
}

func strEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// TestMigration000048ConsolidatesVenues pins every shape's venue row and which
// listings point at it.
func TestMigration000048ConsolidatesVenues(t *testing.T) {
	ctx, tx, up, _ := migration000048(t)
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("up: %v", err)
	}
	venueOf := func(slug string) *venueRow {
		t.Helper()
		var id *int64
		if err := tx.QueryRow(ctx, `SELECT venue_id FROM workshops WHERE slug = $1`, slug).Scan(&id); err != nil {
			t.Fatalf("%s: %v", slug, err)
		}
		if id == nil {
			return nil
		}
		r := venueRow{id: *id}
		if err := tx.QueryRow(ctx, `SELECT name, address, note FROM venues WHERE id = $1`, *id).Scan(&r.name, &r.address, &r.note); err != nil {
			t.Fatalf("%s venue: %v", slug, err)
		}
		return &r
	}
	want := []struct {
		slugs         []string
		name          string
		address, note *string
	}{
		{[]string{"zz-0617-seq-1", "zz-0617-seq-2", "zz-0617-seq-3", "zz-0617-seq-draft"}, "Sequoia Fabrica", textPtr("1736 18th Street, San Francisco, CA 94107"), textPtr("Foot of Potrero Hill")},
		{[]string{"zz-0617-shop-1", "zz-0617-shop-2", "zz-0617-shop-3"}, "THE SHOP", textPtr("1 Main"), textPtr("Upstairs")},
		{[]string{"zz-0617-address-only"}, "99 Lonely Road", nil, nil},
		{[]string{"zz-0617-note-only"}, "Ask at the desk", nil, nil},
		{[]string{"zz-0617-canceled"}, "Canceled Place", nil, nil},
	}
	for _, w := range want {
		var first int64
		for i, slug := range w.slugs {
			got := venueOf(slug)
			if got == nil {
				t.Errorf("%s: no venue", slug)
				continue
			}
			if got.name != w.name || !strEq(got.address, w.address) || !strEq(got.note, w.note) {
				t.Errorf("%s: venue = %q %v %v, want %q", slug, got.name, got.address, got.note, w.name)
			}
			if i == 0 {
				first = got.id
			} else if got.id != first {
				t.Errorf("%s: venue %d, want the same row as %s (%d)", slug, got.id, w.slugs[0], first)
			}
		}
		var rows int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM venues WHERE lower(name) = lower($1)`, w.name).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Errorf("%d venues named %q, want 1", rows, w.name)
		}
	}
	for _, slug := range []string{"zz-0617-blank", "zz-0617-null"} {
		if got := venueOf(slug); got != nil {
			t.Errorf("%s: venue %q, want none", slug, got.name)
		}
	}
}

func TestMigration000048SeedsSequoiaAsDefault(t *testing.T) {
	ctx, tx, up, _ := migration000048(t)
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("up: %v", err)
	}
	var count int
	var address, note, setting, id string
	if err := tx.QueryRow(ctx, `SELECT count(*) OVER (), id::text, address, note FROM venues WHERE lower(name) = 'sequoia fabrica'`).Scan(&count, &id, &address, &note); err != nil {
		t.Fatal(err)
	}
	if count != 1 || address != "1736 18th Street, San Francisco, CA 94107" || note != "Foot of Potrero Hill" {
		t.Errorf("count=%d address=%q note=%q", count, address, note)
	}
	if err := tx.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'default_venue_id'`).Scan(&setting); err != nil {
		t.Fatal(err)
	}
	if setting != id {
		t.Errorf("setting = %q, want %q", setting, id)
	}
}

func TestMigration000048LeavesUpdatedAtAndTextAlone(t *testing.T) {
	ctx, tx, up, _ := migration000048(t)
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("up: %v", err)
	}
	for _, f := range venueFixtures {
		var name, address, note *string
		var updated time.Time
		if err := tx.QueryRow(ctx, `SELECT location_name, location_address, location_note, updated_at FROM workshops WHERE slug = $1`, f.slug).
			Scan(&name, &address, &note, &updated); err != nil {
			t.Fatal(err)
		}
		if !strEq(name, f.name) || !strEq(address, f.address) || !strEq(note, f.note) {
			t.Errorf("%s: text changed: %v %v %v", f.slug, name, address, note)
		}
		if !updated.Equal(f.updatedAt) {
			t.Errorf("%s: updated_at = %v, want %v", f.slug, updated, f.updatedAt)
		}
	}
}

func TestMigration000048DownRepopulatesFromVenue(t *testing.T) {
	ctx, tx, up, down := migration000048(t)
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("up: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE venues SET address = 'Edited address' WHERE lower(name) = 'the shop'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatalf("down: %v", err)
	}
	var name, address, note *string
	read := func(slug string) {
		t.Helper()
		if err := tx.QueryRow(ctx, `SELECT location_name, location_address, location_note FROM workshops WHERE slug = $1`, slug).Scan(&name, &address, &note); err != nil {
			t.Fatal(err)
		}
	}
	read("zz-0617-shop-2")
	if !strEq(name, textPtr("THE SHOP")) || !strEq(address, textPtr("Edited address")) || !strEq(note, textPtr("Upstairs")) {
		t.Errorf("shop-2 = %v %v %v", name, address, note)
	}
	read("zz-0617-blank")
	if name != nil || address != nil || note != nil {
		t.Errorf("a listing with no venue reads %v %v %v, want NULLs", name, address, note)
	}
	var hasTable, hasColumn, hasSetting bool
	if err := tx.QueryRow(ctx, `SELECT
		to_regclass('venues') IS NOT NULL,
		EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'workshops' AND column_name = 'venue_id' AND table_schema = current_schema()),
		EXISTS (SELECT 1 FROM settings WHERE key = 'default_venue_id')`).Scan(&hasTable, &hasColumn, &hasSetting); err != nil {
		t.Fatal(err)
	}
	if hasTable || hasColumn || hasSetting {
		t.Errorf("after down: table=%v column=%v setting=%v, want all gone", hasTable, hasColumn, hasSetting)
	}
}

// TestMigration000048PriorBuildQueriesStillRun runs the previous build's literal
// column list at head, which is what keeps it serving between the migration and
// the restart. The follow-up that drops the three columns deletes this test.
func TestMigration000048PriorBuildQueriesStillRun(t *testing.T) {
	ctx, tx, up, _ := migration000048(t)
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("up: %v", err)
	}
	const columns = `id, slug, title, summary, body_md, starts_at, ends_at,
		location_name, location_address, location_note, capacity, signup_url,
		cover_image, status, published_at, created_at, updated_at, event_type_id`
	if _, err := tx.Exec(ctx, `SELECT `+columns+` FROM workshops`); err != nil {
		t.Errorf("select: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO workshops (slug, title, summary, body_md, starts_at, ends_at,
			location_name, location_address, location_note, capacity, signup_url, cover_image, event_type_id)
		 VALUES ('zz-0617-prior', 't', NULL, NULL, NULL, NULL, 'n', 'a', 'o', NULL, NULL, NULL, event_types_default_id())
		 RETURNING `+columns); err != nil {
		t.Errorf("insert: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE workshops SET location_name = 'n2', location_address = 'a2', location_note = 'o2' WHERE slug = 'zz-0617-prior'`); err != nil {
		t.Errorf("update: %v", err)
	}
}

func TestWorkshopsVenueForeignKeyRestricts(t *testing.T) {
	ctx, tx, up, _ := migration000048(t)
	if _, err := tx.Exec(ctx, up); err != nil {
		t.Fatalf("up: %v", err)
	}
	_, err := tx.Exec(ctx, `DELETE FROM venues WHERE lower(name) = 'the shop'`)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" || pgErr.ConstraintName != "workshops_venue_id_fkey" {
		t.Errorf("err = %v, want 23503 on workshops_venue_id_fkey", err)
	}
}
