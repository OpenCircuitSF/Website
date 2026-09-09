package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
)

// devSessionCreator is the subset of devstore.Store that the dev auth
// middleware needs. Using an interface keeps the middleware package free of a
// direct import of devstore (which itself imports many other internal packages).
type devSessionCreator interface {
	CreateDevSession(userID int64, ttl time.Duration) (token string, expiresAt time.Time, err error)
}

// devAuthOnce gates the one-time startup log so "dev auth active" is printed
// exactly once no matter how many requests arrive.
var devAuthOnce sync.Once

// devAuthSessionTTL is the lifetime of a dev session token. 24 hours is long
// enough for a local dev session without needing any renewal logic.
const devAuthSessionTTL = 24 * time.Hour

// devAuthAdminID is the fixed user id of the seeded mock admin in the dev
// store (mirrors devstore.seedAdminID).
const devAuthAdminID int64 = 1

// DevAutoLogin returns middleware that, in dev mode only, automatically
// establishes an authenticated session for the seeded mock admin user when a
// request arrives without a valid session cookie. The session token is written
// to the response (so the browser stores it) and injected into the request (so
// RequireSession can validate it on the same request).
//
// Hard guardrail: the returned middleware panics at construction time if
// devMode is false, so it is structurally impossible to wire this into the
// production (Postgres) path even by accident.
//
// This must be wired only inside serveDevMode (cmd/opencircuit/main.go), which
// itself is only reached when STORAGE=json is set.
func DevAutoLogin(creator devSessionCreator, devMode bool) func(http.Handler) http.Handler {
	if !devMode {
		// Fail loudly at startup rather than silently. If this ever fires it
		// means the caller wired dev middleware on the production path — a bug.
		panic("middleware.DevAutoLogin: must not be called outside dev mode (STORAGE=json)")
	}

	devAuthOnce.Do(func() {
		slog.Warn("DEV MODE: auto-login is active — all requests are authenticated as the mock admin; NEVER run this in production")
	})

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// If the request already carries a session cookie, let RequireSession
			// validate it normally — no new token needed.
			if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
				next.ServeHTTP(w, r)
				return
			}

			// No session cookie present: mint a dev session for the mock admin.
			token, expiresAt, err := creator.CreateDevSession(devAuthAdminID, devAuthSessionTTL)
			if err != nil {
				slog.Error("devauth: failed to create dev session", "error", err)
				http.Error(w, "dev auth: session creation failed", http.StatusInternalServerError)
				return
			}

			// Write the cookie to the response so the browser stores it for
			// subsequent requests.
			auth.SetSessionCookie(w, token, expiresAt)

			// Inject the cookie into the current request so RequireSession (which
			// reads r.Cookie) can validate the token on this very request without
			// needing a client round-trip.
			r.AddCookie(&http.Cookie{
				Name:  auth.SessionCookieName,
				Value: token,
			})

			next.ServeHTTP(w, r)
		})
	}
}

// devAdminAuthOnce gates DevAdminAutoLogin's one-time startup warning. It is
// deliberately a SEPARATE sync.Once from devAuthOnce above: DevAutoLogin
// (STORAGE=json) and DevAdminAutoLogin (Postgres, #0402) are mutually
// exclusive in practice — one requires devMode true, the other requires it
// false — but sharing a single Once between them would still swallow one of
// the two warnings in a hypothetical process that constructed both, and a
// warning silently eaten by the wrong feature's flag is exactly the kind of
// bug this file's guards exist to prevent.
var devAdminAuthOnce sync.Once

// DevAdminSessionMinter mints a fresh, real session for the dev-admin
// bypass and returns its token, expiry, and any construction error.
// DevAdminAutoLogin's production wiring (cmd/opencircuit's
// newDevAdminAutoLogin) builds this as a closure over auth.NewSessionToken
// and (*auth.Store).CreateSession rather than as a new exported method on
// auth.Store — see that function's doc comment for why the "mint a session
// for an arbitrary user id with no authentication" capability stays
// confined to one function in main.go instead of becoming permanent surface
// on the store.
type DevAdminSessionMinter func(ctx context.Context) (token string, expiresAt time.Time, err error)

