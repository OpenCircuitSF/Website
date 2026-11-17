package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreachconn"
)

// Canary secrets for outreach_connections_status: neither may appear in any
// response body.
const (
	v1OutreachBskyCanary = "bsky-app-pw-CANARY-0697"
	v1OutreachMastoToken = "masto-token-CANARY-0697"
)

type v1OutreachEnv struct {
	*outreachEnv
	reg  *api.Registry
	api  http.Handler
	read string
	none string
}

func newV1OutreachEnv(t *testing.T) *v1OutreachEnv {
	t.Helper()
	e := newOutreachEnvPosting(t, true, fakePosting{"bluesky": true})
	reg := v1TestRegistry(e.pool, outreachOperations(e.h)...)
	h := api.NewHTTPRouter(reg, api.RouterDeps{Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test"})
	read, _ := v1TestKey(t, e.pool, apitokens.ScopeOutreachRead)
	none, _ := v1TestKey(t, e.pool, apitokens.ScopeEventsRead)
	return &v1OutreachEnv{outreachEnv: e, reg: reg, api: h, read: read, none: none}
}

func (e *v1OutreachEnv) call(t *testing.T, path, secret string) []byte {
	t.Helper()
	rec := apitest.Do(t, e.api, http.MethodGet, path, secret, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body)
	}
	return rec.Body.Bytes()
}

func TestV1Outreach_ListOutreach(t *testing.T) {
	e := newV1OutreachEnv(t)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")
	o := e.start(t, w).Outreach
	e.createPost(t, o.ID, "bluesky", "announce")
	var got api.List[outreachSummaryV1]
	if err := json.Unmarshal(e.call(t, "/api/v1/outreach?limit=100", e.read), &got); err != nil {
		t.Fatal(err)
	}
	for _, s := range got.Items {
		if s.WorkshopID != w.ID {
			continue
		}
		if s.Counts["draft"] != 1 {
			t.Errorf("counts = %v", s.Counts)
		}
		at, err := time.Parse(time.RFC3339, *s.StartsAt)
		if err != nil || !at.Equal(*w.StartsAt) {
			t.Errorf("starts_at = %q (%v)", *s.StartsAt, err)
		}
		if !strings.HasSuffix(*s.StartsAt, "-08:00") {
			t.Errorf("starts_at %q is not Pacific", *s.StartsAt)
		}
		return
	}
	t.Errorf("workshop %d missing from %+v", w.ID, got.Items)
}

// TestV1Outreach_PanelMatchesAdmin: get_event_outreach is the admin panel
// field for field, plus started.
func TestV1Outreach_PanelMatchesAdmin(t *testing.T) {
	e := newV1OutreachEnv(t)
	w := seedOutreachWorkshop(t, e.pool, e.ws, "published")

	var before map[string]any
	if err := json.Unmarshal(e.call(t, fmt.Sprintf("/api/v1/events/%d/outreach", w.ID), e.read), &before); err != nil {
		t.Fatal(err)
	}
	if before["started"] != false || before["outreach"] != nil {
		t.Errorf("before start: started=%v outreach=%v", before["started"], before["outreach"])
	}

	o := e.start(t, w).Outreach
	e.createPost(t, o.ID, "bluesky", "announce")
	code, adminBody := e.do(t, http.MethodGet, fmt.Sprintf("/admin/workshops/%d/outreach", w.ID), "")
	if code != http.StatusOK {
		t.Fatalf("admin = %d", code)
	}
	var admin, v1 map[string]any
	if err := json.Unmarshal(adminBody, &admin); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(e.call(t, fmt.Sprintf("/api/v1/events/%d/outreach", w.ID), e.read), &v1); err != nil {
		t.Fatal(err)
	}
	if v1["started"] != true {
		t.Errorf("started = %v", v1["started"])
	}
	delete(v1, "started")
	ws, _ := v1["workshop"].(map[string]any)
	posts, _ := v1["posts"].([]any)
	if len(posts) == 0 {
		t.Fatal("v1 panel has no posts")
	}
	post, _ := posts[0].(map[string]any)
	for name, v := range map[string]any{"workshop.starts_at": ws["starts_at"], "posts[0].created_at": post["created_at"]} {
		s, _ := v.(string)
		if !strings.HasSuffix(s, "-08:00") && !strings.HasSuffix(s, "-07:00") {
			t.Errorf("v1 %s = %q, want a Pacific offset", name, s)
		}
	}
	// The admin panel is UTC and v1 is Pacific: compare instants.
	admin, v1 = utcTimes(admin).(map[string]any), utcTimes(v1).(map[string]any)
	if !reflect.DeepEqual(admin, v1) {
		t.Errorf("v1 panel differs from admin:\nadmin %v\nv1    %v", admin, v1)
	}
}

// utcTimes rewrites every RFC 3339 string in a decoded JSON value to UTC.
func utcTimes(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = utcTimes(e)
		}
	case []any:
		for i, e := range x {
			x[i] = utcTimes(e)
		}
	case string:
		if t, err := time.Parse(time.RFC3339, x); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return v
}

