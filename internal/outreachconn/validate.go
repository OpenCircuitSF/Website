package outreachconn

import (
	"errors"
	"net/url"
	"strings"
	"unicode"
)

const (
	maxURLLen    = 2048
	maxHandleLen = 253
	maxSecretLen = 1024
)

// ValidationError names the offending field and the rule it broke, never the
// value.
type ValidationError struct{ Key, Msg string }

func (e *ValidationError) Error() string { return e.Key + ": " + e.Msg }

// ValidServiceURL accepts an https URL with a host and no userinfo, query, or
// fragment. http is allowed only for localhost and 127.0.0.1.
func ValidServiceURL(s string) error {
	bad := errors.New("must be an https URL (http is allowed only for localhost and 127.0.0.1)")
	if len(s) > maxURLLen {
		return bad
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return bad
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(s, "#") {
		return bad
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if h := u.Hostname(); h == "localhost" || h == "127.0.0.1" {
			return nil
		}
	}
	return bad
}

// hasSpaceOrControl reports whitespace or a control character anywhere.
func hasSpaceOrControl(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// Normalize trims value and applies the field's rule, returning the value to
// store. It returns *ValidationError for a rejected value.
func Normalize(key, value string) (string, error) {
	f, ok := FieldByKey(key)
	if !ok {
		return "", &ValidationError{Key: key, Msg: "unknown key"}
	}
	value = strings.TrimSpace(value)
	switch {
	case f.Secret:
		if value == "" || len(value) > maxSecretLen || hasSpaceOrControl(value) {
			return "", &ValidationError{Key: key, Msg: "must be 1 to 1024 characters with no whitespace"}
		}
		return value, nil
	case key == KeyBlueskyHandle:
		value = strings.TrimPrefix(value, "@")
		if value == "" || len(value) > maxHandleLen || hasSpaceOrControl(value) {
			return "", &ValidationError{Key: key, Msg: "must be 1 to 253 characters with no whitespace"}
		}
		return value, nil
	default:
		if err := ValidServiceURL(value); err != nil {
			return "", &ValidationError{Key: key, Msg: err.Error()}
		}
		return strings.TrimRight(value, "/"), nil
	}
}
