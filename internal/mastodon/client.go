// Package mastodon is a small plain-net/http client for posting a status to
// one Mastodon instance (#0545). No media and no card: the instance builds
// the link preview from the page's OpenGraph tags.
//
// A nil *Client means "Mastodon not configured". The token never appears in
// an error or a log line.
package mastodon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultTimeout bounds each call end to end.
	DefaultTimeout = 15 * time.Second
	// MaxCharacters is the default instance limit.
	MaxCharacters = 500
	// urlWeight is what Mastodon counts every URL as, whatever its length.
	urlWeight = 23

	maxResponseBytes = 1 << 20
)

var (
	// ErrAuth is a credential or scope failure (401/403). Permanent.
	ErrAuth = errors.New("mastodon: authentication failed")
	// ErrNotFound is a 404: the status is gone, or the token cannot see it.
	// Permanent (#0547).
	ErrNotFound = errors.New("mastodon: not found")
	// ErrInvalid is a rejected status (422 and other 4xx). Permanent.
	ErrInvalid = errors.New("mastodon: invalid request")
	// ErrRateLimited is a 429. Transient.
	ErrRateLimited = errors.New("mastodon: rate limited")
	// ErrServer is a 5xx. Transient.
	ErrServer = errors.New("mastodon: server error")
)

// APIError is a non-2xx response. It never contains the token.
type APIError struct {
	Status  int
	Message string
	// Permanent is true when retrying cannot succeed.
	Permanent bool
	kind      error
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("mastodon: HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("mastodon: HTTP %d", e.Status)
}

// Unwrap lets errors.Is match the sentinel kind.
func (e *APIError) Unwrap() error { return e.kind }

// IsPermanent reports whether err is a client error retrying cannot fix.
// Network failures and anything not an *APIError are transient.
func IsPermanent(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && ae.Permanent
}

// Client talks to one instance as one account.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// New returns a client, or nil when baseURL or token is blank. The URL must
// be https, except that http is allowed for localhost and loopback so tests
// can use httptest.
func New(baseURL, token string) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil, nil
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, errors.New("mastodon: MASTODON_BASE_URL must be an absolute https URL")
	}
	host := u.Hostname()
	local := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return nil, errors.New("mastodon: MASTODON_BASE_URL must be an absolute https URL")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return &Client{base: u, token: token, http: &http.Client{Timeout: DefaultTimeout}}, nil
}

// Enabled reports whether the client is usable; safe on a nil receiver.
func (c *Client) Enabled() bool { return c != nil }

// Status is the part of the created status this site records.
type Status struct{ ID, URL, URI string }

var urlRE = regexp.MustCompile(`https?://\S+`)

// CountCharacters counts the way Mastodon does: code points, with every
// http(s) URL counted as 23 whatever its length.
func CountCharacters(text string) int {
	n := 0
	last := 0
	for _, m := range urlRE.FindAllStringIndex(text, -1) {
		// Mastodon's own matcher stops before trailing punctuation, which
		// then counts as ordinary text (#0553). Same rule as the Bluesky
		// facet code's trimLinkEnd.
		end := trimLinkEnd(text, m[0], m[1])
		if end <= m[0] {
			continue
		}
		n += utf8.RuneCountInString(text[last:m[0]]) + urlWeight
		last = end
	}
	return n + utf8.RuneCountInString(text[last:])
}

// trimLinkEnd drops trailing sentence punctuation from a matched URL, and a
// closing parenthesis when the URL has no opening one. It mirrors the Bluesky
// facet code so both channels agree on where a link ends.
func trimLinkEnd(text string, start, end int) int {
	for end > start {
		c := text[end-1]
		switch {
		case strings.IndexByte(".,;:!?'\"", c) >= 0:
			end--
		case c == ')' && !strings.Contains(text[start:end], "("):
			end--
		default:
			return end
		}
	}
	return end
}

// PostStatus creates a public status. idempotencyKey makes a retry of the
// same logical post return the original status instead of a duplicate (the
// instance remembers a key for about an hour).
func (c *Client) PostStatus(ctx context.Context, text, idempotencyKey string) (Status, error) {
	if c == nil {
		return Status{}, &APIError{Message: "client not configured", Permanent: true, kind: ErrAuth}
	}
	if strings.TrimSpace(text) == "" {
		return Status{}, &APIError{Message: "status text is empty", Permanent: true, kind: ErrInvalid}
	}
	payload, err := json.Marshal(map[string]string{"status": text, "visibility": "public", "language": "en"})
	if err != nil {
		return Status{}, fmt.Errorf("mastodon: encode request: %w", err)
	}
	u := *c.base
	u.Path += "/api/v1/statuses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return Status{}, fmt.Errorf("mastodon: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return Status{}, fmt.Errorf("mastodon: POST /api/v1/statuses: %w", ue.Err)
		}
		return Status{}, fmt.Errorf("mastodon: POST /api/v1/statuses: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Status{}, fmt.Errorf("mastodon: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Status{}, c.apiError(resp.StatusCode, body)
	}
	var st Status
	var out struct {
		ID  string `json:"id"`
		URL string `json:"url"`
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == "" {
		return Status{}, &APIError{Status: resp.StatusCode, Message: "malformed response", kind: ErrServer}
	}
	st = Status{ID: out.ID, URL: out.URL, URI: out.URI}
	return st, nil
}

func (c *Client) apiError(status int, body []byte) *APIError {
	var eb struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &eb)
	e := &APIError{Status: status, Message: strings.ReplaceAll(eb.Error, c.token, "[redacted]")}
	switch {
	case status == http.StatusTooManyRequests:
		e.kind = ErrRateLimited
	case status >= 500:
		e.kind = ErrServer
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.kind, e.Permanent = ErrAuth, true
	case status == http.StatusNotFound:
		e.kind, e.Permanent = ErrNotFound, true
	default:
		e.kind, e.Permanent = ErrInvalid, true
	}
	return e
}
