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

func TestNormalizeOutreachFields(t *testing.T) {
	ok := map[[2]string]string{
		{KeyBlueskyCardURL, " Canonical "}:                                  "canonical",
		{KeyOutreachPostHour, "0"}:                                          "0",
		{KeyOutreachPostHour, "23"}:                                         "23",
		{KeyOutreachHashtags, "#SanFrancisco   #Electronics #sanfrancisco"}: "#SanFrancisco #Electronics",
	}
	for in, want := range ok {
		if got, err := Normalize(in[0], in[1]); err != nil || got != want {
			t.Errorf("Normalize(%q, %q) = %q, %v; want %q", in[0], in[1], got, err, want)
		}
	}
	bad := [][2]string{
		{KeyBlueskyCardURL, "long"}, {KeyOutreachPostHour, "24"}, {KeyOutreachPostHour, "-1"},
		{KeyOutreachHashtags, "SanFrancisco"}, {KeyOutreachHashtags, "#San-Francisco"}, {KeyOutreachHashtags, "#"},
		{KeyOutreachHashtags, "#a #b #c #d #e #f #g"}, {KeyOutreachHashtags, "#2026"},
		{KeyOutreachHashtags, "#" + strings.Repeat("a", MaxHashtagRunes+1)},
	}
	for _, in := range bad {
		if _, err := Normalize(in[0], in[1]); err == nil {
			t.Errorf("Normalize(%q, %q) accepted", in[0], in[1])
		}
	}
}

func TestNormalizeOutreachChannels(t *testing.T) {
	ok := map[string]string{
		"bluesky,mastodon,instagram":  "bluesky,mastodon,instagram",
		" YouTube , bluesky ,bluesky": "bluesky,youtube",
		"tiktok":                      "tiktok",
	}
	for in, want := range ok {
		if got, err := Normalize(KeyOutreachChannels, in); err != nil || got != want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", ",", "bluesky,", "facebook", "bluesky;mastodon"} {
		if _, err := Normalize(KeyOutreachChannels, in); err == nil {
			t.Errorf("Normalize(%q) accepted", in)
		}
	}
}

// #0601: the default is lowercase; validation still accepts any case.
func TestDefaultOutreachHashtagsLowercase(t *testing.T) {
	if DefaultOutreachHashtags != "#sanfrancisco #electronics" {
		t.Errorf("default = %q", DefaultOutreachHashtags)
	}
	if got, err := Normalize(KeyOutreachHashtags, DefaultOutreachHashtags); err != nil || got != DefaultOutreachHashtags {
		t.Errorf("default does not validate: %q, %v", got, err)
	}
	if strings.Contains(msgHashtags, "CamelCase") || strings.Contains(msgHashtags, "SanFrancisco") {
		t.Error("validation message must not recommend CamelCase")
	}
}
