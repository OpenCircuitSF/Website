package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicmcp"
)

// publicDocsPage is the slice of web/src/content/docs/mcp.json this guard
// reads (#0735).
type publicDocsPage struct {
	Sections []struct {
		Heading string `json:"heading"`
		Blocks  []struct {
			Type  string   `json:"type"`
			Label string   `json:"label"`
			Text  string   `json:"text"`
			Items []string `json:"items"`
		} `json:"blocks"`
	} `json:"sections"`
}

const publicDocsMCPFile = "../../web/src/content/docs/mcp.json"

// docsToolDrift compares the Tools section's first list block with ops, in
// order, as "name — Summary". It returns one line per difference.
func docsToolDrift(raw []byte, ops []publicapi.Op) []string {
	var page publicDocsPage
	if err := json.Unmarshal(raw, &page); err != nil {
		return []string{"decode: " + err.Error()}
	}
	var items []string
	found := false
	for _, s := range page.Sections {
		if s.Heading != "Tools" {
			continue
		}
		for _, b := range s.Blocks {
			if b.Type == "list" {
				items, found = b.Items, true
				break
			}
		}
	}
	if !found {
		return []string{"no list block in a Tools section"}
	}
	var drift []string
	for i := 0; i < len(ops) || i < len(items); i++ {
		want, got := "", ""
		if i < len(ops) {
			want = ops[i].Name + " — " + ops[i].Summary
		}
		if i < len(items) {
			got = items[i]
		}
		if want != got {
			drift = append(drift, fmt.Sprintf("tool %d: docs %q, registry %q", i, got, want))
		}
	}
	return drift
}

func TestPublicDocsMCPPageMatchesRegistry(t *testing.T) {
	raw, err := os.ReadFile(publicDocsMCPFile)
	if err != nil {
		t.Fatal(err)
	}
	ops := BuildPublicRegistry(PublicDeps{}).Ops()
	if len(ops) < 6 {
		t.Fatalf("registry has %d ops, want at least 6", len(ops))
	}
	for _, d := range docsToolDrift(raw, ops) {
		t.Error(d)
	}

	endpoint := "https://www.opencircuitsf.com" + publicmcp.Path
	var page publicDocsPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	var sawCommand, sawJSON bool
	for _, s := range page.Sections {
		for _, b := range s.Blocks {
			if b.Type != "code" {
				continue
			}
			if b.Text == "claude mcp add --transport http opencircuitsf "+endpoint {
				sawCommand = true
			}
			if b.Label == "mcpServers JSON" {
				sawJSON = true
				var cfg struct {
					MCPServers map[string]struct {
						Type string `json:"type"`
						URL  string `json:"url"`
					} `json:"mcpServers"`
				}
				if !json.Valid([]byte(b.Text)) || json.Unmarshal([]byte(b.Text), &cfg) != nil {
					t.Errorf("mcpServers JSON block is not valid JSON")
				} else if got := cfg.MCPServers["opencircuitsf"].URL; got != endpoint {
					t.Errorf("mcpServers.opencircuitsf.url = %q, want %q", got, endpoint)
				}
				if got := cfg.MCPServers["opencircuitsf"].Type; got != "http" {
					t.Errorf("mcpServers.opencircuitsf.type = %q, want \"http\"", got)
				}
			}
		}
	}
	if !sawCommand {
		t.Errorf("no code block holds the claude mcp add command for %s", endpoint)
	}
	if !sawJSON {
		t.Errorf("no code block labelled mcpServers JSON")
	}
}

// TestDocsToolDriftBites proves the comparison fails when a tool is missing
// from the docs.
func TestDocsToolDriftBites(t *testing.T) {
	raw, err := os.ReadFile(publicDocsMCPFile)
	if err != nil {
		t.Fatal(err)
	}
	var page map[string]any
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	mutated := false
	for _, s := range page["sections"].([]any) {
		sec := s.(map[string]any)
		if sec["heading"] != "Tools" {
			continue
		}
		for _, b := range sec["blocks"].([]any) {
			blk := b.(map[string]any)
			if blk["type"] == "list" && !mutated {
				items := blk["items"].([]any)
				blk["items"] = items[:len(items)-1]
				mutated = true
			}
		}
	}
	if !mutated {
		t.Fatal("no Tools list block to mutate")
	}
	out, _ := json.Marshal(page)
	if len(docsToolDrift(out, BuildPublicRegistry(PublicDeps{}).Ops())) == 0 {
		t.Fatal("docsToolDrift reported nothing for a page missing a tool")
	}
}
