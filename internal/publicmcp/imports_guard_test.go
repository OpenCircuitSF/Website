package publicmcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi/publicapitest"
)

// TestNoAdminAuthImports: no non-test file here imports the admin MCP
// server, API tokens, the handlers package or the SDK's auth package, or
// names the token middleware. The scanner is publicapitest.AdminAuthViolations.
func TestNoAdminAuthImports(t *testing.T) {
	if v := publicapitest.AdminAuthViolations("."); len(v) > 0 {
		t.Fatalf("the public MCP server must share no auth with the admin one:\n%v", v)
	}
	dir := t.TempDir()
	src, err := os.ReadFile("publicmcp.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "publicmcp.go"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := "package publicmcp\n\nimport _ \"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver\"\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.go"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if v := publicapitest.AdminAuthViolations(dir); len(v) == 0 {
		t.Fatal("the guard missed an mcpserver import")
	}
}
