package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMountServesOnlyAdminPath (#0728): Mount answers on /admin/mcp for
// every Streamable HTTP method and never on /mcp, which #0729's public
// server will take. Beside an SPA-style "GET /", /mcp gets the mux's 405
// for POST and DELETE and the SPA's 404 for GET.
func TestMountServesOnlyAdminPath(t *testing.T) {
	if Path != "/admin/mcp" {
		t.Fatalf("Path = %q, want /admin/mcp", Path)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	Mount(mux, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(299) }))

	for _, c := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/admin/mcp", 299},
		{http.MethodGet, "/admin/mcp", 299},
		{http.MethodDelete, "/admin/mcp", 299},
		{http.MethodPost, "/mcp", http.StatusMethodNotAllowed},
		{http.MethodGet, "/mcp", http.StatusNotFound},
		{http.MethodDelete, "/mcp", http.StatusMethodNotAllowed},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
	for _, p := range []string{"/admin/mcp/", "/admin/mcp/x"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, p, nil))
		if rec.Code == 299 {
			t.Errorf("POST %s reached the MCP handler", p)
		}
	}
}
