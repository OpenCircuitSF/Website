package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicapi"
	"github.com/brennanMKE/OpenCircuitSF/internal/publicmcp"
	"github.com/brennanMKE/OpenCircuitSF/internal/seo"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// publicWorkshopLister is what handlers.NewPublicWorkshopsHandler reads.
type publicWorkshopLister interface {
	ListVisible(context.Context, time.Time) ([]workshops.Workshop, []workshops.Workshop, error)
	GetBySlug(context.Context, string) (workshops.Workshop, error)
}

// titledWorkshopStore is fakePublicMCPWorkshopStore with a settable title.
type titledWorkshopStore struct {
	fakePublicMCPWorkshopStore
	mu    sync.Mutex
	title string
}

func (s *titledWorkshopStore) setTitle(t string) { s.mu.Lock(); s.title = t; s.mu.Unlock() }

func (s *titledWorkshopStore) ListVisible(context.Context, time.Time) (upcoming, past []workshops.Workshop, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w := s.event()
	w.Title = s.title
	return []workshops.Workshop{w}, nil, nil
}

// startPublicServer runs the real mountAndServe with the public handlers
// (#0730) and returns its base URL and *seo.Site; stopped on cleanup.
func startPublicServer(t *testing.T, store publicWorkshopLister) (string, *seo.Site) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("close probe listener: %v", err)
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	cfg := &config.Config{Port: port, BaseURL: baseURL, WebAuthnRPID: "localhost", WebAuthnRPOrigin: baseURL}
	site, err := buildSEOSite(cfg, nil, nil)
	if err != nil {
		t.Fatalf("build seo site: %v", err)
	}
	requireSession := middleware.RequireSession(auth.NewStore(nil))
	requireAdmin := func(next http.Handler) http.Handler { return requireSession(middleware.RequireAdmin(next)) }
	publicWorkshopsH := handlers.NewPublicWorkshopsHandler(store, nil)
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
	waitForHealthy(t, newShutdownWiringClient(), baseURL, errCh, ready)
	t.Cleanup(func() {
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Errorf("send SIGTERM: %v", err)
			return
		}
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("mountAndServe returned %v after SIGTERM, want nil", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("mountAndServe did not return within 10s of SIGTERM")
		}
		signal.Reset(syscall.SIGTERM)
	})
	return baseURL, site
}

func publicDo(t *testing.T, method, url, xff, body string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("X-Forwarded-For", xff)
	c := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: wiringHTTPTimeout}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(b)
}

// TestMountAndServe_PublicMCPRateLimited is #0730: /mcp and /api/public/v1/
// share one per-IP budget of publicapi.RequestsPerMinute, keyed on the
// trusted X-Forwarded-For (the test client is a loopback peer).
func TestMountAndServe_PublicMCPRateLimited(t *testing.T) {
	baseURL, _ := startPublicServer(t, fakePublicMCPWorkshopStore{})
	const list = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	for i := 0; i < publicapi.RequestsPerMinute-1; i++ {
		if code, _, body := publicDo(t, http.MethodPost, baseURL+publicmcp.Path, "198.51.100.9", list); code == http.StatusTooManyRequests {
			t.Fatalf("request %d: 429 %s", i, body)
		}
	}
	// The REST surface spends the same budget: this is request 60.
	if code, _, _ := publicDo(t, http.MethodGet, baseURL+publicapi.Prefix+"events", "198.51.100.9", ""); code == http.StatusTooManyRequests {
		t.Fatal("REST request 60 refused")
	}
	code, hdr, body := publicDo(t, http.MethodPost, baseURL+publicmcp.Path, "198.51.100.9", list)
	if code != http.StatusTooManyRequests || hdr.Get("Retry-After") == "" || !strings.Contains(body, `"rate_limited"`) || strings.Contains(body, "jsonrpc") {
		t.Fatalf("request 61 = %d Retry-After %q %s, want 429 rate_limited", code, hdr.Get("Retry-After"), body)
	}
	if code, _, _ := publicDo(t, http.MethodGet, baseURL+publicapi.Prefix+"events", "198.51.100.9", ""); code != http.StatusTooManyRequests {
		t.Errorf("REST over budget = %d, want 429", code)
	}
	if code, _, _ := publicDo(t, http.MethodPost, baseURL+publicmcp.Path, "198.51.100.10", list); code == http.StatusTooManyRequests {
		t.Error("another IP shares the budget")
	}
}

// TestMountAndServe_PublicCacheInvalidatedBySite is #0730: the list cache
// serves a repeat call, and site.Invalidate (what every admin publish or
// edit calls) clears it.
func TestMountAndServe_PublicCacheInvalidatedBySite(t *testing.T) {
	store := &titledWorkshopStore{title: "Title A"}
	baseURL, site := startPublicServer(t, store)
	get := func() string {
		code, _, body := publicDo(t, http.MethodGet, baseURL+publicapi.Prefix+"events", "198.51.100.20", "")
		if code != http.StatusOK {
			t.Fatalf("GET events = %d %s", code, body)
		}
		return body
	}
	if b := get(); !strings.Contains(b, "Title A") {
		t.Fatalf("first call %s", b)
	}
	store.setTitle("Title B")
	if b := get(); !strings.Contains(b, "Title A") {
		t.Errorf("second call not cached: %s", b)
	}
	site.Invalidate()
	if b := get(); !strings.Contains(b, "Title B") {
		t.Errorf("after site.Invalidate: %s", b)
	}
}
