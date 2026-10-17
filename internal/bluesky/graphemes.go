package bluesky

import (
	"strings"

	"github.com/rivo/uniseg"
)

const (
	// MaxGraphemes is Bluesky's post length limit, in UAX #29 grapheme
	// clusters. The server rejects anything longer, so this check must count
	// the way the server does.
	MaxGraphemes = 300
	// MaxTextBytes is the server's separate byte cap on post text.
	MaxTextBytes = 3000
)

// GraphemeCount counts extended grapheme clusters, so a precomposed e-acute,
// an e plus a combining accent, a ZWJ family emoji, a flag and a skin-toned
// thumb each count once.
func GraphemeCount(s string) int { return uniseg.GraphemeClusterCount(s) }

// ValidateText rejects empty text and text over either limit with a
// permanent *APIError.
func ValidateText(s string) error {
	if strings.TrimSpace(s) == "" {
		return &APIError{Message: "post text is empty", Permanent: true, kind: ErrInvalid}
	}
	if n := GraphemeCount(s); n > MaxGraphemes {
		return &APIError{Message: "post text is over the limit", Permanent: true, kind: ErrTextTooLong}
	}
	if len(s) > MaxTextBytes {
		return &APIError{Message: "post text is over the byte limit", Permanent: true, kind: ErrTextTooLong}
	}
	return nil
}
