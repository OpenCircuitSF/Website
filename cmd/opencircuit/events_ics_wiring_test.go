package main

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/handlers"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// fakeICSStore serves one published upcoming event for the #0732 wiring test.
type fakeICSStore struct{}

func (fakeICSStore) ListVisible(_ context.Context, now time.Time) (upcoming, past []workshops.Workshop, err error) {
	start := now.Add(48 * time.Hour)
	pub := now.Add(-time.Hour)
	return []workshops.Workshop{{
		ID: 7, Slug: "ics-wiring-slug", Title: "ICS wiring", StartsAt: &start,
		Status: workshops.StatusPublished, PublishedAt: &pub, UpdatedAt: now,
	}}, nil, nil
}

func (fakeICSStore) GetBySlug(context.Context, string) (workshops.Workshop, error) {
	return workshops.Workshop{}, workshops.ErrNotFound
}

// TestMountAndServe_EventsICSRouteServedCompressed is #0732: the real
// mountAndServe registers GET /events.ics on the public workshops handler,
// and middleware.Compress gzips its text/calendar body. Setup mirrors
// TestMountAndServe_CompressionWiredAroundWholeMux.
func TestMountAndServe_EventsICSRouteServedCompressed(t *testing.T) {
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
	publicWorkshopsH := handlers.NewPublicWorkshopsHandler(fakeICSStore{}, nil)

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, okPinger{},
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, publicWorkshopsH, nil, nil,
			nil, nil, nil, nil, nil, nil,
			site, requireSession, requireAdmin, nil, ready)
	}()
	client := newShutdownWiringClient()
	waitForHealthy(t, client, baseURL, errCh, ready)

	raw := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true, DisableCompression: true},
		Timeout:   wiringHTTPTimeout,
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/events.ics", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := raw.Do(req)
	if err != nil {
		t.Fatalf("GET /events.ics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/calendar; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "max-age") {
		t.Errorf("Cache-Control = %q, want a max-age", got)
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	body := strings.ReplaceAll(string(b), "\r\n ", "")
	if !strings.Contains(body, "BEGIN:VCALENDAR") || !strings.Contains(body, "/events/ics-wiring-slug") {
		t.Errorf("decompressed body missing calendar or event URL:\n%s", body)
	}

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
