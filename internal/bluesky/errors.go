package bluesky

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel kinds, matched with errors.Is. The concrete *APIError carries the
// HTTP status, the XRPC error code and a redacted message.
var (
	// ErrAuth is a credential failure: bad handle or app password, a taken-down
	// account, a second-factor requirement. Permanent: retrying cannot help.
	ErrAuth = errors.New("bluesky: authentication failed")
	// ErrRateLimited is a 429. Transient.
	ErrRateLimited = errors.New("bluesky: rate limited")
	// ErrTextTooLong is a post over the grapheme or byte limit. Permanent.
	ErrTextTooLong = errors.New("bluesky: text too long")
	// ErrInvalid is a request the PDS (or this client) rejects as malformed.
	// Permanent.
	ErrInvalid = errors.New("bluesky: invalid request")
	// ErrServer is a 5xx. Transient.
	ErrServer = errors.New("bluesky: server error")
)

// APIError is a failure from the PDS, or a pre-flight rejection. It never
// contains the app password or a session token.
type APIError struct {
	Status  int    // HTTP status; 0 for a pre-flight rejection
	Code    string // XRPC "error" code, e.g. "InvalidRequest"
	Message string
	// Permanent is true when retrying the same request cannot succeed, so the
	// caller should fail the post immediately instead of backing off.
	Permanent bool
	kind      error
}

func (e *APIError) Error() string {
	var b strings.Builder
	b.WriteString("bluesky")
	if e.Status != 0 {
		fmt.Fprintf(&b, ": HTTP %d", e.Status)
	}
	if e.Code != "" {
		b.WriteString(": " + e.Code)
	}
	if e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	return b.String()
}

// Unwrap lets errors.Is match the sentinel kind.
func (e *APIError) Unwrap() error { return e.kind }

// IsPermanent reports whether err is a client error that retrying cannot fix.
// Anything that is not an *APIError (network failure, timeout) is transient.
func IsPermanent(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Permanent
}

// Code returns the XRPC error code of err, or "".
func Code(err error) string {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}

func classify(status int, code, msg string) *APIError {
	e := &APIError{Status: status, Code: code, Message: msg}
	switch {
	case status == 429:
		e.kind = ErrRateLimited
	case status >= 500:
		e.kind = ErrServer
	case status == 401 || status == 403 || code == "AuthFactorTokenRequired" || code == "AccountTakedown" || code == "AuthenticationRequired":
		e.kind = ErrAuth
		e.Permanent = true
	default:
		e.kind = ErrInvalid
		e.Permanent = true
	}
	return e
}
