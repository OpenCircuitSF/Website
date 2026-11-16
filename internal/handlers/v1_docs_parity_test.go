package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

// TestV1DocsNameEveryOperation (#0687): docs/api.md names every v1
// operation (its OpenAPI operationId), and docs/mcp.md names every MCP tool,
// which is every operation without an MCPExcluded reason. A name counts only
// in backticks, so a new operation cannot be satisfied by prose that happens
// to contain the same words. The list is the production registry itself, so
// adding an operation without documenting it fails here.
func TestV1DocsNameEveryOperation(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", "docs", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	apiDoc, mcpDoc := read("api.md"), read("mcp.md")
	ops := BuildV1Registry(api.Config{Version: "x"}, V1Deps{}).Operations()
	if len(ops) < 20 {
		t.Fatalf("registry has %d operations; expected the full v1 list", len(ops))
	}
	tools := 0
	for _, op := range ops {
		tick := "`" + op.Name + "`"
		if !strings.Contains(apiDoc, tick) {
			t.Errorf("docs/api.md does not name operation %s", tick)
		}
		if op.MCPExcluded != "" {
			continue
		}
		tools++
		if !strings.Contains(mcpDoc, tick) {
			t.Errorf("docs/mcp.md does not name MCP tool %s", tick)
		}
	}
	if tools == 0 || tools == len(ops) {
		t.Fatalf("%d of %d operations are MCP tools; expected token_probe alone excluded", tools, len(ops))
	}
}
