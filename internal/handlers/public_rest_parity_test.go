package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicmcp"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// TestPublicRESTAndMCPParity (#0734): every public operation is both an MCP
// tool with the registry's schemas and a REST route, and the two return the
// same result for the same input.
func TestPublicRESTAndMCPParity(t *testing.T) {
	ev := pubV1Workshop("parity-event", time.Now().Add(72*time.Hour))
	reg := pubV1Reg(pubV1WorkshopStore{upcoming: []workshops.Workshop{ev}}, nil, pubV1ArchiveStore{})
	ops := reg.Ops()
	mcpSrv := httptest.NewServer(publicmcp.NewHandler(reg, nil))
	defer mcpSrv.Close()
	rest := httptest.NewServer(publicapi.NewHTTPRouter(reg, pubV1Base, nil))
	defer rest.Close()
	cs := mcptest.ConnectURL(t, mcpSrv.URL+publicmcp.Path, &mcptest.Transport{})
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != len(ops) || len(ops) != 6 {
		t.Fatalf("%d tools, %d routes, want 6 each", len(tools.Tools), len(ops))
	}
	norm := func(v any) any {
		b, _ := json.Marshal(v)
		var out any
		_ = json.Unmarshal(b, &out)
		return out
	}
	// The same input for each op, as MCP arguments and as a REST path.
	inputs := map[string]struct {
		args map[string]any
		path string
	}{
		"list_upcoming_events":   {map[string]any{"limit": 5}, "events?limit=5"},
		"search_events":          {map[string]any{"query": "parity"}, "events/search?query=parity"},
		"get_event":              {map[string]any{"slug": "parity-event"}, "events/parity-event"},
		"list_newsletter_issues": {nil, "newsletter"},
		"get_newsletter_issue":   {map[string]any{"slug": "missing"}, "newsletter/missing"},
		"get_about":              {nil, "about"},
	}
	for _, op := range ops {
		var tool any
		for _, tl := range tools.Tools {
			if tl.Name == op.Name {
				tool = tl
			}
		}
		if tool == nil {
			t.Errorf("%s: no MCP tool", op.Name)
			continue
		}
		tj := norm(tool).(map[string]any)
		if !reflect.DeepEqual(tj["inputSchema"], norm(op.InputSchema())) || !reflect.DeepEqual(tj["outputSchema"], norm(op.OutputSchema())) {
			t.Errorf("%s: tool schemas differ from the registry's", op.Name)
		}
		in, ok := inputs[op.Name]
		if !ok || !strings.HasPrefix(publicapi.Prefix+in.path, strings.SplitN(op.Path, "{", 2)[0]) {
			t.Fatalf("%s: no parity input for %s", op.Name, op.Path)
		}
		resp, err := rest.Client().Get(rest.URL + publicapi.Prefix + in.path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		res := mcptest.Call(t, cs, op.Name, in.args)
		var mcpOut []byte
		if res.IsError {
			mcpOut = []byte(mcptest.Text(res))
		} else {
			mcpOut, _ = json.Marshal(res.StructuredContent)
		}
		var a, b any
		if err := json.Unmarshal(bytes.TrimSpace(body), &a); err != nil {
			t.Fatalf("%s: REST body %q: %v", op.Name, body, err)
		}
		_ = json.Unmarshal(mcpOut, &b)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("%s: REST %s\nMCP  %s", op.Name, body, mcpOut)
		}
		if (resp.StatusCode == 200) == res.IsError {
			t.Errorf("%s: REST %d but MCP IsError=%v", op.Name, resp.StatusCode, res.IsError)
		}
	}
}
