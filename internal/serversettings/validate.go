package serversettings

import (
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxValueLen = 2048

// ValidationError names the offending field and the rule it broke, never the
// value.
type ValidationError struct{ Key, Msg string }

func (e *ValidationError) Error() string { return e.Key + ": " + e.Msg }

const (
	msgMediaDir   = "must be an absolute directory path other than /"
	msgEmail      = "must be an email address, optionally with a display name"
	msgListDomain = "must be a mail subdomain such as lists.example.org, never a bare domain"
	msgBool       = "must be true or false"
	msgBucket     = "must be an S3 bucket name (3 to 63 lowercase letters, digits, dots or hyphens)"
	msgBatch      = "must be a whole number from 1 to 500"
	msgCardURL    = "must be short or canonical"
	msgHashtags   = "must be up to 6 hashtags, each # followed by up to 40 letters or digits (not digits alone), such as #SanFrancisco"
	msgPostHour   = "must be a whole hour from 0 to 23 (Pacific time)"
)

// Normalize validates value for key and returns the form to store. It returns
// *ValidationError for a rejected value; the message never echoes the value.
func Normalize(key, value string) (string, error) {
	if _, ok := FieldByKey(key); !ok {
		return "", &ValidationError{Key: key, Msg: "unknown key"}
	}
	value = strings.TrimSpace(value)
	bad := func(msg string) (string, error) { return "", &ValidationError{Key: key, Msg: msg} }
	msg := map[string]string{
		KeyMediaDir: msgMediaDir, KeyEmailFrom: msgEmail, KeyEmailReplyTo: msgEmail,
		KeyEmailListDomain: msgListDomain, KeySESSandbox: msgBool,
		KeySESInboundBucket: msgBucket, KeySendBatchSize: msgBatch,
		KeyBlueskyCardURL: msgCardURL, KeyOutreachHashtags: msgHashtags, KeyOutreachPostHour: msgPostHour,
	}[key]
	if value == "" || len(value) > maxValueLen || hasControl(value) {
		return bad(msg)
	}
	switch key {
	case KeyMediaDir:
		if !filepath.IsAbs(value) || filepath.Clean(value) == "/" {
			return bad(msg)
		}
		return filepath.Clean(value), nil
	case KeyEmailFrom, KeyEmailReplyTo:
		if _, err := mail.ParseAddress(value); err != nil {
			return bad(msg)
		}
		return value, nil
	case KeyEmailListDomain:
		d := strings.TrimSuffix(strings.ToLower(value), ".")
		if !validListDomain(d) {
			return bad(msg)
		}
		return d, nil
	case KeySESSandbox:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return bad(msg)
		}
		return strconv.FormatBool(b), nil
	case KeySESInboundBucket:
		if !validBucket(value) {
			return bad(msg)
		}
		return value, nil
	case KeyBlueskyCardURL:
		d := strings.ToLower(value)
		if d != CardURLShort && d != CardURLCanonical {
			return bad(msg)
		}
		return d, nil
	case KeyOutreachHashtags:
		tags, ok := NormalizeHashtags(value)
		if !ok || len(tags) == 0 {
			return bad(msg)
		}
		return strings.Join(tags, " "), nil
	case KeyOutreachPostHour:
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 23 {
			return bad(msg)
		}
		return strconv.Itoa(n), nil
	default: // KeySendBatchSize
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 500 {
			return bad(msg)
		}
		return strconv.Itoa(n), nil
	}
}

func hasControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func validListDomain(d string) bool {
	if len(d) == 0 || len(d) > 253 {
		return false
	}
	labels := strings.Split(d, ".")
	if len(labels) < 3 {
		return false
	}
	for _, l := range labels {
		if len(l) < 1 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, r := range l {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

func validBucket(b string) bool {
	if len(b) < 3 || len(b) > 63 {
		return false
	}
	alnum := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }
	if !alnum(b[0]) || !alnum(b[len(b)-1]) {
		return false
	}
	for i := 0; i < len(b); i++ {
		if !alnum(b[i]) && b[i] != '.' && b[i] != '-' {
			return false
		}
	}
	return true
}

// NormalizeHashtags splits s on whitespace and checks that every token is a
// "#" followed by letters or digits, at least one a letter and at most
// MaxHashtagRunes in all (CamelCase is recommended, so screen
// readers say the words apart) and that there are at most MaxOutreachHashtags.
// Duplicates, ignoring case, are dropped.
func NormalizeHashtags(s string) ([]string, bool) {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.Fields(s) {
		body := strings.TrimPrefix(tok, "#")
		if body == tok || body == "" || utf8.RuneCountInString(body) > MaxHashtagRunes {
			return nil, false
		}
		letter := false
		for _, r := range body {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return nil, false
			}
			letter = letter || unicode.IsLetter(r)
		}
		// Bluesky and Mastodon do not make an all-digit token a hashtag.
		if !letter {
			return nil, false
		}
		if k := strings.ToLower(tok); !seen[k] {
			seen[k] = true
			out = append(out, tok)
		}
	}
	return out, len(out) <= MaxOutreachHashtags
}
