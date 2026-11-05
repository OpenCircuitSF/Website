package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// migration000047 opens a transaction that the test's end rolls back and returns
// the migration's own SQL.
func migration000047(t *testing.T) (ctx context.Context, tx pgx.Tx, up, down string) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	upB, err := os.ReadFile(filepath.Join(dir, "000047_default_event_type_setting.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	downB, err := os.ReadFile(filepath.Join(dir, "000047_default_event_type_setting.down.sql"))
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
	return ctx, tx, string(upB), string(downB)
}

// TestMigration000047DefaultEventTypeSetting moves the default both ways and
// proves a raw insert follows it each time.
func TestMigration000047DefaultEventTypeSetting(t *testing.T) {
	ctx, tx, up, down := migration000047(t)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	rawInsertType := func(slug string) string {
		t.Helper()
		var got string
		if err := tx.QueryRow(ctx,
			`WITH w AS (INSERT INTO workshops (slug, title) VALUES ($1, 'zz 0618') RETURNING event_type_id)
			 SELECT et.slug FROM w JOIN event_types et ON et.id = w.event_type_id`, slug).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	var talk int64
	if err := tx.QueryRow(ctx, `SELECT id FROM event_types WHERE slug = 'talk'`).Scan(&talk); err != nil {
		t.Fatal(err)
	}

	exec(down)
	exec(`UPDATE event_types SET is_default = FALSE WHERE is_default`)
	exec(`UPDATE event_types SET is_default = TRUE WHERE slug = 'talk'`)
	exec(up)

	var setting string
	if err := tx.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'default_event_type_id'`).Scan(&setting); err != nil {
		t.Fatal(err)
	}
	if setting != strconv.FormatInt(talk, 10) {
		t.Errorf("setting = %q, want %d", setting, talk)
	}
	var hasColumn, hasConstraint, hasIndex bool
	shape := func() {
		t.Helper()
		if err := tx.QueryRow(ctx, `SELECT
			EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'event_types' AND column_name = 'is_default' AND table_schema = current_schema()),
			EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'event_types_default_enabled'),
			to_regclass('event_types_one_default') IS NOT NULL`).Scan(&hasColumn, &hasConstraint, &hasIndex); err != nil {
			t.Fatal(err)
		}
	}
	shape()
	if hasColumn || hasConstraint || hasIndex {
		t.Errorf("after up: column=%v constraint=%v index=%v, want all gone", hasColumn, hasConstraint, hasIndex)
	}
	if got := rawInsertType("zz-0618-up"); got != "talk" {
		t.Errorf("raw insert after up got %q, want talk", got)
	}

	exec(down)
	shape()
	if !hasColumn || !hasConstraint || !hasIndex {
		t.Errorf("after down: column=%v constraint=%v index=%v, want all present", hasColumn, hasConstraint, hasIndex)
	}
	var flagged string
	if err := tx.QueryRow(ctx, `SELECT slug FROM event_types WHERE is_default`).Scan(&flagged); err != nil {
		t.Fatal(err)
	}
	if flagged != "talk" {
		t.Errorf("flagged = %q, want talk", flagged)
	}
	var rows int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM settings WHERE key = 'default_event_type_id'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("settings rows after down = %d, want 0", rows)
	}
	if got := rawInsertType("zz-0618-down"); got != "talk" {
		t.Errorf("raw insert after down got %q, want talk", got)
	}
}

func TestMigration000047FailsWithNoDefault(t *testing.T) {
	ctx, tx, up, down := migration000047(t)
	if _, err := tx.Exec(ctx, down); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE event_types SET is_default = FALSE WHERE is_default`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, up); err == nil {
		t.Error("up succeeded with no default type")
	}
}

func TestEventTypesDefaultIDFailsClearly(t *testing.T) {
	ctx, tx, _, _ := migration000047(t)
	var disabled int64
	if err := tx.QueryRow(ctx, `INSERT INTO event_types (slug, name, enabled) VALUES ('zz-0618-off', 'Zz', FALSE) RETURNING id`).Scan(&disabled); err != nil {
		t.Fatal(err)
	}
	const set = `UPDATE settings SET value = $1 WHERE key = 'default_event_type_id'`
	cases := []struct {
		name string
		sql  string
		args []any
	}{
		{"missing", `DELETE FROM settings WHERE key = 'default_event_type_id'`, nil},
		{"non-numeric", set, []any{"abc"}},
		{"no row", set, []any{"999999999"}},
		{"disabled", set, []any{strconv.FormatInt(disabled, 10)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sp, err := tx.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = sp.Rollback(ctx) }()
			if _, err := sp.Exec(ctx, c.sql, c.args...); err != nil {
				t.Fatal(err)
			}
			var id int64
			err = sp.QueryRow(ctx, `SELECT event_types_default_id()`).Scan(&id)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "OCDEF" || !strings.Contains(pgErr.Message, "default_event_type_id") {
				t.Errorf("err = %v, want OCDEF naming the key", err)
			}
		})
	}
}

// TestLiveColumnsRetireADroppedColumn pins the PRD column guard's replay: a column
// 000047 drops is not one PRD.md §6.2 must still list, and its siblings still are.
func TestLiveColumnsRetireADroppedColumn(t *testing.T) {
	live := liveColumnsFromMigrations(t)["event_types"]
	if _, ok := live["is_default"]; ok {
		t.Error("event_types.is_default is still live after its drop")
	}
	if _, ok := live["enabled"]; !ok {
		t.Error("event_types.enabled went missing")
	}
}
