package publicapitest

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAdminAuthViolations: a clean file passes, each banned import and
// selector is reported, and an empty directory is an error, not a pass.
func TestAdminAuthViolations(t *testing.T) {
	dir := t.TempDir()
	if v := AdminAuthViolations(dir); len(v) == 0 {
		t.Error("an empty directory passed")
	}
	write := func(name, src string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ok.go", "package p\n\nimport _ \"net/http\"\n")
	write("ok_test.go", "package p\n\nimport _ \"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest\"\n")
	if v := AdminAuthViolations(dir); len(v) != 0 {
		t.Fatalf("clean dir: %v", v)
	}
	write("bad.go", "package p\n\nimport (\n\t_ \"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest\"\n\t_ \"github.com/brennanMKE/OpenCircuitSF/internal/apitokens\"\n\t_ \"github.com/brennanMKE/OpenCircuitSF/internal/handlers\"\n\t_ \"github.com/modelcontextprotocol/go-sdk/auth\"\n)\n\nvar _ = middleware.WithAPIToken\n")
	if v := AdminAuthViolations(dir); len(v) != 5 {
		t.Fatalf("want 5 violations, got %d: %v", len(v), v)
	}
}
