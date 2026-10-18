package serversettings

import (
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		key, in, want string
		ok            bool
	}{
		{KeyMediaDir, "/var/www/media", "/var/www/media", true},
		{KeyMediaDir, "/var/www/media/" + strings.Repeat(".", 2) + "/media", "/var/www/media", true},
		{KeyMediaDir, "relative/x", "", false},
		{KeyMediaDir, "/", "", false},
		{KeyMediaDir, "/a\x00b", "", false},
		{KeyEmailFrom, "Open Circuit <a@mailing.example.org>", "Open Circuit <a@mailing.example.org>", true},
		{KeyEmailFrom, "a@b.org\r\nBcc: x@y.org", "", false},
		{KeyEmailFrom, "not an address", "", false},
		{KeyEmailReplyTo, "contact@example.org", "contact@example.org", true},
		{KeyEmailReplyTo, "nope", "", false},
		{KeyEmailListDomain, "opencircuitsf.com", "", false},
		{KeyEmailListDomain, "Lists.OpenCircuitSF.com.", "lists.opencircuitsf.com", true},
		{KeyEmailListDomain, "-a.b.example.org", "", false},
		{KeyEmailListDomain, "a_b.example.org", "", false},
		{KeySESSandbox, "TRUE", "true", true},
		{KeySESSandbox, "false", "false", true},
		{KeySESSandbox, "maybe", "", false},
		{KeySESInboundBucket, "opencircuitsf-inbound", "opencircuitsf-inbound", true},
		{KeySESInboundBucket, "ab", "", false},
		{KeySESInboundBucket, "Bad_Bucket", "", false},
		{KeySESInboundBucket, "-bucket", "", false},
		{KeySendBatchSize, "50", "50", true},
		{KeySendBatchSize, "0", "", false},
		{KeySendBatchSize, "501", "", false},
		{KeySendBatchSize, "5x", "", false},
		{"nope", "x", "", false},
	}
	for _, c := range cases {
		got, err := Normalize(c.key, c.in)
		if c.ok != (err == nil) || got != c.want {
			t.Errorf("Normalize(%s, %q) = %q, %v", c.key, c.in, got, err)
			continue
		}
		if err != nil && len(c.in) > 4 && strings.Contains(err.Error(), strings.TrimSpace(c.in)) && c.key != "nope" {
			t.Errorf("error echoes input: %v", err)
		}
	}
}
