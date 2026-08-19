package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// req builds a bare GET request with the given RemoteAddr and, if non-empty,
// X-Forwarded-For header.
func req(remoteAddr, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

// TestClientIP_DelegatesToMiddleware confirms handlers.clientIP is a thin
// wrapper over middleware.ClientIP — #0077's unification of what used to be
// two independently-maintained copies of the same security-relevant
// X-Forwarded-For parse (this package's own leftmost-trusting clientIP, and
// middleware's). Two implementations is how they drift; this proves there is
// now exactly one behavior, exercised through the package other handlers
// (auth, credentials, settings, users, interests, subscribe) actually call.
func TestClientIP_DelegatesToMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{
			name:       "trusted proxy hop, rightmost XFF entry wins",
			remoteAddr: "127.0.0.1:9999",
			xff:        "172.16.0.7, 198.51.100.9",
			want:       "198.51.100.9",
		},
		{
			name:       "no trusted proxy, spoofed XFF ignored",
			remoteAddr: "198.51.100.9:1234",
			xff:        "1.2.3.4",
			want:       "198.51.100.9",
		},
		{
			name:       "no proxy, no header, RemoteAddr port stripped",
			remoteAddr: "203.0.113.42:5000",
			xff:        "",
			want:       "203.0.113.42",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clientIP(req(tt.remoteAddr, tt.xff)); got != tt.want {
				t.Errorf("clientIP() = %q, want %q", got, tt.want)
			}
		})
	}
}
