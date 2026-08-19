package middleware

import (
	"net"
	"net/http"
	"strings"
)

// ClientIP derives the originating client IP for every security-relevant
// decision that needs it: rate-limit bucketing (RateLimiter.Middleware, this
// file) and consent-evidence / audit-log IP attribution
// (internal/handlers.clientIP, which forwards here). #0077 found that this
// project had two independent copies of this parse — one in this package, one
// in internal/handlers — and both trusted the WRONG end of X-Forwarded-For.
// Two implementations of the same security-relevant parse is how they drift;
// this is now the only one.
//
// # The bug this fixes
//
// The old code took the FIRST (leftmost) X-Forwarded-For entry. But
// deploy/apache/opencircuitsf.com.conf proxies with mod_proxy_http, which
// APPENDS the peer it sees to any existing X-Forwarded-For rather than
// replacing it:
//
//	ProxyPass / http://127.0.0.1:8080/
//
// So the leftmost entry of an inbound X-Forwarded-For is whatever the client
// put there — attacker-controlled — and the rightmost entry is the one Apache
// itself appended, i.e. the real peer. Trusting the leftmost entry let a
// client bypass every per-IP rate limiter (including the Phase 1 auth
// limiters on login/registration/recovery, which use this same helper via
// internal/handlers.clientIP) by rotating a header value, and let it choose
// its own signup_ip — the value PRD §6.3 calls consent evidence and
// #0075's published privacy policy tells subscribers is recorded.
//
// # Trust model
//
// deploy/systemd/opencircuit.service documents the actual topology: this
// process "listens on 127.0.0.1:8080 behind the Apache reverse proxy" — i.e.
// it binds loopback only, and the vhost has exactly one ProxyPass with no
// other proxy layer (no CDN, no second hop) in front of it. Two rules follow
// directly from that, and both must hold for X-Forwarded-For to be trusted at
// all:
//
//  1. X-Forwarded-For is honored ONLY when the immediate TCP peer
//     (r.RemoteAddr) is loopback (127.0.0.1 / ::1) — the address Apache (or a
//     local developer/test client) connects from. Any other peer reached this
//     process directly, with no proxy in front, so whatever
//     X-Forwarded-For it supplies is unauthenticated client input and MUST be
//     ignored. This is what makes the "no proxy at all" case (dev mode,
//     direct connections, or a misrouted/direct external connection in
//     production) safe: a spoofed header from a non-loopback peer can never
//     override RemoteAddr.
//  2. When the peer IS loopback, exactly ONE hop is trusted: the rightmost
//     X-Forwarded-For entry, because that is the one entry mod_proxy_http
//     itself appended. Everything left of it is client-supplied. If this
//     service is ever put behind an additional layer (a CDN, a second
//     proxy), this single-hop assumption must be revisited to match the new
//     topology — the vhost is in-repo and the answer is knowable, not
//     guessable (see issues/0077.md Notes).
//
// With no X-Forwarded-For header at all (the ordinary case with no proxy in
// front), ClientIP simply returns RemoteAddr's host, port stripped — matching
// the pre-#0077 fallback behaviour exactly.
func ClientIP(r *http.Request) string {
	peer := remoteAddrHost(r)

	if isTrustedProxyPeer(peer) {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if entry := rightmostXFFEntry(xff); entry != "" {
				if ip := parseXFFIP(entry); ip != "" {
					return ip
				}
			}
		}
	}

	return peer
}

// remoteAddrHost strips the port from r.RemoteAddr, falling back to the raw
// value if it isn't in host:port form.
func remoteAddrHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// isTrustedProxyPeer reports whether peer — the host portion of RemoteAddr —
// is the loopback address the trusted Apache hop always connects from in this
// deployment (opencircuit binds 127.0.0.1:8080; see
// deploy/systemd/opencircuit.service). A non-loopback peer talked to this
// process directly, with no proxy in front, so its X-Forwarded-For (if any)
// is untrusted client input.
func isTrustedProxyPeer(peer string) bool {
	ip := net.ParseIP(peer)
	return ip != nil && ip.IsLoopback()
}

// rightmostXFFEntry returns the last comma-separated entry of an
// X-Forwarded-For header value, trimmed of surrounding whitespace. That is
// the hop most recently appended — the one the trusted proxy itself added —
// under mod_proxy_http's append (not replace) behaviour.
func rightmostXFFEntry(xff string) string {
	parts := strings.Split(xff, ",")
	return strings.TrimSpace(parts[len(parts)-1])
}

// parseXFFIP validates that entry (already trimmed by rightmostXFFEntry) is
// an IP address, returning "" if it cannot be resolved to one at all. #0080:
// the caller previously trusted this entry verbatim with no validation. A
// value that is neither an IP nor a stripped-port host:port pair must never
// reach a caller: signup_ip is an INET column (an unparseable value fails
// the INSERT outright — #0026's handler still returns its uniform 202
// regardless, so the signup silently vanishes with no error surfaced
// anywhere), and this same value keys the rate-limit bucket map
// (RateLimiter.Middleware, ratelimit.go), where an unparseable token would
// otherwise let every malformed value share one bucket instead of being
// rejected.
//
// A bare IP ("203.0.113.5") is trusted as-is. An IP:port pair
// ("203.0.113.5:4444") — not itself a valid inet literal, and not something
// mod_proxy_http produces, but conceivably injected by whatever sits to the
// left of the trusted hop — has its port stripped rather than being stored
// or bucketed verbatim, per the issue's second acceptance criterion.
// Anything else (e.g. "not-an-ip") returns "", and ClientIP falls back to
// the peer address.
func parseXFFIP(entry string) string {
	if net.ParseIP(entry) != nil {
		return entry
	}
	if host, _, err := net.SplitHostPort(entry); err == nil && net.ParseIP(host) != nil {
		return host
	}
	return ""
}