func TestV1Outreach_UnknownEventAndScope(t *testing.T) {
	e := newV1OutreachEnv(t)
	rec := apitest.Do(t, e.api, http.MethodGet, "/api/v1/events/999999999/outreach", e.read, "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown event = %d %s", rec.Code, rec.Body)
	}
	for _, p := range []string{"/api/v1/outreach", "/api/v1/events/1/outreach", "/api/v1/outreach/connections"} {
		if rec := apitest.Do(t, e.api, http.MethodGet, p, e.none, ""); rec.Code != http.StatusForbidden {
			t.Errorf("%s without outreach:read = %d", p, rec.Code)
		}
	}
}

func TestV1Outreach_ConnectionsStatusLeaksNoSecret(t *testing.T) {
	e := newV1OutreachEnv(t)
	ctx := context.Background()
	e.pool.Exec(ctx, `DELETE FROM outreach_connection_values`)
	t.Cleanup(func() { e.pool.Exec(context.Background(), `DELETE FROM outreach_connection_values`) })
	mgr := outreachconn.NewManager(outreachconn.NewStore(e.pool), map[string]string{
		outreachconn.KeyBlueskyHandle: "oc.example.test", outreachconn.KeyBlueskyAppPassword: v1OutreachBskyCanary,
		outreachconn.KeyMastodonBaseURL: "https://masto.example.test",
	}, connQuiet())
	if err := mgr.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Apply(ctx, map[string]string{outreachconn.KeyMastodonToken: v1OutreachMastoToken}, time.Now()); err != nil {
		t.Fatalf("save mastodon token: %v", err)
	}
	e.h.SetConnections(NewAdminOutreachConnectionsHandler(mgr, audit.New(e.pool)))

	body := e.call(t, "/api/v1/outreach/connections", e.read)
	for _, secret := range []string{v1OutreachBskyCanary, v1OutreachMastoToken, "CANARY"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("response carries %q: %s", secret, body)
		}
	}
	var got outreachConnectionsV1
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	fields := map[string]outreachFieldStatusV1{}
	for _, g := range got.Groups {
		for _, f := range g.Fields {
			fields[f.Key] = f
		}
	}
	if f := fields[outreachconn.KeyBlueskyAppPassword]; !f.Set || f.Source != string(outreachconn.SourceEnvironment) {
		t.Errorf("bluesky app password = %+v", f)
	}
	if f := fields[outreachconn.KeyMastodonToken]; !f.Set || f.Source != string(outreachconn.SourceDatabase) {
		t.Errorf("mastodon token = %+v", f)
	}
	var bsky *outreachChannelStatusV1
	for i := range got.Channels {
		if got.Channels[i].Name == "bluesky" {
			bsky = &got.Channels[i]
		}
	}
	if bsky == nil || !bsky.Configured || !bsky.Enabled {
		t.Errorf("bluesky channel = %+v", bsky)
	}
}

// TestV1Outreach_MCPToolsFollowScope: the three tools are listed for an
// outreach:read key and absent for a key without it.
func TestV1Outreach_MCPToolsFollowScope(t *testing.T) {
	e := newV1OutreachEnv(t)
	mcpH := mcpserver.NewHandler(mcpserver.Deps{Registry: e.reg, Resolver: apitokens.NewStore(e.pool), Limiter: middleware.NewAPITokenRateLimiter()})
	want := []string{"list_outreach", "get_event_outreach", "outreach_connections_status"}
	for _, tc := range []struct {
		key    string
		listed bool
	}{{e.read, true}, {e.none, false}} {
		cs := mcptest.Connect(t, mcpH, tc.key)
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		have := map[string]bool{}
		for _, tool := range res.Tools {
			have[tool.Name] = true
		}
		for _, n := range want {
			if have[n] != tc.listed {
				t.Errorf("tool %s listed=%v, want %v", n, have[n], tc.listed)
			}
		}
	}
}
