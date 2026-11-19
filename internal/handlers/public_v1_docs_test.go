package handlers

import (
	"os"
	"strings"
	"testing"
)

// TestPublicMCPDocsNameEveryTool (#0729): docs/mcp.md's public section names
// every public tool and the www endpoint.
func TestPublicMCPDocsNameEveryTool(t *testing.T) {
	b, err := os.ReadFile("../../docs/mcp.md")
	if err != nil {
		t.Fatal(err)
	}
	_, sec, ok := strings.Cut(string(b), "\n## Public MCP server")
	if !ok {
		t.Fatal("docs/mcp.md has no ## Public MCP server section")
	}
	if i := strings.Index(sec, "\n## "); i >= 0 {
		sec = sec[:i]
	}
	if !strings.Contains(sec, "https://www.opencircuitsf.com/mcp") {
		t.Error("public section lacks https://www.opencircuitsf.com/mcp")
	}
	for _, op := range BuildPublicRegistry(PublicDeps{}).Ops() {
		if !strings.Contains(sec, "`"+op.Name+"`") {
			t.Errorf("public section does not name `%s`", op.Name)
		}
	}
}
