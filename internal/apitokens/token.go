// Package apitokens owns the api_tokens table (#0652): admin-created, scoped,
// read-only bearer tokens that let a machine such as Home Assistant read the
// /api/v1/ routes. Only the SHA-256 of a token is ever stored.
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
	// ScopeEngagementRead lets a token read the engagement endpoint (#0653).
	ScopeEngagementRead = "engagement:read"
	// LastUsedInterval is the minimum gap between last_used_at writes for
	// one token, so polling never becomes a write per request.
	LastUsedInterval = time.Minute
)

// KnownScopes is every scope a token may carry; the migration's CHECK must match.
var KnownScopes = []string{ScopeEngagementRead}

var (
	// ErrNotFound covers an unknown, malformed or revoked token alike.
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
