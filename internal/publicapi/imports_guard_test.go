package publicapi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi/publicapitest"
)

// TestNoAdminAuthImports: no non-test file of the shared public definition
// imports admin auth or names the token middleware.
func TestNoAdminAuthImports(t *testing.T) {
	if v := publicapitest.AdminAuthViolations("."); len(v) > 0 {
		t.Fatalf("publicapi must share no auth with the admin API:\n%v", v)
	}
	dir := t.TempDir()
	src, err := os.ReadFile("registry.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "registry.go"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := "package publicapi\n\nimport \"github.com/brennanMKE/OpenCircuitSF/internal/middleware\"\n\nvar _ = middleware.RequireAPIToken\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if v := publicapitest.AdminAuthViolations(dir); len(v) == 0 {
		t.Fatal("the guard missed middleware.RequireAPIToken")
	}
}
