package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/events"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// TestMountAndServe_EventsRequiresSessionAndAdmin is #0048's proof that GET
// /api/events is session+admin gated at the real mux (mountAndServe), not
// just requireSession as it was before this issue. It is deliberately NOT
// folded into TestMountAndServe_AdminRoutesRequireSessionAndAdmin
// (admin_wiring_test.go): that test drives adminRoutes()'s table, and
// /api/events is deliberately NOT registered through that table (see
// main.go's route comment and admin_routes_ast_test.go's own doc comment
// for why /api/* routes carry their own guard at the call site instead) —
// this test proves that inline guard directly, the same way
// subscribe_wiring_test.go/unsubscribe_wiring_test.go prove their own
// routes' auth shape without going through adminRoutes either.
//
// Guard-removal proof (see #0048's ## Verification for the transcript):
// with mountAndServe's "GET /api/events" registration temporarily changed
// from requireAdmin(...) back to requireSession(...), the "non-admin
// session" case below fails (got 200, want 403).
func TestMountAndServe_EventsRequiresSessionAndAdmin(t *testing.T) {
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
	requireSession := middleware.RequireSession(store)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}

	broker := events.NewBroker()
	eventsH := handlers.NewEventsHandler(broker)

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, pool,
			nil, nil, nil, nil, nil, nil, /* adminInterestsH: not exercised */
			nil,               /* adminSubscribersH: not exercised */
			nil,               /* adminSuppressionsH: not exercised */
			nil,               /* adminCampaignsH: not exercised */
			nil,               /* adminCampaignAudienceH: not exercised */
			nil,               /* adminCampaignPreviewH: not exercised */
			nil,               /* adminCampaignPreflightH: not exercised */
			eventsH, nil, nil, /* meH, subscribeH: not exercised */
			nil, nil, nil, nil, /* publicInterestsH, preferencesH, confirmH, unsubscribeH: not exercised */
			nil, /* sesNotifyH: not exercised */
			nil, /* sendWorker: not exercised */
			requireSession, requireAdmin, nil, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, baseURL, errCh, ready)

	nonAdminID := seedAdminWiringUser(t, pool, fmt.Sprintf("zz-events-nonadmin-%d@example.com", time.Now().UnixNano()), false)
	adminID := seedAdminWiringUser(t, pool, fmt.Sprintf("zz-events-admin-%d@example.com", time.Now().UnixNano()), true)
	seedAdminWiringSession(t, pool, nonAdminID, "zz-events-nonadmin-token")
	seedAdminWiringSession(t, pool, adminID, "zz-events-admin-token")

	cases := []struct {
		name   string
		cookie string
		want   func(status int) bool
	}{
		{"no session", "", func(status int) bool { return status == http.StatusUnauthorized }},
		{"non-admin session", "zz-events-nonadmin-token", func(status int) bool { return status == http.StatusForbidden }},
		{"admin session", "zz-events-admin-token", func(status int) bool { return status == http.StatusOK }},
	}
	for _, c := range cases {
		req, err := http.NewRequest(http.MethodGet, baseURL+"/api/events", nil)
		if err != nil {
			t.Fatalf("%s: build request: %v", c.name, err)
		}
		if c.cookie != "" {
			req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: c.cookie})
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s: request: %v", c.name, err)
		}
		resp.Body.Close()
		if !c.want(resp.StatusCode) {
			t.Errorf("%s: GET /api/events status = %d, unexpected for this case", c.name, resp.StatusCode)
		}
	}

	select {
	case err := <-errCh:
		t.Fatalf("mountAndServe exited unexpectedly: %v", err)
	default:
	}
}