// DevAdminAutoLogin returns middleware that, on the Postgres serve path
// only and only when explicitly enabled, automatically authenticates every
// request as the configured admin user — the STORAGE=postgres counterpart
// to DevAutoLogin above, closing the gap #0402 describes: STORAGE=json has
// no mailing-list tables at all (CLAUDE.md §5), so exercising that
// subsystem locally requires the Postgres path, which before this had no
// passkey-free way in.
//
// It is a SIBLING of DevAutoLogin, not a parameter added to it, for three
// reasons: its guard keys on BASE_URL rather than on STORAGE=json; its data
// seam is Postgres session minting plus resolution, not
// devSessionCreator.CreateDevSession; and one function carrying two
// mutually exclusive guards would be weaker than two functions each
// carrying one. DevAutoLogin itself is unchanged by this addition.
//
// The construction invariant, and what makes #0409 unreachable here rather
// than merely fixed: after this middleware runs, the token RequireSession
// will extract from the request is exactly the token whose validity this
// middleware just established. There is no branch keyed on a credential
// merely being PRESENT — only one keyed on it being VALID. Request
// handling, in order:
//
//  1. Extract the token with the SAME unexported sessionToken helper
//     RequireSession uses (auth.go), so header-vs-cookie precedence can
//     never diverge between the two.
//  2. If that token resolves to a live session (resolver.ResolveSession
//     returns no error), pass the request through completely unchanged: no
//     mint, no Set-Cookie, no new sessions row.
//  3. Otherwise — no token, or an unusable one (absent, stale, garbage,
//     expired, revoked) — obtain a token from the cache-or-mint helper
//     below, set it as the response cookie, and REPLACE the request's own
//     Cookie header with that token as its only opencircuit_session entry,
//     and drop any Authorization header the request carried.
//
// Both rewrites in step 3 are load-bearing, not defensive extras:
// (*http.Request).AddCookie APPENDS to the Cookie header, and
// (*http.Request).Cookie (via sessionToken) returns the FIRST match — so a
// stale cookie left in place ahead of a newly-appended one would still win
// at RequireSession's extraction even after a successful mint. And
// sessionToken (auth.go) prefers "Authorization: Bearer" over the cookie,
// so an unusable bearer token left in place would reproduce the identical
// stuck-401 by a second route sessionToken can reach. Rebuilding the Cookie
// header from scratch, and deleting Authorization outright, means neither
// stale credential can survive into RequireSession — a property of the
// control flow here, not a case that has to be remembered at each call site.
//
// Sign-out is a no-op while this mode is active, not a way to leave it:
// POST /auth/logout deletes the sessions row and clears the cookie, and the
// very next request mints a fresh session under exactly the invariant
// above — the mode's contract is "you are the admin", which is the opposite
// failure from #0409's stuck 401.
//
// Hard guardrails, mirroring DevAutoLogin's: this panics at construction
// time if allowed is false, or if resolver or mint is nil. This is a
// structural backstop against a future miswiring, not the load-bearing
// refusal — that is cmd/opencircuit's newDevAdminAutoLogin's startup error,
// which runs, and can fail the whole process, before this constructor is
// ever reached.
//
// Wired ONLY from servePostgres (cmd/opencircuit/main.go), behind
// newDevAdminAutoLogin's BASE_URL-is-loopback check.
func DevAdminAutoLogin(resolver SessionResolver, mint DevAdminSessionMinter, allowed bool) func(http.Handler) http.Handler {
	if !allowed {
		panic("middleware.DevAdminAutoLogin: must not be called unless explicitly allowed (DEV_ADMIN_LOGIN=true on a loopback BASE_URL)")
	}
	if resolver == nil || mint == nil {
		panic("middleware.DevAdminAutoLogin: resolver and mint must not be nil")
	}

	devAdminAuthOnce.Do(func() {
		slog.Warn("DEV MODE: DEV_ADMIN_LOGIN is active — all requests are authenticated as the configured admin; NEVER run this in production (refused outside localhost/127.0.0.1)")
	})

	// mu guards cached/cachedExpiry together with the decision to reuse vs.
	// mint, held across the WHOLE obtain operation below (not just the map
	// write) — see obtain's own comment for why.
	var mu sync.Mutex
	var cached string
	var cachedExpiry time.Time

	// obtain returns a live session token, reusing the cached one when it is
	// still valid and minting a fresh one otherwise. The mutex is held
	// across validation AND minting, not released in between, so that "a
	// first page load's twenty parallel asset requests create exactly one
	// sessions row" is a guarantee rather than a race — this is a dev-only
	// path, so the serialisation costs nothing that matters. Validating the
	// cached token before reuse (rather than trusting it blindly) is what
	// lets a cached token whose row was deleted out of band — a
	// db-reset.sh, a TRUNCATE, a logout — heal on the very next request
	// instead of being handed out dead forever.
	obtain := func(ctx context.Context) (string, time.Time, error) {
		mu.Lock()
		defer mu.Unlock()
		if cached != "" {
			if _, err := resolver.ResolveSession(ctx, cached, time.Now()); err == nil {
				return cached, cachedExpiry, nil
			}
		}
		tok, expiresAt, err := mint(ctx)
		if err != nil {
			return "", time.Time{}, err
		}
		cached, cachedExpiry = tok, expiresAt
		return tok, expiresAt, nil
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tok := sessionToken(r); tok != "" {
				if _, err := resolver.ResolveSession(r.Context(), tok, time.Now()); err == nil {
					// Valid credential: pass through untouched. No mint, no
					// Set-Cookie, no new row — this is the ONLY pass-through
					// branch, and it is keyed on validity, never presence.
					next.ServeHTTP(w, r)
					return
				}
			}

			token, expiresAt, err := obtain(r.Context())
			if err != nil {
				slog.Error("devadminauth: failed to mint dev admin session", "error", err)
				http.Error(w, "dev admin auth: session creation failed", http.StatusInternalServerError)
				return
			}

			auth.SetSessionCookie(w, token, expiresAt)

			// Rebuild the Cookie header from scratch so the new token is the
			// request's ONLY opencircuit_session entry — never
			// r.AddCookie, which appends and would leave a stale value
			// first in the header, exactly where sessionToken's r.Cookie
			// call finds it (see the doc comment above).
			var kept []string
			for _, c := range r.Cookies() {
				if c.Name == auth.SessionCookieName {
					continue
				}
				kept = append(kept, c.Name+"="+c.Value)
			}
			kept = append(kept, auth.SessionCookieName+"="+token)
			r.Header.Set("Cookie", strings.Join(kept, "; "))
			// sessionToken prefers Authorization: Bearer over the cookie; an
			// unusable bearer token reaching this branch has just been
			// judged invalid, so it must not survive into RequireSession by
			// a second route.
			r.Header.Del("Authorization")

			next.ServeHTTP(w, r)
		})
	}
}
