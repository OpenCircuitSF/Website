package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/mcpserver/mcptest"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicmcp"
	"github.com/brennanMKE/OpenCircuitSF/internal/seo"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// fakePublicMCPWorkshopStore serves one published upcoming event.
type fakePublicMCPWorkshopStore struct{}

func (fakePublicMCPWorkshopStore) event() workshops.Workshop {
	start := time.Date(2099, 12, 2, 18, 0, 0, 0, time.UTC)
	pub := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return workshops.Workshop{Slug: "public-mcp-wiring", Title: "Public MCP wiring", StartsAt: &start, Status: workshops.StatusPublished, PublishedAt: &pub}
}

func (f fakePublicMCPWorkshopStore) ListVisible(context.Context, time.Time) (upcoming, past []workshops.Workshop, err error) {
	return []workshops.Workshop{f.event()}, nil, nil
}

func (f fakePublicMCPWorkshopStore) GetBySlug(_ context.Context, slug string) (workshops.Workshop, error) {
	if slug == "public-mcp-wiring" {
		return f.event(), nil
	}
	return workshops.Workshop{}, workshops.ErrNotFound
}

// fakePublicMCPArchiveStore has no issues.
type fakePublicMCPArchiveStore struct{}

func (fakePublicMCPArchiveStore) ListArchived(context.Context) ([]mailing.Campaign, error) {
	return nil, nil
}

func (fakePublicMCPArchiveStore) GetBySlug(context.Context, string) (mailing.Campaign, error) {
	return mailing.Campaign{}, mailing.ErrCampaignNotFound
}

// TestMountAndServe_PublicMCPMounted is #0729: the real mountAndServe
// mounts the public MCP server at /mcp when both public handlers exist, it
// ignores Authorization, and it never answers at /admin/mcp. Setup mirrors
// TestMountAndServe_EventsICSRouteServedCompressed.
func TestMountAndServe_PublicMCPMounted(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg := &config.Config{
		Port:             port,
		BaseURL:          baseURL,
		WebAuthnRPID:     "localhost",
		WebAuthnRPOrigin: baseURL,
	}

	site, err := buildSEOSite(cfg, nil, nil)
	if err != nil {
		t.Fatalf("build seo site: %v", err)
	}

	requireSession := middleware.RequireSession(auth.NewStore(nil))
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	publicWorkshopsH := handlers.NewPublicWorkshopsHandler(fakePublicMCPWorkshopStore{}, nil)
	publicArchiveH := handlers.NewPublicArchiveHandler(fakePublicMCPArchiveStore{})

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, okPinger{},
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, publicWorkshopsH, nil, nil,
			publicArchiveH, nil, nil, nil, nil, nil,
			site, requireSession, requireAdmin, nil, ready)
	}()
	client := newShutdownWiringClient()
	waitForHealthy(t, client, baseURL, errCh, ready)

	endpoint := baseURL + publicmcp.Path
	getEvent := func(bearer string) (*mcp.ClientSession, string) {
		cs := mcptest.ConnectURL(t, endpoint, &mcptest.Transport{Bearer: bearer})
		res := mcptest.Call(t, cs, "get_event", map[string]any{"slug": "public-mcp-wiring"})
		if res.IsError {
			t.Fatalf("get_event: %s", mcptest.Text(res))
		}
		b, _ := json.Marshal(res.StructuredContent)
		return cs, string(b)
	}
	cs, plain := getEvent("")
	tools, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var names []string
	for _, tl := range tools.Tools {
		names = append(names, tl.Name)
	}
	slices.Sort(names)
	want := []string{"get_about", "get_event", "get_newsletter_issue", "list_newsletter_issues", "list_upcoming_events", "search_events"}
	if !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
	var ev struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(plain), &ev); err != nil || !strings.HasPrefix(ev.URL, baseURL+"/events/") {
		t.Errorf("get_event url = %q (%v), want under %s/events/", ev.URL, err, baseURL)
	}
	if _, withKey := getEvent("ocsf_x"); withKey != plain {
		t.Errorf("result with Authorization differs:\n%s\n%s", withKey, plain)
	}
	about := mcptest.Call(t, cs, "get_about", nil)
	b, _ := json.Marshal(about.StructuredContent)
	var a struct {
		Name         string `json:"name"`
		ContactEmail string `json:"contact_email"`
	}
	if err := json.Unmarshal(b, &a); err != nil || a.Name != seo.SiteName || a.ContactEmail != "contact@opencircuitsf.com" {
		t.Errorf("get_about = %s (%v), want name %q and the contact address", b, err, seo.SiteName)
	}

	raw := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: wiringHTTPTimeout}
	do := func(method, path, body string) (int, string, string) {
		req, err := http.NewRequest(method, baseURL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := raw.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), string(rb)
	}
	if _, ct, _ := do(http.MethodGet, "/.well-known/oauth-protected-resource", ""); strings.HasPrefix(ct, "application/json") {
		t.Errorf("/.well-known/oauth-protected-resource is JSON (%q); the public server serves no OAuth metadata", ct)
	}
	const list = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		code, _, body := do(m, "/admin/mcp", list)
		if code == http.StatusOK || strings.Contains(body, `"jsonrpc"`) {
			t.Errorf("%s /admin/mcp = %d %.200s; the public server must not answer there", m, code, body)
		}
	}

	checkPublicAPIMounted(t, baseURL)
	checkServerCardMounted(t, baseURL)

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("mountAndServe returned %v after SIGTERM, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("mountAndServe did not return within 10s of SIGTERM")
	}
	signal.Reset(syscall.SIGTERM)
}

