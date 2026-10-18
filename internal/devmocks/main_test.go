package devmocks_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// testDBPool is nil when TEST_DATABASE_URL is unset; DB-backed tests skip then.
var testDBPool *pgxpool.Pool

func TestMain(m *testing.M) {
	release := testdb.Lock()
	pool, err := testdb.Connect(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		release()
		os.Exit(1)
	}
	testDBPool = pool
	code := m.Run()
	if testDBPool != nil {
		testDBPool.Close()
	}
	release()
	os.Exit(code)
}
