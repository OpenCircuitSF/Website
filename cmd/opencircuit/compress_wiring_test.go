package main

import (
	"compress/gzip"
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
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// TestMountAndServe_CompressionWiredAroundWholeMux is #0716: it runs the real
// mountAndServe and proves middleware.Compress wraps the whole mux — a JSON
// route (/health) and the SPA shell both come back gzipped — and that the
// shell carries Cache-Control: no-cache through seo.Site.Middleware. Setup
// mirrors TestMountAndServe_NoIndexHeaderWiredAroundWholeMux.
func TestMountAndServe_CompressionWiredAroundWholeMux(t *testing.T) {
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
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			nil, nil, nil, nil, nil, nil,
			site, requireSession, requireAdmin, nil, ready)
	}()
	// #0596: no spare StateNew connection may survive to the SIGTERM below —
	// see newShutdownWiringClient.
	client := newShutdownWiringClient()
	waitForHealthy(t, client, baseURL, errCh, ready)

	// A raw client: no transparent decompression, so the encoding is visible.
	raw := &http.Client{
		Transport: &http.Transport{DisableKeepAlives: true, DisableCompression: true},
		Timeout:   wiringHTTPTimeout,
	}
	get := func(p string) (*http.Response, string) {
		req, err := http.NewRequest(http.MethodGet, baseURL+p, nil)
		if err != nil {
			t.Fatalf("build request %s: %v", p, err)
		}
		req.Header.Set("Accept-Encoding", "gzip")
		resp, err := raw.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		defer resp.Body.Close()
		if resp.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("GET %s: Content-Encoding = %q, want gzip", p, resp.Header.Get("Content-Encoding"))
		}
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			t.Fatalf("GET %s: gzip reader: %v", p, err)
		}
		body, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("GET %s: read gzip: %v", p, err)
		}
		return resp, string(body)
	}

	if _, body := get("/health"); !strings.Contains(body, "{") {
		t.Errorf("/health decompressed body = %q, want JSON", body)
	}
	resp, body := get("/")
	if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("/ Cache-Control = %q, want no-cache", got)
	}
	if !strings.Contains(strings.ToLower(body), "<html") && !strings.Contains(strings.ToLower(body), "<!doctype") {
		t.Errorf("/ decompressed body is not HTML: %.80q", body)
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