// checkPublicAPIMounted is #0734's wiring check, run inside
// TestMountAndServe_PublicMCPMounted against the same real mountAndServe:
// /api/public/v1/ is served as JSON with CORS *, noindex and gzip, and a
// wrong method or unknown path is a JSON error, not the SPA.
func checkPublicAPIMounted(t *testing.T, baseURL string) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true, DisableCompression: true}, Timeout: wiringHTTPTimeout}
	do := func(method, path string, gzip bool) (*http.Response, string) {
		req, err := http.NewRequest(method, baseURL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if gzip {
			req.Header.Set("Accept-Encoding", "gzip")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	resp, _ := do(http.MethodGet, "/api/public/v1/about", false)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") ||
		resp.Header.Get("Access-Control-Allow-Origin") != "*" || resp.Header.Get("X-Robots-Tag") != "noindex" {
		t.Errorf("GET about = %d %v", resp.StatusCode, resp.Header)
	}
	if resp, _ := do(http.MethodGet, "/api/public/v1/about", true); resp.Header.Get("Content-Encoding") != "gzip" {
		t.Errorf("about with Accept-Encoding: gzip = Content-Encoding %q", resp.Header.Get("Content-Encoding"))
	}
	_, body := do(http.MethodGet, "/api/public/v1/events/public-mcp-wiring", false)
	var ev struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(body), &ev); err != nil || !strings.HasPrefix(ev.URL, baseURL+"/events/") {
		t.Errorf("events/public-mcp-wiring url = %q (%v)", ev.URL, err)
	}
	for _, c := range []struct {
		method, path string
		code         int
	}{{http.MethodPost, "/api/public/v1/events", 405}, {http.MethodGet, "/api/public/v1/openapi.json", 200}, {http.MethodGet, "/api/public/v1/nope", 404}} {
		resp, body := do(c.method, c.path, false)
		if resp.StatusCode != c.code || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") || strings.Contains(body, "<html") {
			t.Errorf("%s %s = %d %q %.120s, want %d JSON", c.method, c.path, resp.StatusCode, resp.Header.Get("Content-Type"), body, c.code)
		}
	}
}

// checkServerCardMounted is #0731's wiring check: the real mountAndServe
// serves the server card and AI catalog, and leaves every other
// /.well-known/ path (atproto-did, served by Apache from disk) unclaimed.
func checkServerCardMounted(t *testing.T, baseURL string) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: wiringHTTPTimeout}
	get := func(path string) (int, string, []byte) {
		resp, err := client.Get(baseURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Content-Type"), b
	}
	code, ct, body := get(publicmcp.CardPath)
	var card struct {
		Remotes []struct {
			URL string `json:"url"`
		} `json:"remotes"`
	}
	if err := json.Unmarshal(body, &card); code != http.StatusOK || ct != publicmcp.CardMediaType || err != nil ||
		len(card.Remotes) != 1 || card.Remotes[0].URL != baseURL+publicmcp.Path {
		t.Errorf("GET %s = %d %q %.300s", publicmcp.CardPath, code, ct, body)
	}
	if code, ct, _ := get(publicmcp.CatalogPath); code != http.StatusOK || ct != publicmcp.CatalogMediaType {
		t.Errorf("GET %s = %d %q", publicmcp.CatalogPath, code, ct)
	}
	if code, _, _ := get("/.well-known/atproto-did"); code == http.StatusOK {
		t.Error("GET /.well-known/atproto-did = 200 from Go; Apache serves it from disk")
	}
}
