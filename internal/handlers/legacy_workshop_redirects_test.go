package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func legacyRedirectMux() *http.ServeMux {
	mux := http.NewServeMux()
	MountLegacyWorkshopRedirects(mux)
	return mux
}

func assertLegacyRedirect(t *testing.T, method, path, wantLocation string) {
	t.Helper()
	rec := httptest.NewRecorder()
	legacyRedirectMux().ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	if rec.Code != http.StatusMovedPermanently {
		t.Errorf("%s %s status = %d, want 301", method, path, rec.Code)
	}
	if got := rec.Header().Get("Location"); got != wantLocation {
		t.Errorf("%s %s Location = %q, want %q", method, path, got, wantLocation)
	}
}

func TestLegacyWorkshopsIndexRedirects(t *testing.T) {
	assertLegacyRedirect(t, http.MethodGet, "/workshops", "/events?type=workshop")
	assertLegacyRedirect(t, http.MethodGet, "/workshops/", "/events?type=workshop")
	assertLegacyRedirect(t, http.MethodGet, "/workshops?utm_source=x", "/events?type=workshop&utm_source=x")
}

func TestLegacyWorkshopDetailRedirects(t *testing.T) {
	assertLegacyRedirect(t, http.MethodGet, "/workshops/solder-101", "/events/solder-101")
	assertLegacyRedirect(t, http.MethodGet, "/workshops/solder-101/", "/events/solder-101")
	assertLegacyRedirect(t, http.MethodGet, "/workshops/solder-101?utm_source=x&utm_medium=y", "/events/solder-101?utm_source=x&utm_medium=y")
	// No store is consulted, so an unknown slug redirects like any other.
	assertLegacyRedirect(t, http.MethodGet, "/workshops/never-existed", "/events/never-existed")
	// An encoded slash stays one segment, so the target stays on this site.
	assertLegacyRedirect(t, http.MethodGet, "/workshops/%2F%2Fevil.com", "/events/%2F%2Fevil.com")
}

func TestLegacyWorkshopRedirectsAnswerHEAD(t *testing.T) {
	assertLegacyRedirect(t, http.MethodHead, "/workshops", "/events?type=workshop")
	assertLegacyRedirect(t, http.MethodHead, "/workshops/solder-101", "/events/solder-101")
}

// The card route is registered beside these, and the more specific pattern
// must win so a published post's preview image is served, not redirected.
func TestLegacyWorkshopRedirectsLeaveOGCardRouteAlone(t *testing.T) {
	mux := legacyRedirectMux()
	mux.HandleFunc("GET /workshops/{slug}/og.png", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/workshops/solder-101/og.png", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (Location %q)", rec.Code, rec.Header().Get("Location"))
	}
}
