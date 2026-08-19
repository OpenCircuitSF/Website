package handlers

import (
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// clientIP derives the originating client IP. It is a thin wrapper over
// middleware.ClientIP — the single security-relevant X-Forwarded-For parse
// shared by this package (audit-log IP attribution; signup_ip consent
// evidence, PRD §6.3) and internal/middleware (per-IP rate limiting).
//
// #0077 found that this package used to carry its own copy of this parse,
// independently from internal/middleware's, and BOTH trusted the leftmost —
// client-supplied — X-Forwarded-For entry instead of the rightmost one
// deploy/apache/opencircuitsf.com.conf's mod_proxy_http actually appends.
// Two implementations of the same security-relevant parse is how they
// drift; unifying on middleware.ClientIP means the auth limiters, the
// audit trail, and signup_ip consent evidence all derive the client the
// same way and can only ever drift together. See middleware.ClientIP's doc
// comment (internal/middleware/clientip.go) for the trust model.
//
// Ported from the source skeleton's redirect handler (deleted in #0002
// along with the rest of internal/handlers/redirect.go) — this helper is a
// generic request utility used by the auth, credentials, settings, users,
// interests, and subscribe handlers, not a redirect-specific concern.
func clientIP(r *http.Request) string {
	return middleware.ClientIP(r)
}
