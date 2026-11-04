package db

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

// TestMigration000045EventTypes (#0613) runs the migration's own SQL inside a
// transaction that is rolled back: an existing workshop is backfilled to the
// default type, the seeds are exact, and down removes everything it added.
func TestMigration000045EventTypes(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	up, err := os.ReadFile(filepath.Join(dir, "000045_create_event_types.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(filepath.Join(dir, "000045_create_event_types.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	exec := func(sql string) {
		t.Helper()
		_, err := tx.Exec(ctx, sql)
		must(err)
	}

	exec(string(down))
	var wid int64
	must(tx.QueryRow(ctx, `INSERT INTO workshops (slug, title) VALUES ('zz-0613-mig', 'zz 0613') RETURNING id`).Scan(&wid))
	exec(string(up))

	var backfilled string
	must(tx.QueryRow(ctx, `SELECT et.slug FROM workshops w JOIN event_types et ON et.id = w.event_type_id WHERE w.id = $1`, wid).Scan(&backfilled))
	if backfilled != "workshop" {
		t.Errorf("backfilled type = %q, want workshop", backfilled)
	}

	rows, err := tx.Query(ctx, `SELECT slug, name, enabled, is_default FROM event_types ORDER BY sort_order`)
	must(err)
	type seed struct {
		Slug, Name         string
		Enabled, IsDefault bool
	}
	var got []seed
	for rows.Next() {
		var s seed
		must(rows.Scan(&s.Slug, &s.Name, &s.Enabled, &s.IsDefault))
		got = append(got, s)
	}
	must(rows.Err())
	want := []seed{
		{"workshop", "Workshop", true, true},
		{"hack-night", "Hack Night", true, false},
		{"talk", "Talk", true, false},
		{"hackathon", "Hackathon", true, false},
		{"social", "Social", true, false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("seeds = %+v, want %+v", got, want)
	}

	exec(string(down))
	var hasTable, hasColumn, hasFunc bool
	must(tx.QueryRow(ctx, `SELECT
		to_regclass('event_types') IS NOT NULL,
		EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'workshops' AND column_name = 'event_type_id' AND table_schema = current_schema()),
		EXISTS (SELECT 1 FROM pg_proc WHERE proname = 'event_types_default_id')`).Scan(&hasTable, &hasColumn, &hasFunc))
	if hasTable || hasColumn || hasFunc {
		t.Errorf("after down: table=%v column=%v function=%v, want all gone", hasTable, hasColumn, hasFunc)
	}
}
