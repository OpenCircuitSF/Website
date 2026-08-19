package middleware

import (
	"os"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

func TestMain(m *testing.M) {
	release := testdb.Lock()
	code := m.Run()
	release()
	os.Exit(code)
}
