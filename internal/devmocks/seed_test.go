package devmocks_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/devmocks"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreachconn"
)

func TestSeedDemoIsIdempotent(t *testing.T) {
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	ctx := context.Background()
	clean := func() {
		testDBPool.Exec(ctx, `DELETE FROM workshops WHERE slug = $1`, devmocks.DemoSlug)
		testDBPool.Exec(ctx, `DELETE FROM outreach_locations WHERE code IN ('mission-lib','haight-coop','soma-hw','civic-mkr')`)
		testDBPool.Exec(ctx, `DELETE FROM outreach_connection_values`)
	}
	clean()
	t.Cleanup(clean)
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		if err := devmocks.SeedDemo(ctx, testDBPool, dir, "http://127.0.0.1:18081", time.Now()); err != nil {
			t.Fatalf("SeedDemo run %d: %v", i+1, err)
		}
	}
	var n int
	var status, cover string
	if err := testDBPool.QueryRow(ctx, `SELECT count(*), min(status), min(cover_image) FROM workshops WHERE slug = $1`, devmocks.DemoSlug).Scan(&n, &status, &cover); err != nil {
		t.Fatal(err)
	}
	if n != 1 || status != "published" || cover != "/media/led-programming.png" {
		t.Fatalf("workshop rows = %d status %q cover %q", n, status, cover)
	}
	if err := testDBPool.QueryRow(ctx, `SELECT count(*) FROM outreach_locations WHERE code IN ('mission-lib','haight-coop','soma-hw','civic-mkr')`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("locations = %d, %v", n, err)
	}
	vals, err := outreachconn.NewStore(testDBPool).All(ctx)
	if err != nil || len(vals) != len(devmocks.ConnectionValues("x")) {
		t.Fatalf("saved values = %d, %v", len(vals), err)
	}
	if fi, err := os.Stat(dir + "/led-programming.png"); err != nil || fi.Size() == 0 || fi.Size() > 1_000_000 {
		t.Fatalf("cover file: %v", err)
	}
}
