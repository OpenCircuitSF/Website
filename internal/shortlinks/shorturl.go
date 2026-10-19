package shortlinks

import (
	"net/url"
	"strings"
)

// ShortURL builds the shareable short URL for key under base. ShortLinks
// serves redirects only at GET /u/{key}; any other path falls through to its
// SPA (HTTP 200), so a URL built without the /u/ segment is a dead link
// (#0603). It matches ShortLinks' own qr.ShortURL exactly: a trailing slash
// on base is trimmed, and the key is percent-escaped as one path segment.
func ShortURL(base, key string) string {
	return strings.TrimRight(base, "/") + "/u/" + url.PathEscape(key)
}
