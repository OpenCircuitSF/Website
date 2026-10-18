// Package serversettings owns the server settings saved from admin Settings:
// their saved values, their precedence over environment variables, and the
// live getters the rest of the service reads them through (#0566).
//
// A value saved here takes precedence over the matching environment variable,
// field by field. Nothing in this package is a secret.
package serversettings

// Setting groups.
const (
	GroupMedia   = "media"
	GroupEmail   = "email"
	GroupSES     = "ses"
	GroupSending = "sending"
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
)

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
}

// Groups lists the group names in display order.
var Groups = []string{GroupMedia, GroupEmail, GroupSES, GroupSending}

// FieldByKey returns the field with the given key.
func FieldByKey(key string) (Field, bool) {
	for _, f := range Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}
