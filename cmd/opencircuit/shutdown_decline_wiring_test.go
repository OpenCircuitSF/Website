package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// TestMountAndServe_ShutdownTimeoutNamesWhatHeldItOpen is #0536: before the
// fix, a shutdown that outlived its grace budget surfaced from
// mountAndServe as a bare context.DeadlineExceeded — indistinguishable, to
// an operator reading the journal, between the benign designed case (an
// open GET /api/events stream, which http.Server.Shutdown does not cancel)
// and a genuine hang (e.g. a half-open connection that never finished
// sending a request). mountAndServe now tracks the connections its server
// reports as not-yet-idle and, when Shutdown returns the deadline error,
// both logs and returns a named, self-describing error stating what was
// still active at expiry.
//
// The decline shape this test drives is deliberately NOT the SSE case the
// budget test above covers: it opens a raw TCP connection and sends
// complete request headers announcing a body that never fully arrives,
// which parks the server-side connection in StateActive inside the
// handler's body read. Under #0536 this test sent an unterminated header
// instead (StateNew); #0598 made a shutdown holding only StateNew
// connections close them and return nil — no request was ever read, so
// nothing is aborted — and that shape is now covered by
// TestMountAndServe_ShutdownClosesNeverUsedConnections below. The named
// error must still appear for any non-new connection, which this test pins.
//
// The test shrinks the production budget to declineShutdownBudget (package
// var, the same override the budget test uses) so the timed-out shutdown is
// deterministic: Shutdown never treats an active connection as idle, so the
// budget always expires with this connection still recorded.
//
// Handler arguments are typed nils except where a route this test actually
// drives needs a real instance — the same shape noindex_wiring_test.go
// uses with okPinger, since the only route it exercises is GET /health
// plus one raw connection that never completes a request.
func TestMountAndServe_ShutdownTimeoutNamesWhatHeldItOpen(t *testing.T) {
	// #0598: a request whose headers are complete but whose body never
	// arrives is StateActive, which must still take the error path. (The
	// half-open-header shape this test used under #0536 is StateNew, which
	// #0598 now closes cleanly; see the test below.)
	err := runShutdownHoldingConn(t, func(t *testing.T, port int) {
		raw, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("dial raw connection: %v", err)
		}
		t.Cleanup(func() { raw.Close() })
		if _, err := raw.Write([]byte("POST /health HTTP/1.1\r\nHost: opencircuitsf.invalid\r\nContent-Length: 100\r\n\r\npartial")); err != nil {
			t.Fatalf("write request with unfinished body: %v", err)
		}
	})
	if err == nil {
		t.Fatal("mountAndServe returned nil after SIGTERM with an active request outliving the budget — " +
			"#0598's all-new check must not swallow a non-new connection")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("mountAndServe error %v does not wrap context.DeadlineExceeded", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"graceful shutdown incomplete",
		declineShutdownBudget.String(),
		"connections still active at expiry",
		"1 connection(s)",
		"127.0.0.1",
		" active",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("decline error %q does not name %q", msg, want)
		}
	}
}

// TestMountAndServe_ShutdownClosesNeverUsedConnections is #0598: a client
// connection that never sent a request is StateNew, which net/http's
// Shutdown holds busy for ~5s (Go issue 22682) — the whole production
// budget. With only such connections left there is nothing to drain, so
// mountAndServe must close them and return nil, not a FAILURE.
//
// The elapsed-time floor proves the nil came from the all-new close path,
// not from Shutdown finishing early because the connection never reached
// the server's tracked set: the helper sleeps 250ms after hold before
// SIGTERM, so a Shutdown that actually waited on the StateNew connection
// returns no sooner than 250ms + declineShutdownBudget after hold began.
// A lower bound only — load can lengthen it, never shorten it.
func TestMountAndServe_ShutdownClosesNeverUsedConnections(t *testing.T) {
	var held time.Time
	err := runShutdownHoldingConn(t, func(t *testing.T, port int) {
		raw, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatalf("dial silent connection: %v", err)
		}
		t.Cleanup(func() { raw.Close() })
		held = time.Now()
	})
	if err != nil {
		t.Fatalf("mountAndServe returned %v for a shutdown holding only never-used connections, want nil", err)
	}
	if floor := 250*time.Millisecond + declineShutdownBudget; time.Since(held) < floor {
		t.Fatalf("mountAndServe returned nil %s after the silent connection was opened, under the %s floor — "+
			"Shutdown did not wait on it, so the all-new close path was not exercised", time.Since(held), floor)
	}
}

// declineShutdownBudget is the shutdown budget both decline tests run under,
// shared so the error-text assertion cannot drift from the override.
const declineShutdownBudget = 500 * time.Millisecond

// runShutdownHoldingConn starts mountAndServe with a declineShutdownBudget
// shutdown budget, lets hold open whatever connection the scenario needs,
// sends SIGTERM and returns mountAndServe's result.
func runShutdownHoldingConn(t *testing.T, hold func(t *testing.T, port int)) error {
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

	origServerTimeout := shutdownServerTimeout
	shutdownServerTimeout = declineShutdownBudget
	t.Cleanup(func() {
		shutdownServerTimeout = origServerTimeout
	})

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, okPinger{},
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			site, requireSession, requireAdmin, nil, ready)
	}()
	waitForHealthy(t, &http.Client{Timeout: wiringHTTPTimeout}, baseURL, errCh, ready)

	hold(t, port)
	// Let the accept loop pick the connection up and enter StateNew before
	// the SIGTERM lands — otherwise Shutdown could close the listener while
	// the connection is still in the kernel backlog, never entered the
	// server's active set, and returned nil for the wrong reason.
	time.Sleep(250 * time.Millisecond)

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	select {
	case err := <-errCh:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("mountAndServe did not return within 10s of SIGTERM")
		return nil
	}
}
