package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// okPinger stands in for the DB pool behind mountAndServe's GET /health
// route: this test drives only routes that never reach the database (a
// cookieless 401 from RequireSession, a mux-level 405, the SPA catch-all,
// and the seo handlers over nil sources), so a real Postgres connection
// would be pure setup cost.
type okPinger struct{}

func (okPinger) Ping(context.Context) error { return nil }

// TestMountAndServe_NoIndexHeaderWiredAroundWholeMux is #0525: #0518's
// TestNoIndexAPIMiddleware_SetsHeaderOnAPIRoutesOnly proves the middleware
// COMPOSES correctly over a hand-built mux, but not that main.go's
// mountAndServe — the exact function servePostgres calls in production —
// actually WIRES it. The wrap is applied once, to the whole mux, just before
// the outer middleware; a future edit to mountAndServe that dropped it (or
// narrowed it to a sub-mux) would leave every per-middleware suite green.
// This test runs the real mountAndServe over a real socket and asserts the
// header in both directions: present on every /api/ response regardless of
// status (a 401 from the session guard, a 405 from method mismatch, a 404
// from the SPA catch-all), and absent on the four non-API surfaces the
// header exists to keep indexable — including /apiary, which shares the
// prefix TEXTUALLY but not the "/api/" path prefix the middleware matches
// on.
//
// Handler arguments are typed nils except where a route this test actually
// drives needs a real instance: registration only evaluates method values,
// which is legal on a nil pointer receiver, and none of the nil handlers'
// methods is ever called (the 401 fires before Me runs, the 405 never
// leaves the mux, and the 404 is the SPA catch-all's own).
func TestMountAndServe_NoIndexHeaderWiredAroundWholeMux(t *testing.T) {
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

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, okPinger{},
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			nil, nil, nil, nil, nil,
			site, requireSession, requireAdmin, nil, ready)
	}()
	waitForHealthy(t, &http.Client{Timeout: wiringHTTPTimeout}, baseURL, errCh, ready)

	client := &http.Client{Timeout: wiringHTTPTimeout}

	present := []struct {
		method     string
		path       string
		wantStatus int
	}{
		{http.MethodGet, "/api/me", http.StatusUnauthorized},
		{http.MethodPost, "/api/me", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/not-a-real-route", http.StatusNotFound},
	}
	for _, c := range present {
		req, err := http.NewRequest(c.method, baseURL+c.path, nil)
		if err != nil {
			t.Fatalf("build request %s %s: %v", c.method, c.path, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", c.method, c.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != c.wantStatus {
			t.Errorf("%s %s: status = %d, want %d", c.method, c.path, resp.StatusCode, c.wantStatus)
		}
		if got := resp.Header.Get("X-Robots-Tag"); got != "noindex" {
			t.Errorf("%s %s: X-Robots-Tag = %q, want %q — the header must be wired around the whole mux", c.method, c.path, got, "noindex")
		}
	}

	absent := []string{"/", "/sitemap.xml", "/robots.txt", "/apiary"}
	for _, p := range absent {
		resp, err := client.Get(baseURL + p)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("X-Robots-Tag"); got != "" {
			t.Errorf("GET %s: X-Robots-Tag = %q, want unset — this surface must stay indexable", p, got)
		}
	}

	// Shut the server down cleanly so this test leaves no listener bound and
	// no SIGTERM registration behind: mountAndServe's own signal handling
	// consumes the SIGTERM, drains (nothing is in flight), and returns nil.
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

	// The signal.Stop inside mountAndServe has unregistered its channel; drop
	// this process's own test context back to default signal behavior so a
	// later Ctrl-C behaves normally.
	signal.Reset(syscall.SIGTERM)
}
