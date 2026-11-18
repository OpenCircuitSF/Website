package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVersionHandler_BodyAndNoStore(t *testing.T) {
	rec := httptest.NewRecorder()
	NewVersionHandler("0.16.2", "abc123").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type=%q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control=%q, want no-store", got)
	}
	var body struct{ Version, Commit, Build string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Version != "0.16.2" || body.Commit != "abc123" || body.Build != "0.16.2+abc123" {
		t.Errorf("body=%+v", body)
	}
}
