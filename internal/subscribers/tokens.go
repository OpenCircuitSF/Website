package subscribers

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// tokenBytes is the number of random bytes behind confirm_token and
// manage_token, per PRD §6.4: "32-byte random value, not an HMAC of the
// email." Random tokens are revocable by rotating the column and leak
// nothing if a URL lands in a referrer header or a screenshot — an HMAC of
// the email would leak the email format and could not be individually
// revoked without changing the HMAC key for every subscriber.
const tokenBytes = 32

// newToken returns a URL-safe, unpadded base64 token over 32 random bytes
// from crypto/rand. Used for both confirm_token and manage_token so neither
// needs escaping when embedded in a confirmation or preference-center link.
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("subscribers: reading random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
