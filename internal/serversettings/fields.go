// Package serversettings owns the server settings saved from admin Settings:
// their saved values, their precedence over environment variables, and the
// live getters the rest of the service reads them through (#0566).
//
// A value saved here takes precedence over the matching environment variable,
// field by field. Nothing in this package is a secret.
package serversettings

// Setting groups.
const (
	GroupMedia    = "media"
	GroupEmail    = "email"
	GroupSES      = "ses"
	GroupSending  = "sending"
	GroupOutreach = "outreach"
)

// Field keys, as stored in server_setting_values.key.
const (
	KeyMediaDir         = "media_dir"
	KeyEmailFrom        = "email_from"
	KeyEmailReplyTo     = "email_reply_to"
	KeyEmailListDomain  = "email_list_domain"
	KeySESSandbox       = "ses_sandbox"
	KeySESInboundBucket = "ses_inbound_bucket"
	KeySendBatchSize    = "send_batch_size"
	KeyBlueskyCardURL   = "bluesky_card_url"
	KeyOutreachHashtags = "outreach_hashtags"
	KeyOutreachPostHour = "outreach_post_hour"
	KeyOutreachChannels = "outreach_channels"
)

// Outreach settings (#0571, #0573, #0574) and their defaults.
const (
	// CardURLShort points the Bluesky card at the tracked short link;
	// CardURLCanonical at the workshop's own URL with UTM.
	CardURLShort     = "short"
	CardURLCanonical = "canonical"

	DefaultOutreachHashtags = "#SanFrancisco #Electronics"
	DefaultOutreachPostHour = "10"
	// DefaultOutreachChannels is the set of channels outreach drafts, counts
	// and shows until Settings says otherwise (#0588): the two the site posts
	// itself and Instagram. TikTok and YouTube need a video, so they start off.
	DefaultOutreachChannels = "bluesky,mastodon,instagram"
	// MaxOutreachHashtags bounds the site-wide set.
	MaxOutreachHashtags = 6
	// MaxHashtagRunes bounds one tag after its "#"; Bluesky's tag facet
	// allows 64 graphemes, and a tag this long is unreadable anyway.
	MaxHashtagRunes = 40
)

// OutreachChannelNames lists every channel outreach knows, in display order.
// It mirrors internal/outreach's channel list (a test in that package keeps
// the two equal); it is spelled out here because this package must not import
// outreach.
var OutreachChannelNames = []string{"bluesky", "mastodon", "instagram", "tiktok", "youtube"}

// DefaultMediaDir is the production media directory.
const DefaultMediaDir = "/var/www/media"

// Field describes one configurable value. RestartRequired means the service
// reads it once at boot.
type Field struct {
	Key, Group, EnvVar string
	Default            string
	RestartRequired    bool
}

// Fields is in display order; the migration's CHECK list must match it.
var Fields = []Field{
	{Key: KeyMediaDir, Group: GroupMedia, EnvVar: "MEDIA_DIR", Default: DefaultMediaDir},
	{Key: KeyEmailFrom, Group: GroupEmail, EnvVar: "EMAIL_FROM", RestartRequired: true},
	{Key: KeyEmailReplyTo, Group: GroupEmail, EnvVar: "EMAIL_REPLY_TO", RestartRequired: true},
	{Key: KeyEmailListDomain, Group: GroupEmail, EnvVar: "EMAIL_LIST_DOMAIN", RestartRequired: true},
	{Key: KeySESSandbox, Group: GroupSES, EnvVar: "SES_SANDBOX", Default: "true"},
	{Key: KeySESInboundBucket, Group: GroupSES, EnvVar: "SES_INBOUND_BUCKET"},
	{Key: KeySendBatchSize, Group: GroupSending, EnvVar: "SEND_BATCH_SIZE", Default: "50", RestartRequired: true},
	{Key: KeyBlueskyCardURL, Group: GroupOutreach, Default: CardURLShort},
	{Key: KeyOutreachHashtags, Group: GroupOutreach, Default: DefaultOutreachHashtags},
	{Key: KeyOutreachPostHour, Group: GroupOutreach, Default: DefaultOutreachPostHour},
	{Key: KeyOutreachChannels, Group: GroupOutreach, Default: DefaultOutreachChannels},
}

// Groups lists the group names in display order.
var Groups = []string{GroupMedia, GroupEmail, GroupSES, GroupSending, GroupOutreach}

// FieldByKey returns the field with the given key.
func FieldByKey(key string) (Field, bool) {
	for _, f := range Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}
