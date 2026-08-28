package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/seo"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// TestMountAndServe_WorkshopMutationInvalidatesSharedSEOSite is #0054's
// binding proof, carried in from #0051's review Ruling 1: that
// AdminWorkshopsHandler's cache invalidator and the *seo.Site mountAndServe
// mounts at GET /workshops/{slug} and GET /sitemap.xml are the SAME
// instance, not two independently-constructed ones. A counting fake inside
// internal/handlers (as admin_workshops_test.go already has) cannot show
// this -- it only proves InvalidateWorkshops gets CALLED, never that the
// object it's called on is the one anything actually reads from. This test
// runs the real production wiring shape (buildSEOSite -> the same *seo.Site
// handed to both NewAdminWorkshopsHandler and mountAndServe) through a real
// HTTP listener, exactly as cmd/opencircuit's other *_wiring_test.go files
// do for their own guarantees.
//
// It also pins #0051's carried-in #0073 checkbox for the REAL WorkshopSource
// adapter (workshop_seo_source.go), not just the fakes seo_test.go already
// covers. Option 1 (the "workshop:"+slug cache key, bounded by
// maxCacheEntries) was already the shape #0051 landed inside internal/seo
// itself; this test's job is to observe its ACTUAL behavior against a real,
// DB-backed WorkshopSource by counting WorkshopBySlug calls across N
// repeated requests inside the TTL. Read literally, internal/seo/seo.go's
// Render doc comment says a repeat request re-reads the source every time
// ("no longer skips that lookup on a cache hit") -- only the
// substitution/escaping step and the unbounded-key-growth defect are what
// the cache actually fixes. This test's count (5 calls for 5 requests, not
// 1) pins exactly that: it is what distinguishes option 1 from option 2 (an
// in-memory published-workshop map, under which per-request store reads
// would mostly disappear) -- so a future change that silently swaps the
// chosen option, or reintroduces a per-path cache that skips the lookup
// entirely, shows up here as a call-count change instead of passing
// unnoticed.
//
// How this test was confirmed to actually fail on broken wiring (not just
// pass on correct wiring, which proves nothing on its own): run once with
// adminWorkshopsH constructed with a nil invalidator (mirroring #0051's old
// literal nil) -- the post-mutation GET below then still returns the STALE
// title, and the test fails at that assertion. Run again with mountAndServe
// handed a SEPARATE *seo.Site (via buildSEOSite(cfg, source) called a second
// time) instead of the one passed to NewAdminWorkshopsHandler -- the mutation
// invalidates a cache nothing reads from, and the same assertion fails the
// same way. Both were exercised by hand against this test body during
// implementation and reverted; see #0054's Verification section in
// issues/0054.md for the transcript.
//
// Scope note (#0054's phase-3 review, finding 3): this test rebuilds the
// production wiring shape BY HAND inside the test body -- it constructs its
// own site/adminWorkshopsH and calls mountAndServe directly -- rather than
// exercising servePostgres itself. It proves the seam works when the same
// *seo.Site is threaded to both call sites, matching every other
// *_wiring_test.go in this package, but it does not pin servePostgres's own
// wiring: a future edit that reintroduces a literal nil or a second
// buildSEOSite call inside main.go's servePostgres would not be caught here.
func TestMountAndServe_WorkshopMutationInvalidatesSharedSEOSite(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), wiringDBConnectTimeout)
	defer cancel()

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// Truncate on both ends -- mirrors admin_wiring_test.go and
	// shutdown_budget_wiring_test.go's convention -- so a second run
	// against the same database doesn't die on
	// sessions_token_key (this test used a fixed admin token and never
	// cleaned up, which is exactly what broke re-runs; see #0054's
	// Review notes finding 2).
	registerWiringTest(t)
	t.Cleanup(func() {
		truncateAdminWiringTables(t, pool)
		pool.Close()
	})
	truncateAdminWiringTables(t, pool)

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

	store := auth.NewStore(pool)
	auditLogger := audit.New(pool)
	workshopsStore := workshops.NewStore(pool)

	// bySlugCalls counts real WorkshopBySlug calls through the production
	// adapter (workshop_seo_source.go), pinning #0073's cache-by-bucket fix
	// against a real, DB-backed source rather than seo_test.go's in-memory
	// fakes.
	var bySlugCalls int64
	source := countingWorkshopSource{
		inner:       workshopSEOSource{store: workshopsStore},
		bySlugCalls: &bySlugCalls,
	}
	site, err := buildSEOSite(cfg, source, nil)
	if err != nil {
		t.Fatalf("build seo site: %v", err)
	}

	// The production shape (#0054): the SAME *seo.Site is handed to
	// AdminWorkshopsHandler as its invalidator AND to mountAndServe below.
	adminWorkshopsH := handlers.NewAdminWorkshopsHandler(workshopsStore, site, mailing.NewCampaignStore(pool), auditLogger, cfg.BaseURL)

	requireSession := middleware.RequireSession(store)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}

	adminID := seedAdminWiringUser(t, pool, fmt.Sprintf("seo-wiring-admin-%d@example.com", testdb.Unique()), true)
	seedAdminWiringSession(t, pool, adminID, "seo-wiring-admin-token")

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, pool,
			nil, nil, nil, nil, nil, nil, /* adminInterestsH: not exercised by this test */
			nil, /* adminSubscribersH: not exercised by this test */
			nil, /* adminImportsH: not exercised */
			nil, /* adminPendingH: not exercised by this test */
			nil, /* adminSuppressionsH: not exercised by this test */
			nil, /* adminDeliverabilityH: not exercised by this test */
			nil, /* adminCampaignsH: not exercised by this test */
			nil, /* adminCampaignAudienceH: not exercised by this test */
			nil, /* adminCampaignPreviewH: not exercised by this test */
			nil, /* adminCampaignPreflightH: not exercised by this test */
			nil, /* adminCampaignStatsH: not exercised by this test */
			nil, /* adminCampaignArchiveH: not exercised by this test */
			adminWorkshopsH,
			nil,           /* adminDashboardH: not exercised by this test */
			nil, nil, nil, /* eventsH, meH, subscribeH: not exercised by this test */
			nil, nil, nil, nil, /* publicInterestsH, preferencesH, confirmH, unsubscribeH: not exercised by this test */
			nil, nil, /* publicWorkshopsH, publicListStatsH: not exercised by this test -- this test hits the SEO-rendered detail page, not the JSON API */
			nil, /* publicArchiveH: not exercised by this test */
			nil, /* sesNotifyH: not exercised by this test */
			nil, /* sendWorker: not exercised by this test */
			nil, /* outboxWorker: not exercised */
			site,
			requireSession, requireAdmin, nil, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, baseURL, errCh, ready)

	// Seed a throwaway draft workshop through the real store (never a
	// literal/seeded id, CLAUDE.md §8b) with a unique title/slug per run.
	origTitle := fmt.Sprintf("SEO Wiring Workshop %d", testdb.Unique())
	created, err := workshopsStore.Create(context.Background(), workshops.CreateInput{Title: origTitle})
	if err != nil {
		t.Fatalf("seed workshop: %v", err)
	}
	slug := created.Slug
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), wiringDBOpTimeout)
		defer cancel()
		_ = workshopsStore.Delete(ctx, created.ID)
	})

	adminCookie := &http.Cookie{Name: auth.SessionCookieName, Value: "seo-wiring-admin-token"}

	patchWorkshop := func(t *testing.T, id int64, body string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPatch, fmt.Sprintf("%s/admin/workshops/%d", baseURL, id), strings.NewReader(body))
		if err != nil {
			t.Fatalf("build PATCH request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(adminCookie)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("PATCH /admin/workshops/%d: %v", id, err)
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("PATCH /admin/workshops/%d: status=%d body=%s", id, resp.StatusCode, respBody)
		}
	}

	getBody := func(t *testing.T, path string) string {
		t.Helper()
		resp, err := client.Get(baseURL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("GET %s: read body: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: status=%d", path, resp.StatusCode)
		}
		return string(body)
	}

	// Publish the workshop -- the first mutation, before either cache has
	// ever been primed, so it has nothing to invalidate yet.
	patchWorkshop(t, created.ID, `{"status":"published"}`)

	// Prime the sitemap cache and confirm the workshop is there.
	sitemapBody := getBody(t, "/sitemap.xml")
	if !strings.Contains(sitemapBody, "/workshops/"+slug) {
		t.Fatalf("sitemap.xml missing published workshop %s right after publish:\n%s", slug, sitemapBody)
	}

	// Prime the per-path meta cache and confirm the original title rendered.
	detailPath := "/workshops/" + slug
	firstBody := getBody(t, detailPath)
	if !strings.Contains(firstBody, origTitle) {
		t.Fatalf("GET %s missing original title %q:\n%s", detailPath, origTitle, firstBody)
	}
	if got := atomic.LoadInt64(&bySlugCalls); got != 1 {
		t.Fatalf("WorkshopBySlug calls after first GET = %d, want 1", got)
	}

	// #0073 option 1 (the chosen, already-implemented shape: a syntactic
	// "workshop:"+slug cache key bounded by maxCacheEntries) pinned against
	// a REAL, DB-backed WorkshopSource: Renderer.resolve calls
	// WorkshopBySlug on every request to determine the cache key/status
	// BEFORE consulting the cache (internal/seo/seo.go's Render doc
	// comment: "a repeat request for a workshop-detail page no longer
	// skips that lookup on a cache hit"). So four more requests for the
	// SAME path within the TTL make four MORE calls -- five total, not one
	// -- which is the accepted tradeoff, not a defect: what the cache
	// bounds is unbounded key growth from distinct paths (the original
	// #0073 leak) and the re-substitution/escaping work, not this one
	// indexed read per request. A regression to option 2 (an in-memory
	// published-workshop map) would make this assertion fail differently
	// (far fewer calls than requests), which is exactly why this count is
	// worth pinning rather than leaving implicit.
	for i := 0; i < 4; i++ {
		body := getBody(t, detailPath)
		if !strings.Contains(body, origTitle) {
			t.Fatalf("GET %s (repeat %d) missing original title %q:\n%s", detailPath, i, origTitle, body)
		}
	}
	if got := atomic.LoadInt64(&bySlugCalls); got != 5 {
		t.Fatalf("WorkshopBySlug calls after 5 total GETs inside TTL = %d, want 5 (option 1: every request re-reads the store; only the substitution step is cached)", got)
	}

	// Mutate the title through AdminWorkshopsHandler over the real mux --
	// this is the call this whole test exists to prove reaches the SAME
	// *seo.Site the detail/sitemap routes above just read from.
	newTitle := origTitle + " (Updated)"
	patchWorkshop(t, created.ID, fmt.Sprintf(`{"title":%q}`, newTitle))

	updatedBody := getBody(t, detailPath)
	if strings.Contains(updatedBody, origTitle) && !strings.Contains(updatedBody, newTitle) {
		t.Fatalf("GET %s still serving the STALE title %q after a title PATCH -- the invalidator and the site the detail route reads from are not the same instance:\n%s", detailPath, origTitle, updatedBody)
	}
	if !strings.Contains(updatedBody, newTitle) {
		t.Fatalf("GET %s missing updated title %q after PATCH:\n%s", detailPath, newTitle, updatedBody)
	}
	if got := atomic.LoadInt64(&bySlugCalls); got != 6 {
		t.Fatalf("WorkshopBySlug calls after the post-mutation GET = %d, want 6 (5 above + 1 for this GET)", got)
	}

	// Cancel the workshop and confirm the sitemap -- the SECOND cache
	// InvalidateWorkshops clears -- drops it too, through the same instance.
	patchWorkshop(t, created.ID, `{"status":"canceled"}`)
	sitemapAfterCancel := getBody(t, "/sitemap.xml")
	if strings.Contains(sitemapAfterCancel, "/workshops/"+slug) {
		t.Fatalf("sitemap.xml still lists canceled workshop %s -- sitemap cache was not invalidated by the same *seo.Site instance:\n%s", slug, sitemapAfterCancel)
	}
}

// countingWorkshopSource wraps a seo.WorkshopSource and counts calls to
// WorkshopBySlug, so a test can assert internal/seo's cache is actually
// being hit (not re-querying the store) across repeated requests inside the
// TTL, against a real store-backed adapter rather than seo_test.go's
// in-memory fakes.
type countingWorkshopSource struct {
	inner       seo.WorkshopSource
	bySlugCalls *int64
}

func (c countingWorkshopSource) WorkshopBySlug(slug string) (seo.Workshop, bool, error) {
	atomic.AddInt64(c.bySlugCalls, 1)
	return c.inner.WorkshopBySlug(slug)
}

func (c countingWorkshopSource) Workshops() ([]seo.Workshop, error) {
	return c.inner.Workshops()
}
