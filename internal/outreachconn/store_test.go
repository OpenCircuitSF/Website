package outreachconn

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	t.Cleanup(func() {
		testDBPool.Exec(context.Background(), `DELETE FROM outreach_connection_values`)
	})
	return testDBPool
}

func TestStoreApplyAndAll(t *testing.T) {
	pool := testPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.Apply(ctx, map[string]string{KeyMastodonBaseURL: "https://m.example", KeyMastodonToken: "tok-fake"}, nil, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.All(ctx)
	if err != nil || len(got) != 2 || got[KeyMastodonToken] != "tok-fake" {
		t.Fatalf("All = %v, %v", got, err)
	}
	later := now.Add(time.Hour)
	if err := s.Apply(ctx, map[string]string{KeyMastodonToken: "tok-fake-2"}, nil, later); err != nil {
		t.Fatal(err)
	}
	var updated time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM outreach_connection_values WHERE key=$1`, KeyMastodonToken).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if !updated.Equal(later) {
		t.Errorf("an upsert must update updated_at: got %v want %v", updated, later)
	}
	if err := s.Apply(ctx, nil, []string{KeyMastodonBaseURL, KeyMastodonToken}, now); err != nil {
		t.Fatal(err)
	}
	if got, err := s.All(ctx); err != nil || len(got) != 0 {
		t.Errorf("after clear All = %v, %v", got, err)
	}
}

func TestStoreChecks(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO outreach_connection_values (key, value) VALUES ('not_a_key', 'x')`); err == nil {
		t.Error("an unknown key must violate the CHECK")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outreach_connection_values (key, value) VALUES ('mastodon_token', '')`); err == nil {
		t.Error("an empty value must violate the CHECK")
	}
}
