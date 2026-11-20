package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicmcp"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// TestPublicToolsNeverReturnUnpublished (#0729) seeds hidden events and
// campaigns in a real database, calls every public tool through the real
// MCP server, and asserts none of them comes back, while published controls
// do. Since #0734 it makes the same calls over the REST router too.
func TestPublicToolsNeverReturnUnpublished(t *testing.T) {
	pool := interestsTestPool(t)
	ctx := context.Background()
	token := fmt.Sprintf("zzpubmcp%d", testdb.Unique())
	store := workshops.NewStore(pool)
	start := time.Now().Add(96 * time.Hour)

	mkEvent := func(label string, steps ...string) workshops.Workshop {
		title := uniquePublicWorkshopTitle(t) + " " + token + " " + label
		w, err := store.Create(ctx, workshops.CreateInput{Title: title, StartsAt: &start})
		if err != nil {
			t.Fatalf("seed %s: %v", label, err)
		}
		cleanupPublicWorkshop(t, pool, w.ID)
		for _, s := range steps {
			if w, err = store.Update(ctx, w.ID, workshops.UpdateInput{Title: w.Title, StartsAt: &start, Status: s}); err != nil {
				t.Fatalf("%s -> %s: %v", label, s, err)
			}
		}
		return w
	}
	draft := mkEvent("draft")
	canceledNever := mkEvent("canceled-never-published", workshops.StatusCanceled)
	unpublished := mkEvent("unpublished", workshops.StatusPublished, workshops.StatusDraft)
	control := mkEvent("control", workshops.StatusPublished)
	if unpublished.PublishedAt != nil {
		t.Logf("note: unpublishing kept published_at; it is hidden by its draft status")
	}
	hiddenEvents := []workshops.Workshop{draft, canceledNever, unpublished}

	draftC := seedArchiveCampaign(t, pool, mailing.CampaignStatusDraft, mailing.ArchiveStatusPending)
	withheldC := seedArchiveCampaign(t, pool, mailing.CampaignStatusSent, mailing.ArchiveStatusWithheld)
	scheduledC := seedArchiveCampaign(t, pool, mailing.CampaignStatusScheduled, mailing.ArchiveStatusPublished)
	controlC := seedArchiveCampaign(t, pool, mailing.CampaignStatusSent, mailing.ArchiveStatusPublished)
	hiddenCampaigns := []mailing.Campaign{draftC, withheldC, scheduledC}

	reg := BuildPublicRegistry(PublicDeps{
		Workshops: NewPublicWorkshopsHandler(store, interests.NewStore(pool)),
		Archive:   NewPublicArchiveHandler(mailing.NewCampaignStore(pool)),
		About:     func() publicapi.AboutText { return publicapi.AboutText{Links: map[string]string{}} },
		BaseURL:   "https://www.opencircuitsf.com", Version: "t",
	})
	srv := httptest.NewServer(publicmcp.NewHandler(reg, nil, nil))
	defer srv.Close()
	cs := mcptest.ConnectURL(t, srv.URL+publicmcp.Path, &mcptest.Transport{})

	var outputs []string
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res := mcptest.Call(t, cs, name, args)
		var b strings.Builder
		for _, c := range res.Content {
			b.WriteString(c.(*mcp.TextContent).Text + "\n")
		}
		sc, _ := json.Marshal(res.StructuredContent)
		b.Write(sc)
		outputs = append(outputs, b.String())
		return res
	}
	text := func(res *mcp.CallToolResult) string { return mcptest.Text(res) }

	call("list_upcoming_events", map[string]any{"limit": 50})
	search := call("search_events", map[string]any{"query": token, "include_past": true})
	call("list_newsletter_issues", map[string]any{"limit": 50})
	call("get_about", nil)

	unknownEvent := text(call("get_event", map[string]any{"slug": token + "-no-such-event"}))
	for _, w := range hiddenEvents {
		if got := text(call("get_event", map[string]any{"slug": w.Slug})); got != unknownEvent {
			t.Errorf("get_event %s = %q, want the unknown-slug text %q", w.Slug, got, unknownEvent)
		}
	}
	unknownIssue := text(call("get_newsletter_issue", map[string]any{"slug": token + "-no-such-issue"}))
	for _, c := range hiddenCampaigns {
		if got := text(call("get_newsletter_issue", map[string]any{"slug": c.Slug})); got != unknownIssue {
			t.Errorf("get_newsletter_issue %s = %q, want the unknown-slug text %q", c.Slug, got, unknownIssue)
		}
	}
	if !strings.Contains(unknownEvent, `"not_found"`) || !strings.Contains(unknownIssue, `"not_found"`) {
		t.Errorf("unknown texts = %q, %q", unknownEvent, unknownIssue)
	}

	// The REST twin of every call (#0734), through the real router.
	rest := httptest.NewServer(publicapi.NewHTTPRouter(reg, "https://www.opencircuitsf.com", nil))
	defer rest.Close()
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := rest.Client().Get(rest.URL + publicapi.Prefix + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		outputs = append(outputs, string(b))
		return resp.StatusCode, string(b)
	}
	get("events?limit=50")
	if code, body := get("events/search?query=" + token + "&include_past=true"); code != 200 || !strings.Contains(body, `"`+control.Slug+`"`) {
		t.Errorf("REST search = %d %s, want the control", code, body)
	}
	get("newsletter?limit=50")
	get("about")
	_, restUnknownEvent := get("events/" + token + "-no-such-event")
	for _, w := range hiddenEvents {
		if code, body := get("events/" + w.Slug); code != 404 || body != restUnknownEvent {
			t.Errorf("REST events/%s = %d %q, want the unknown-slug 404 %q", w.Slug, code, body, restUnknownEvent)
		}
	}
	_, restUnknownIssue := get("newsletter/" + token + "-no-such-issue")
	for _, c := range hiddenCampaigns {
		if code, body := get("newsletter/" + c.Slug); code != 404 || body != restUnknownIssue {
			t.Errorf("REST newsletter/%s = %d %q, want the unknown-slug 404 %q", c.Slug, code, body, restUnknownIssue)
		}
	}
	if code, _ := get("events/" + control.Slug); code != 200 {
		t.Errorf("REST control event = %d", code)
	}
	if code, _ := get("newsletter/" + controlC.Slug); code != 200 {
		t.Errorf("REST control issue = %d", code)
	}

	all := strings.Join(outputs, "\n")
	for _, w := range hiddenEvents {
		// The slug is a prefix-free unique title slug, so a substring
		// check cannot be satisfied by the control.
		if strings.Contains(all, `"`+w.Slug+`"`) || strings.Contains(all, "/events/"+w.Slug) {
			t.Errorf("hidden event %s appeared in a tool output", w.Slug)
		}
	}
	for _, c := range hiddenCampaigns {
		if strings.Contains(all, `"`+c.Slug+`"`) || strings.Contains(all, "/archive/"+c.Slug) {
			t.Errorf("hidden campaign %s (%s/%s) appeared in a tool output", c.Slug, c.Status, c.ArchiveStatus)
		}
	}

	// Controls come back, so the test is not vacuous.
	if !strings.Contains(text(search), "1 matching event") {
		t.Errorf("search summary = %q, want exactly the control", text(search))
	}
	sc, _ := json.Marshal(search.StructuredContent)
	if !strings.Contains(string(sc), `"`+control.Slug+`"`) {
		t.Errorf("search did not return the control %s: %s", control.Slug, sc)
	}
	if res := call("get_newsletter_issue", map[string]any{"slug": controlC.Slug}); res.IsError {
		t.Errorf("control issue %s: %s", controlC.Slug, text(res))
	}
}
