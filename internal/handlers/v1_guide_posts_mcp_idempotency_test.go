package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// TestV1GuidePosts_MCPIdempotencyKey: #0685 against Postgres. A retried
// create_guide_posts with the same idempotency_key creates its posts once
// and returns the first result; other arguments on that key are a conflict.
// A key first used over REST is a conflict over MCP, not a replay, because
// each adapter hashes its own request form.
func TestV1GuidePosts_MCPIdempotencyKey(t *testing.T) {
	e := newV1GuidePostsEnv(t, bothConnected())
	mcpH := mcpserver.NewHandler(mcpserver.Deps{Registry: e.reg, Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter()})
	cs := mcptest.Connect(t, mcpH, e.write)

	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Tools {
		b, _ := json.Marshal(tool.InputSchema)
		var s struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		_ = json.Unmarshal(b, &s)
		_, has := s.Properties[mcpserver.IdempotencyKeyArg]
		if has != (tool.Name == "create_guide_posts") {
			t.Errorf("%s: %s in schema = %v", tool.Name, mcpserver.IdempotencyKeyArg, has)
		}
	}

	args := map[string]any{"slug": e.slug, "channels": []string{"bluesky", "mastodon"}, mcpserver.IdempotencyKeyArg: "mcp-0685-a"}
	first := mcptest.Call(t, cs, "create_guide_posts", args)
	if first.IsError {
		t.Fatalf("first: %s", mcptest.Text(first))
	}
	second := mcptest.Call(t, cs, "create_guide_posts", args)
	if second.IsError {
		t.Fatalf("retry: %s", mcptest.Text(second))
	}
	if mcptest.Text(first) != mcptest.Text(second) {
		t.Errorf("retry returned %s, want the first result %s", mcptest.Text(second), mcptest.Text(first))
	}
	if rows := v1AuditRows(t, e.pool, audit.ActionGuidePostCreated, e.writeTok.ID); len(rows) != 2 {
		t.Errorf("%d created rows after a retried create, want 2 (one per channel, once)", len(rows))
	}

	other := map[string]any{"slug": e.slug, "channels": []string{"bluesky"}, mcpserver.IdempotencyKeyArg: "mcp-0685-a"}
	if d := mcptest.ErrorOf(t, mcptest.Call(t, cs, "create_guide_posts", other)); d.Code != api.CodeConflict {
		t.Errorf("same key, other arguments = %+v, want conflict", d)
	}

	body := `{"slug":"` + e.slug + `","channels":["mastodon"]}`
	if rec := e.call(t, http.MethodPost, "/api/v1/guide-posts", e.write, body, "Idempotency-Key", "shared-0685"); rec.Code != http.StatusCreated {
		t.Fatalf("REST create = %d %s", rec.Code, rec.Body)
	}
	viaMCP := map[string]any{"slug": e.slug, "channels": []string{"mastodon"}, mcpserver.IdempotencyKeyArg: "shared-0685"}
	if d := mcptest.ErrorOf(t, mcptest.Call(t, cs, "create_guide_posts", viaMCP)); d.Code != api.CodeConflict {
		t.Errorf("REST key reused over MCP = %+v, want conflict", d)
	}
	if rows := v1AuditRows(t, e.pool, audit.ActionGuidePostCreated, e.writeTok.ID); len(rows) != 3 {
		t.Errorf("%d created rows, want 3", len(rows))
	}

	// Without a key, a repeat creates again, as before #0685.
	plain := map[string]any{"slug": e.slug, "channels": []string{"bluesky"}}
	for i := 0; i < 2; i++ {
		if r := mcptest.Call(t, cs, "create_guide_posts", plain); r.IsError {
			t.Fatalf("keyless create %d: %s", i, mcptest.Text(r))
		}
	}
	if rows := v1AuditRows(t, e.pool, audit.ActionGuidePostCreated, e.writeTok.ID); len(rows) != 5 {
		t.Errorf("%d created rows after two keyless creates, want 5", len(rows))
	}
}
