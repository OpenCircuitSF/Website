// Package outreachconn owns the outreach connection credentials (ShortLinks,
// Bluesky, Mastodon): their saved values, their precedence over environment
// variables, and the live clients built from them (#0564).
//
// A value saved here takes precedence over the matching environment variable,
// field by field. Secrets are write-only: no snapshot, log line, or audit
// entry ever carries one.
package outreachconn

import "github.com/brennanMKE/OpenCircuitSF/internal/bluesky"

// Connection groups.
const (
	GroupShortLinks = "shortlinks"
	GroupBluesky    = "bluesky"
	GroupMastodon   = "mastodon"
)

// Field keys, as stored in outreach_connection_values.key.
const (
	KeyShortLinksBaseURL  = "shortlinks_base_url"
	KeyShortLinksAPIToken = "shortlinks_api_token"
	KeyBlueskyHandle      = "bluesky_handle"
	KeyBlueskyAppPassword = "bluesky_app_password"
	KeyBlueskyPDSURL      = "bluesky_pds_url"
	KeyMastodonBaseURL    = "mastodon_base_url"
	KeyMastodonToken      = "mastodon_token"
)

// Field describes one configurable value.
type Field struct {
	Key, Group, EnvVar string
	Secret             bool
	Default            string
}

// Fields is in display order; the migration's CHECK list must match it.
var Fields = []Field{
	{Key: KeyShortLinksBaseURL, Group: GroupShortLinks, EnvVar: "SHORTLINKS_BASE_URL"},
	{Key: KeyShortLinksAPIToken, Group: GroupShortLinks, EnvVar: "SHORTLINKS_API_TOKEN", Secret: true},
	{Key: KeyBlueskyHandle, Group: GroupBluesky, EnvVar: "BLUESKY_HANDLE"},
	{Key: KeyBlueskyAppPassword, Group: GroupBluesky, EnvVar: "BLUESKY_APP_PASSWORD", Secret: true},
	{Key: KeyBlueskyPDSURL, Group: GroupBluesky, EnvVar: "BLUESKY_PDS_URL", Default: bluesky.DefaultPDSURL},
	{Key: KeyMastodonBaseURL, Group: GroupMastodon, EnvVar: "MASTODON_BASE_URL"},
	{Key: KeyMastodonToken, Group: GroupMastodon, EnvVar: "MASTODON_TOKEN", Secret: true},
}

// Groups lists the group names in display order.
var Groups = []string{GroupShortLinks, GroupBluesky, GroupMastodon}

// FieldByKey returns the field with the given key.
func FieldByKey(key string) (Field, bool) {
	for _, f := range Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}
