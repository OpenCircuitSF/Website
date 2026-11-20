package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentsRedirectsToDocsMCP(t *testing.T) {
	mux := http.NewServeMux()
	MountDocsRedirects(mux)
	for path, want := range map[string]string{
		"/agents":     "/docs/mcp",
		"/agents/":    "/docs/mcp",
		"/agents?x=1": "/docs/mcp?x=1",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != want {
			t.Errorf("GET %s = %d Location %q, want 301 %q", path, rec.Code, rec.Header().Get("Location"), want)
		}
	}
}
