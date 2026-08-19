package handlers

import (
	"net"
	"net/http"
	"strings"
)

// clientIP derives the originating client IP. Behind Apache's reverse proxy the
// real client is in X-Forwarded-For (first hop); otherwise fall back to the
// connection's RemoteAddr with the port stripped.
//
// Ported from the source skeleton's redirect handler (deleted in #0002
// along with the rest of internal/handlers/redirect.go) — this helper is a
// generic request utility used by the auth, credentials, settings, and users
// handlers for audit-log IP attribution, not a redirect-specific concern.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For is a comma-separated list; the first entry is the
		// original client.
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
