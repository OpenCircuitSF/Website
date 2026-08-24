package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// TestMountAndServe_RateLimitsSubscribe closes the same kind of gap
// TestMountAndServe_RateLimitsAuthLoginStart closes for the auth routes
// (#0008's review): #0026's acceptance criteria state a per-IP token-bucket
// limit of 5/min, burst 3 on POST /api/subscribe, but nothing proves that
// limiter is actually WIRED to the route in production rather than merely
// constructed and unused. This drives POST /api/subscribe over a real HTTP
// listener via mountAndServe — the exact function servePostgres calls — past
// its burst of 3 from one IP and expects the 4th request within the window
// to come back 429, matching the auth-route precedent exactly.
func TestMountAndServe_RateLimitsSubscribe(t *testing.T) {
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
	defer func() {
		_, _ = pool.Exec(context.Background(), `TRUNCATE subscriber_interests, subscribers RESTART IDENTITY CASCADE`)
		pool.Close()
	}()
	if _, err := pool.Exec(ctx, `TRUNCATE subscriber_interests, subscribers RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

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

	subscribeH := handlers.NewSubscribeHandler(
		subscribers.NewStore(pool), interests.NewStore(pool), &mailing.RecordingMailer{},
		handlers.NoSuppressions{}, nil, audit.New(pool), cfg.BaseURL, slog.Default(),
	)

	// mountAndServe calls requireSession/requireAdmin immediately while
	// mounting the (unused-by-this-test) session/admin-guarded routes, so a
	// nil func here would panic before ListenAndServe even starts. Neither
	// this test's requests nor its assertions touch those routes; a
	// passthrough is enough to let mounting complete.
	passthrough := func(next http.Handler) http.Handler { return next }

	// #0054: a real *seo.Site, built the same way production does.
	site, err := buildSEOSite(cfg, nil)
	if err != nil {
		t.Fatalf("build seo site: %v", err)
	}

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		// mountAndServe blocks in http.ListenAndServe for the life of the
		// process; the test never calls it again, so the goroutine and its
		// listener simply outlive this test within the test binary.
		errCh <- mountAndServe(cfg, pool,
			nil, nil, nil, nil, nil, nil, /* adminInterestsH: not exercised by this test */
			nil, /* adminSubscribersH: not exercised by this test */
			nil, /* adminSuppressionsH: not exercised by this test */
			nil, /* adminCampaignsH: not exercised by this test */
			nil, /* adminCampaignAudienceH: not exercised by this test */
			nil, /* adminCampaignPreviewH: not exercised by this test */
			nil, /* adminCampaignPreflightH: not exercised by this test */
			nil, /* adminCampaignStatsH: not exercised by this test */
			nil, /* adminWorkshopsH: not exercised by this test */
			nil, /* adminDashboardH: not exercised by this test */
			nil, nil, subscribeH,
			nil, nil, nil, nil, nil, /* publicInterestsH, preferencesH, confirmH, unsubscribeH, publicWorkshopsH: not exercised by this test */
			nil, /* sesNotifyH: not exercised by this test */
			nil, /* sendWorker: not exercised by this test */
			site,
			passthrough, passthrough, nil, ready)
	}()

	client := &http.Client{Timeout: wiringHTTPTimeout}
	waitForHealthy(t, client, baseURL, errCh, ready)

	const clientIP = "198.51.100.77" // TEST-NET-2 (RFC 5737)

	for i := 1; i <= 3; i++ {
		resp, err := doSubscribeRequest(client, baseURL, clientIP, i)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("request %d: got 429 before exhausting the burst of 3 — subscribeLimiter's burst is misconfigured or IP bucketing is broken", i)
		}
	}

	resp, err := doSubscribeRequest(client, baseURL, clientIP, 4)
	if err != nil {
		t.Fatalf("request 4: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("request 4: status = %d, want %d — POST /api/subscribe does not appear to be wired to subscribeLimiter in mountAndServe",
			resp.StatusCode, http.StatusTooManyRequests)
	}

	select {
	case err := <-errCh:
		t.Fatalf("mountAndServe exited unexpectedly: %v", err)
	default:
	}
}

// doSubscribeRequest issues one well-formed POST /api/subscribe request
// against baseURL, tagged with clientIP via X-Forwarded-For so the rate
// limiter buckets every call in this test to the same synthetic client
// regardless of the loopback source address the real TCP connection uses.
// n varies the email so distinct requests don't collide on the unique
// constraint, though the rate limiter itself fires before the handler ever
// looks at the body.
func doSubscribeRequest(client *http.Client, baseURL, clientIP string, n int) (*http.Response, error) {
	body, _ := json.Marshal(map[string]any{
		"email":       fmt.Sprintf("zz-ratelimit-%d-%d@example.com", testdb.Unique(), n),
		"interests":   []string{},
		"website":     "",
		"rendered_at": time.Now().Add(-5 * time.Second).UnixMilli(),
	})
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/subscribe", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", clientIP)
	return client.Do(req)
}
