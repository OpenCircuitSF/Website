package serversettings

import (
	"net/mail"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
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
