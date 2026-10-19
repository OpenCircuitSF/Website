package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestNewShutdownWiringClient_NeverLeavesASpareConnection pins the property
// newShutdownWiringClient exists for (#0596): every connection it dials
// carries a request, so none is left in http.StateNew to hold a later
// srv.Shutdown open for ~5s.
//
// The oracle is a count the server observes, not the client's
// configuration: each connection's first StateActive is recorded, and the
// test asserts the number of connections equals the number of requests and
// that no connection closed without ever going active. A keep-alive client
// reuses connections, so it fails the first check on every run rather than
// only when the spare-dial race happens to fire; the second check is the
// spare connection itself. No database, no listener of mountAndServe's,
// and no deadline beyond the client's own timeout.
func TestNewShutdownWiringClient_NeverLeavesASpareConnection(t *testing.T) {
	var (
		mu          sync.Mutex
		conns       int
		requests    int
		everActive  = map[string]bool{}
		neverActive int
	)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		_, _ = w.Write([]byte("ok"))
	}))
	srv.Config.ConnState = func(c net.Conn, s http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		addr := c.RemoteAddr().String()
		switch s {
		case http.StateNew:
			conns++
		case http.StateActive:
			everActive[addr] = true
		case http.StateClosed, http.StateHijacked:
			if !everActive[addr] {
				neverActive++
			}
		}
	}
	srv.Start()

	client := newShutdownWiringClient()
	// The same shape as the SIGTERM test: a GET whose body is closed, then
	// immediately a POST — the sequence that let http.DefaultTransport dial
	// a spare connection.
	const sequences = 20
	for i := 0; i < sequences; i++ {
		resp, err := client.Get(srv.URL + "/health")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		resp.Body.Close()
		resp, err = client.Post(srv.URL+"/api/subscribe", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		resp.Body.Close()
	}
	// Close every connection still open so each one's final state has been
	// reported before counting.
	srv.Close()

	mu.Lock()
	defer mu.Unlock()
	if requests != 2*sequences {
		t.Fatalf("server saw %d requests, want %d", requests, 2*sequences)
	}
	if conns != requests {
		t.Errorf("client opened %d connections for %d requests, want one each — a client that reuses or "+
			"pre-dials connections can leave one in StateNew, which holds srv.Shutdown open for ~5s", conns, requests)
	}
	if neverActive != 0 {
		t.Errorf("%d connection(s) closed without ever carrying a request — a spare dial, the exact "+
			"connection that failed TestMountAndServe_SIGTERMReleasesInFlightClaim", neverActive)
	}
}
