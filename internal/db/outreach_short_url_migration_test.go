package db

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestMigration000043RepairsOutreachShortURLs (#0603) seeds rows in the
// broken host/KEY form, runs the migration's own SQL inside a transaction that
// is rolled back, and checks the rewrite: short_url fixed in all three tables,
// draft body repaired, posted body untouched, already-correct rows untouched,
// and a second run changes nothing.
func TestMigration000043RepairsOutreachShortURLs(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	_, thisFile, _, _ := runtime.Caller(0)
	up, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", "000043_outreach_short_url_u_path.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", "000043_outreach_short_url_u_path.down.sql"))
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

	var wid, oid, loc int64
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tx.QueryRow(ctx, `INSERT INTO workshops (slug, title) VALUES ('zz-0603-mig', 'zz 0603') RETURNING id`).Scan(&wid))
	must(tx.QueryRow(ctx, `INSERT INTO outreach (workshop_id, shortlinks_campaign_slug) VALUES ($1, 'zz-0603') RETURNING id`, wid).Scan(&oid))
	must(tx.QueryRow(ctx, `INSERT INTO outreach_locations (name, code) VALUES ('Zz', 'zz0603') RETURNING id`).Scan(&loc))
	_, err = tx.Exec(ctx, `INSERT INTO outreach_links (outreach_id, channel, wave, short_link_key, short_url) VALUES
		($1,'bluesky','announce','aaa','https://go.example/aaa'),
		($1,'bluesky','reminder','bbb','https://go.example/u/bbb'),
		($1,'bluesky','final','uXLcM0','https://go.example/uXLcM0')`, oid)
	must(err)
	_, err = tx.Exec(ctx, `INSERT INTO outreach_flyer_links (outreach_id, location_id, short_link_key, short_url) VALUES ($1,$2,'fff','https://go.example/fff')`, oid, loc)
	must(err)
	const posted = "Posted https://go.example/ppp"
	_, err = tx.Exec(ctx, `INSERT INTO outreach_posts (outreach_id, channel, wave, body, source_fingerprint, short_link_key, short_url, status, posted_at) VALUES
		($1,'mastodon','announce','Draft https://go.example/ddd now','x','ddd','https://go.example/ddd','draft',NULL),
		($1,'mastodon','reminder',$2,'x','ppp','https://go.example/ppp','posted',now()),
		($1,'mastodon','final','Queued https://go.example/qqq','x','qqq','https://go.example/qqq','queued',NULL),
		($1,'bluesky','final','Token {link} stays','x',NULL,NULL,'draft',NULL),
		($1,'instagram','announce','Good https://go.example/u/ggg','x','ggg','https://go.example/u/ggg','manual',NULL),
		($1,'instagram','reminder','See https://go.example/abc. Not https://go.example/abcd or https://go.example/abc_x','x','abc','https://go.example/abc','scheduled',NULL),
		($1,'bluesky','announce','Bio https://go.example/u','x','u','https://go.example/u','manual',NULL)`, oid, posted)
	must(err)

	check := func(label string) {
		t.Helper()
		str := func(q string) string {
			var s string
			must(tx.QueryRow(ctx, q, oid).Scan(&s))
			return s
		}
		for q, want := range map[string]string{
			`SELECT short_url FROM outreach_links WHERE outreach_id=$1 AND short_link_key='aaa'`: "https://go.example/u/aaa",
			`SELECT short_url FROM outreach_links WHERE outreach_id=$1 AND short_link_key='bbb'`: "https://go.example/u/bbb",
			`SELECT short_url FROM outreach_flyer_links WHERE outreach_id=$1`:                    "https://go.example/u/fff",
			`SELECT short_url FROM outreach_posts WHERE outreach_id=$1 AND short_link_key='ddd'`: "https://go.example/u/ddd",
			`SELECT body FROM outreach_posts WHERE outreach_id=$1 AND short_link_key='ddd'`:      "Draft https://go.example/u/ddd now",
			`SELECT short_url FROM outreach_posts WHERE outreach_id=$1 AND short_link_key='ppp'`: "https://go.example/u/ppp",
			`SELECT body FROM outreach_posts WHERE outreach_id=$1 AND short_link_key='ppp'`:      posted,
			`SELECT short_url FROM outreach_posts WHERE outreach_id=$1 AND short_link_key='qqq'`: "https://go.example/u/qqq",
			`SELECT body FROM outreach_posts WHERE outreach_id=$1 AND short_link_key='qqq'`:      "Queued https://go.example/qqq",
			`SELECT body FROM outreach_posts WHERE outreach_id=$1 AND short_link_key IS NULL`:    "Token {link} stays",
			`SELECT body FROM outreach_posts WHERE outreach_id=$1 AND short_link_key='ggg'`:      "Good https://go.example/u/ggg",
			`SELECT short_url FROM outreach_posts WHERE outreach_id=$1 AND short_link_key='ggg'`: "https://go.example/u/ggg",
			// A key that itself begins with "u" is still broken (anchored prefix test, not a substring).
			`SELECT short_url FROM outreach_links WHERE outreach_id=$1 AND short_link_key='uXLcM0'`: "https://go.example/u/uXLcM0",
			// Key "u": rewritten once, and not again after a down and up.
			`SELECT body FROM outreach_posts WHERE outreach_id=$1 AND short_link_key='u'`: "Bio https://go.example/u/u",
			// Only the whole URL is rewritten, never a prefix of a longer key.
			`SELECT body FROM outreach_posts WHERE outreach_id=$1 AND short_link_key='abc'`: "See https://go.example/u/abc. Not https://go.example/abcd or https://go.example/abc_x",
		} {
			if got := str(q); got != want {
				t.Errorf("%s: %s = %q, want %q", label, q, got, want)
			}
		}
	}
	_, err = tx.Exec(ctx, string(up))
	must(err)
	check("first run")
	_, err = tx.Exec(ctx, string(up))
	must(err)
	check("second run")
	// Down reverses only short_url; up again must not double-rewrite bodies.
	_, err = tx.Exec(ctx, string(down))
	must(err)
	_, err = tx.Exec(ctx, string(up))
	must(err)
	check("down then up")
}
