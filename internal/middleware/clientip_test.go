package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// req builds a bare GET request with the given RemoteAddr and, if non-empty,
// X-Forwarded-For header — the minimal input ClientIP needs.
func req(remoteAddr, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

// TestClientIP_TrustedProxySingleEntry confirms the ordinary production case:
// peer is loopback (Apache), X-Forwarded-For has one entry (a direct client
// with no other proxy in front of Apache) — that entry is trusted.
func TestClientIP_TrustedProxySingleEntry(t *testing.T) {
	got := ClientIP(req("127.0.0.1:9999", "203.0.113.9"))
	if want := "203.0.113.9"; got != want {
		t.Errorf("ClientIP = %q, want %q", got, want)
	}
}

// TestClientIP_TrustedProxyMultiEntryUsesRightmost is #0077's core fix,
// asserted directly against ClientIP rather than through the rate limiter:
// mod_proxy_http APPENDS the real peer, so with a peer of loopback (Apache)
// and a multi-entry X-Forwarded-For, the RIGHTMOST entry — the one Apache
// appended — must be trusted, not the leftmost, client-supplied one.
func TestClientIP_TrustedProxyMultiEntryUsesRightmost(t *testing.T) {
	got := ClientIP(req("127.0.0.1:9999", "172.16.0.7, 198.51.100.9"))
	if want := "198.51.100.9"; got != want {
		t.Errorf("ClientIP = %q, want %q (rightmost/appended entry) — got the attacker-supplied leftmost entry instead", got, want)
	}
}

// TestClientIP_TrustedProxyMultiEntryTrimsWhitespace confirms the
// comma-separated parse tolerates the "a, b, c" spacing convention real
// proxies (and mod_proxy_http) actually use.
func TestClientIP_TrustedProxyMultiEntryTrimsWhitespace(t *testing.T) {
	got := ClientIP(req("127.0.0.1:9999", "  10.0.0.1  ,   198.51.100.9  "))
	if want := "198.51.100.9"; got != want {
		t.Errorf("ClientIP = %q, want %q", got, want)
	}
}

// TestClientIP_IPv6LoopbackPeerIsTrusted confirms the trusted-proxy check
// isn't IPv4-only: ::1 is loopback too.
func TestClientIP_IPv6LoopbackPeerIsTrusted(t *testing.T) {
	got := ClientIP(req("[::1]:9999", "203.0.113.9"))
	if want := "203.0.113.9"; got != want {
		t.Errorf("ClientIP = %q, want %q", got, want)
	}
}

// TestClientIP_UntrustedPeerIgnoresXFF proves a spoofed X-Forwarded-For
// cannot determine ClientIP's answer when the immediate peer is not the
// trusted loopback hop — the "no proxy in front" case (dev mode, a direct
// connection, or any client that isn't Apache). The real RemoteAddr must win
// regardless of what the header claims.
func TestClientIP_UntrustedPeerIgnoresXFF(t *testing.T) {
	got := ClientIP(req("198.51.100.9:1234", "1.2.3.4"))
	if want := "198.51.100.9"; got != want {
		t.Errorf("ClientIP = %q, want %q (real RemoteAddr) — a spoofed X-Forwarded-For must not override it with no trusted proxy in front", got, want)
	}
}

// TestClientIP_UntrustedPeerMultiEntrySpoofIgnored is the same proof with a
// multi-hop spoofed header, confirming the untrusted-peer branch doesn't
// accidentally still parse and trust the rightmost entry.
func TestClientIP_UntrustedPeerMultiEntrySpoofIgnored(t *testing.T) {
	got := ClientIP(req("198.51.100.9:1234", "1.2.3.4, 5.6.7.8, 9.9.9.9"))
	if want := "198.51.100.9"; got != want {
		t.Errorf("ClientIP = %q, want %q (real RemoteAddr)", got, want)
	}
}

// TestClientIP_NoProxyNoHeader is the plain "no proxy at all" case: dev mode
// or a direct connection with no X-Forwarded-For header present at all.
// ClientIP must simply return the RemoteAddr host, port stripped.
func TestClientIP_NoProxyNoHeader(t *testing.T) {
	got := ClientIP(req("203.0.113.42:5000", ""))
	if want := "203.0.113.42"; got != want {
		t.Errorf("ClientIP = %q, want %q", got, want)
	}
}

// TestClientIP_TrustedProxyNoHeader confirms the loopback-peer branch also
// falls back cleanly when Apache forwards a request with no
// X-Forwarded-For at all (shouldn't happen in practice, but must not panic
// or return garbage).
func TestClientIP_TrustedProxyNoHeader(t *testing.T) {
	got := ClientIP(req("127.0.0.1:8080", ""))
	if want := "127.0.0.1"; got != want {
		t.Errorf("ClientIP = %q, want %q", got, want)
	}
}

// TestClientIP_MalformedRemoteAddrFallsBackToRawValue confirms the
// port-stripping fallback matches the pre-#0077 behavior when RemoteAddr
// isn't in host:port form.
func TestClientIP_MalformedRemoteAddrFallsBackToRawValue(t *testing.T) {
	got := ClientIP(req("not-a-host-port", "1.2.3.4"))
	if want := "not-a-host-port"; got != want {
		t.Errorf("ClientIP = %q, want %q", got, want)
	}
}

// TestClientIP_EmptyXFFEntryFallsBackToPeer guards against a pathological
// "X-Forwarded-For: " (present but empty after trimming, e.g. a trailing
// comma) header producing an empty client IP.
func TestClientIP_EmptyXFFEntryFallsBackToPeer(t *testing.T) {
	got := ClientIP(req("127.0.0.1:9999", "203.0.113.9, "))
	if want := "127.0.0.1"; got != want {
		t.Errorf("ClientIP = %q, want %q (fallback to peer when the trusted rightmost entry is empty)", got, want)
	}
}
