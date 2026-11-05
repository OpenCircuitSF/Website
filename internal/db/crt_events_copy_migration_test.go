package db

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMigration000046CrtEventsCopy (#0615) runs the migration's own SQL inside
// a transaction that is rolled back: both directions reword only the seeded
// text, the rest of the output is untouched, and a command an admin edited
// stays as it was.
func TestMigration000046CrtEventsCopy(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	up, err := os.ReadFile(filepath.Join(dir, "000046_crt_events_copy.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(filepath.Join(dir, "000046_crt_events_copy.down.sql"))
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
	command := func() string {
		t.Helper()
		var s string
		must(tx.QueryRow(ctx, `SELECT command FROM crt_commands WHERE slug = 'workshops-next'`).Scan(&s))
		return s
	}
	output := func() string {
		t.Helper()
		var s string
		must(tx.QueryRow(ctx, `SELECT output FROM crt_commands WHERE slug = 'subscribe-interests'`).Scan(&s))
		return s
	}

	exec(string(down))
	if got := command(); got != "workshops --next" {
		t.Errorf("command after down = %q, want workshops --next", got)
	}
	oldOutput := output()
	if !strings.Contains(oldOutput, "[x] workshops  [ ] digests") {
		t.Errorf("output after down = %q, want the old line", oldOutput)
	}

	exec(string(up))
	if got := command(); got != "events --next" {
		t.Errorf("command after up = %q, want events --next", got)
	}
	want := strings.Replace(oldOutput, "[x] workshops  [ ] digests", "[x] events  [ ] digests", 1)
	if got := output(); got != want || got == oldOutput {
		t.Errorf("output after up = %q, want %q", got, want)
	}

	exec(`UPDATE crt_commands SET command = 'custom' WHERE slug = 'workshops-next'`)
	exec(string(down))
	exec(string(up))
	if got := command(); got != "custom" {
		t.Errorf("edited command = %q, want it left as custom", got)
	}
}
