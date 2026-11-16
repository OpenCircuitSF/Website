// Package apitokens owns the api_tokens table (#0652): admin-created, scoped
// bearer tokens that let a machine such as Home Assistant or an MCP client
// call the /api/v1/ routes. #0674 added an optional expiry and write scopes.
// Only the SHA-256 of a token is ever stored.
package apitokens

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"time"
)

const (
	// TokenPrefix marks every API token, so it can be told apart from a
	// session token (43 characters, no prefix) before any lookup.
	TokenPrefix = "ocsf_"
	// TokenLength is len(TokenPrefix) plus 43 characters of unpadded
	// URL-safe base64 over 32 random bytes.
	TokenLength = 48
	// DisplayPrefixLength is how many leading characters are kept as the
	// non-secret prefix shown in the admin list.
	DisplayPrefixLength = 12
	// LastUsedInterval is the minimum gap between last_used_at writes for
	// one token, so polling never becomes a write per request.
	LastUsedInterval = time.Minute
)

// Scopes (#0674). Each names one resource and one access level; a write
// scope also satisfies the read scope of the same resource (ScopeSatisfies).
const (
	// ScopeEngagementRead lets a token read the engagement endpoint (#0653).
	ScopeEngagementRead = "engagement:read"
	// ScopeGuidesRead lets a token read guides.
	ScopeGuidesRead = "guides:read"
	// ScopeGuidePostsRead lets a token read guide posts.
	ScopeGuidePostsRead = "guide_posts:read"
	// ScopeGuidePostsWrite lets a token create, schedule and change guide posts.
	ScopeGuidePostsWrite = "guide_posts:write"
	// ScopeEventsRead lets a token read events.
	ScopeEventsRead = "events:read"
	// ScopeEventsWrite lets a token create and update events.
	ScopeEventsWrite = "events:write"
	// ScopeVenuesRead lets a token read venues.
	ScopeVenuesRead = "venues:read"
)

// KnownScopes is every scope a token may carry. migrations/000053's
// api_tokens_scopes_check array must hold exactly this set (internal/db's
// TestAPITokenScopesMatchMigration).
var KnownScopes = []string{
	ScopeEngagementRead,
	ScopeGuidesRead,
	ScopeGuidePostsRead,
	ScopeGuidePostsWrite,
	ScopeEventsRead,
	ScopeEventsWrite,
	ScopeVenuesRead,
}

var (
	// ErrNotFound covers an unknown, malformed, revoked or expired token alike.
	ErrNotFound = errors.New("apitokens: token not found")
	// ErrAlreadyRevoked is returned when revoking a token twice.
	ErrAlreadyRevoked = errors.New("apitokens: token already revoked")
)

// Generate returns a new random token: TokenPrefix followed by 32 bytes from
// crypto/rand in unpadded URL-safe base64.
func Generate() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// Hash is the SHA-256 of the whole token, prefix included. The token is
// high-entropy, so a slow KDF is not needed.
func Hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// LooksLikeToken reports whether s has an API token's shape. A session token
// is 43 characters, so this is never true for one.
func LooksLikeToken(s string) bool {
	return strings.HasPrefix(s, TokenPrefix) && len(s) == TokenLength
}

// IsKnownScope reports whether s is in KnownScopes.
func IsKnownScope(s string) bool {
	return slices.Contains(KnownScopes, s)
}

// ScopeSatisfies reports whether a token granted these scopes may use a route
// requiring required. A scope satisfies itself, and "R:write" satisfies
// "R:read" for the same resource R. An empty required scope is satisfied by
// any token.
func ScopeSatisfies(granted []string, required string) bool {
	if required == "" || slices.Contains(granted, required) {
		return true
	}
	resource, ok := strings.CutSuffix(required, ":read")
	return ok && resource != "" && slices.Contains(granted, resource+":write")
}
