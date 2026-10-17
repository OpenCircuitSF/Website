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
// budget test above covers: it opens a raw TCP connection and sends a
// request header that never terminates, which parks the server-side
// connection in StateNew. That state is invisible to every handler and to
// any request-count diagnostic — nothing is "in flight" from the handler's
// point of view — yet it holds Shutdown open exactly as an in-flight
// handler does: net/http's quiescence check treats StateNew as idle only
// after ~5s of unread request header, which collides with the 5s
// production budget. It is the clearest demonstration that the deadline
// error was never proof of a hang OR of benign timing: without naming the
// connections, the bare deadline could not distinguish its own causes.
//
// The test shrinks the production budget to 500ms (package var, the same
// override the budget test uses) so the timed-out shutdown is
// deterministic: a fresh StateNew connection is not treated as idle until
// ~5s of unread header has elapsed, so a 500ms budget can never see this
// connection go idle first.
//
// Handler arguments are typed nils except where a route this test actually
// drives needs a real instance — the same shape noindex_wiring_test.go
// uses with okPinger, since the only route it exercises is GET /health
// plus one raw connection that never completes a request.
func TestMountAndServe_ShutdownTimeoutNamesWhatHeldItOpen(t *testing.T) {
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
	const testShutdownServerTimeout = 500 * time.Millisecond
	shutdownServerTimeout = testShutdownServerTimeout
	t.Cleanup(func() {
		shutdownServerTimeout = origServerTimeout
	})

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		errCh <- mountAndServe(cfg, okPinger{},
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			site, requireSession, requireAdmin, nil, ready)
	}()
	waitForHealthy(t, &http.Client{Timeout: wiringHTTPTimeout}, baseURL, errCh, ready)

	// The half-open connection: connect, send a header with no terminating
	// blank line, and never finish the request. The server accepts the
	// connection, reads the partial header, and parks the connection in
	// StateNew for as long as the client stays silent. Closed in cleanup so
	// the server-side read loop terminates when this test is done —
	// mountAndServe's shutdown does not close this connection (that is the
	// behavior under test), so the client side is what releases it.
	raw, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("dial raw connection: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	if _, err := raw.Write([]byte("GET / HTTP/1.1\r\nHost: opencircuitsf.invalid")); err != nil {
		t.Fatalf("write partial header: %v", err)
	}
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
		if err == nil {
			t.Fatal("mountAndServe returned nil after SIGTERM with a half-open connection — " +
				"the shutdown completed instead of hitting its budget; the decline path was not exercised")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("mountAndServe error %v does not wrap context.DeadlineExceeded — the named decline "+
				"error must preserve the deadline identity for callers that match on it", err)
		}
		msg := err.Error()
		for _, want := range []string{
			"graceful shutdown incomplete",
			testShutdownServerTimeout.String(),
			"connections still active at expiry",
			"1 connection(s)",
			"127.0.0.1",
			" new", // http.StateNew.String() — names the half-open conn's state
		} {
			if !strings.Contains(msg, want) {
				t.Errorf("decline error %q does not name %q — a bare deadline with no statement of what "+
					"was still active is exactly the silent-decline shape #0536 closed", msg, want)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("mountAndServe did not return within 10s of SIGTERM")
	}

	// The half-open connection held the shutdown open but must not have
	// wedged anything: the returned error names it, and cleanup closes it,
	// which releases the server-side read loop. Nothing here outlives this
	// test except that one goroutine's read error path, which the client
	// close in cleanup terminates.
}
