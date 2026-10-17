package outreach

import (
	"errors"
	"fmt"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/mastodon"
)

// Length counts a body the way the channel does: UAX #29 grapheme clusters
// for Bluesky (and the manual channels), code points with URLs weighted at 23
// for Mastodon. The Go count is the authority; the SPA's live counter only
// mirrors it.
func Length(ch Channel, body string) int {
	if ch.Name == "mastodon" {
		return mastodon.CountCharacters(body)
	}
	return bluesky.GraphemeCount(body)
}

// ValidateForSend reports why a body cannot be posted to ch: empty, a link
// placeholder still in it, or over the channel's limit (Bluesky also has a
// byte cap). The message names the count and the limit so it can be shown to
// an admin as is.
func ValidateForSend(ch Channel, body string) error {
	if strings.TrimSpace(body) == "" {
		return errors.New("the post is empty")
	}
	if strings.Contains(body, LinkToken) {
		return errors.New("the post still contains the link placeholder; prepare it first")
	}
	if ch.CharLimit > 0 {
		if n := Length(ch, body); n > ch.CharLimit {
			return fmt.Errorf("the post is %d characters, over the %s limit of %d", n, ch.Label, ch.CharLimit)
		}
	}
	if ch.Name == "bluesky" && len(body) > bluesky.MaxTextBytes {
		return fmt.Errorf("the post is %d bytes, over the %s limit of %d", len(body), ch.Label, bluesky.MaxTextBytes)
	}
	return nil
}
