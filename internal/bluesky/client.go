// Package bluesky is a small plain-net/http client for posting to Bluesky
// (#0545): app-password session, record creation with link and hashtag
// facets, and an external link card with an optional thumbnail. No SDK.
//
// A nil *Client means "Bluesky not configured": New returns nil when the
// handle or app password is blank, and Enabled reports that without
// panicking. Nothing here ever puts the app password or a session token in
// an error or a log line.
package bluesky

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTimeout bounds each HTTP call end to end.
	DefaultTimeout = 15 * time.Second
	// MaxThumbBytes is the largest thumbnail the lexicon accepts.
	MaxThumbBytes = 1_000_000
	// DefaultPDSURL is where an unset BLUESKY_PDS_URL points.
	DefaultPDSURL = "https://bsky.social"
	// DefaultAppViewURL is the public, unauthenticated AppView that serves
	// engagement counts (#0547).
	DefaultAppViewURL = "https://public.api.bsky.app"

	maxResponseBytes = 4 << 20
	postCollection   = "app.bsky.feed.post"
)

// Client talks to one PDS as one account.
type Client struct {
	base        *url.URL
	appView     *url.URL
	handle      string
	appPassword string
	http        *http.Client

	mu   sync.Mutex // guards sess; held across login calls on purpose, so two callers never log in at once
	sess *session
}

// New returns a client, or nil when handle or appPassword is blank. An empty
// pdsURL means DefaultPDSURL; a malformed one is an error.
func New(pdsURL, handle, appPassword string) (*Client, error) {
	handle = strings.TrimSpace(handle)
	appPassword = strings.TrimSpace(appPassword)
	if handle == "" || appPassword == "" {
		return nil, nil
	}
	pdsURL = strings.TrimSpace(pdsURL)
	if pdsURL == "" {
		pdsURL = DefaultPDSURL
	}
	u, err := url.Parse(pdsURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("bluesky: BLUESKY_PDS_URL must be an absolute http(s) URL")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	av, _ := url.Parse(DefaultAppViewURL)
	return &Client{base: u, appView: av, handle: handle, appPassword: appPassword, http: &http.Client{Timeout: DefaultTimeout}}, nil
}

// Enabled reports whether the client is usable; safe on a nil receiver.
func (c *Client) Enabled() bool { return c != nil }

// PostInput is one post to create.
type PostInput struct {
	Text string
	// RKey is the record key. Derive it deterministically (TID) so a retry can
	// tell whether an earlier attempt already created the record.
	RKey      string
	CreatedAt time.Time
	Embed     *ExternalCard // nil: no link card
}

// ExternalCard is an app.bsky.embed.external link card.
type ExternalCard struct {
	URI, Title, Description string
	Thumb                   []byte // nil: no thumbnail; uploaded with uploadBlob
	ThumbMIME               string // image/jpeg or image/png
}

// PostResult describes the created (or already existing) record.
type PostResult struct {
	URI, CID, RKey, WebURL string
	// AlreadyExisted is true when getRecord found the record, so nothing was
	// created.
	AlreadyExisted bool
	// ThumbErr is non-nil when the thumbnail could not be uploaded and the
	// post went out without it. It is never fatal.
	ThumbErr error
}

// Post validates the text, skips creation when the record already exists,
// uploads the thumbnail if any, and creates the record.
func (c *Client) Post(ctx context.Context, in PostInput) (PostResult, error) {
	if c == nil {
		return PostResult{}, &APIError{Message: "client not configured", Permanent: true, kind: ErrAuth}
	}
	if err := ValidateText(in.Text); err != nil {
		return PostResult{}, err
	}
	if in.RKey == "" {
		return PostResult{}, &APIError{Message: "missing record key", Permanent: true, kind: ErrInvalid}
	}
	did, handle, err := c.identity(ctx)
	if err != nil {
		return PostResult{}, err
	}
	res := PostResult{RKey: in.RKey, WebURL: "https://bsky.app/profile/" + handle + "/post/" + in.RKey}

	if uri, found, err := c.getRecord(ctx, did, in.RKey); err != nil {
		return PostResult{}, err
	} else if found {
		res.URI, res.AlreadyExisted = uri, true
		return res, nil
	}

	record := map[string]any{
		"$type":     postCollection,
		"text":      in.Text,
		"createdAt": in.CreatedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
		"langs":     []string{"en"},
	}
	if f := DetectFacets(in.Text); len(f) > 0 {
		record["facets"] = f
	}
	if in.Embed != nil {
		ext := map[string]any{"uri": in.Embed.URI, "title": in.Embed.Title, "description": in.Embed.Description}
		if len(in.Embed.Thumb) > 0 && len(in.Embed.Thumb) <= MaxThumbBytes && in.Embed.ThumbMIME != "" {
			blob, terr := c.uploadBlob(ctx, in.Embed.Thumb, in.Embed.ThumbMIME)
			if terr != nil {
				res.ThumbErr = terr
			} else {
				ext["thumb"] = blob
			}
		}
		record["embed"] = map[string]any{"$type": "app.bsky.embed.external", "external": ext}
	}
	var out struct {
		URI string `json:"uri"`
		CID string `json:"cid"`
	}
	err = c.authed(ctx, http.MethodPost, "/xrpc/com.atproto.repo.createRecord", nil, "application/json",
		mustJSON(map[string]any{"repo": did, "collection": postCollection, "rkey": in.RKey, "record": record}), &out)
	if err != nil {
		return PostResult{}, err
	}
	res.URI, res.CID = out.URI, out.CID
	return res, nil
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("bluesky: marshal: %v", err)) // only ever called on maps of strings and RawMessage
	}
	return b
}

// getRecord reports whether the record exists. A missing record is a 400
// RecordNotFound or a 404 depending on the PDS; both mean "create it".
func (c *Client) getRecord(ctx context.Context, did, rkey string) (uri string, found bool, err error) {
	q := url.Values{"repo": {did}, "collection": {postCollection}, "rkey": {rkey}}
	var out struct {
		URI string `json:"uri"`
	}
	err = c.authed(ctx, http.MethodGet, "/xrpc/com.atproto.repo.getRecord", q, "", nil, &out)
	if err == nil {
		return out.URI, true, nil
	}
	var ae *APIError
	if errors.As(err, &ae) && (ae.Status == http.StatusNotFound || ae.Code == "RecordNotFound") {
		return "", false, nil
	}
	return "", false, err
}

// uploadBlob uploads image bytes and returns the PDS's blob object verbatim.
func (c *Client) uploadBlob(ctx context.Context, data []byte, mime string) (json.RawMessage, error) {
	var out struct {
		Blob json.RawMessage `json:"blob"`
	}
	if err := c.authed(ctx, http.MethodPost, "/xrpc/com.atproto.repo.uploadBlob", nil, mime, data, &out); err != nil {
		return nil, err
	}
	if len(out.Blob) == 0 {
		return nil, &APIError{Message: "uploadBlob returned no blob", kind: ErrServer}
	}
	return out.Blob, nil
}

// roundTrip performs one HTTP call. A network failure is returned as a plain
// (transient) error; any HTTP response comes back as status plus body.
func (c *Client) roundTrip(ctx context.Context, method, path string, q url.Values, contentType, bearer string, payload []byte) (int, []byte, error) {
	return c.roundTripTo(ctx, c.base, nil, method, path, q, contentType, bearer, payload)
}

// roundTripTo is roundTrip against an explicit base URL (the PDS or the
// AppView) with optional extra request headers.
func (c *Client) roundTripTo(ctx context.Context, base *url.URL, extra http.Header, method, path string, q url.Values, contentType, bearer string, payload []byte) (int, []byte, error) {
	u := *base
	u.Path += path
	u.RawQuery = q.Encode()
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return 0, nil, fmt.Errorf("bluesky: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	for k, vs := range extra {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return 0, nil, fmt.Errorf("bluesky: %s %s: %w", method, path, ue.Err)
		}
		return 0, nil, fmt.Errorf("bluesky: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return 0, nil, fmt.Errorf("bluesky: read response: %w", err)
	}
	if len(b) > maxResponseBytes {
		return 0, nil, fmt.Errorf("bluesky: response exceeds %d bytes", maxResponseBytes)
	}
	return resp.StatusCode, b, nil
}

// apiError builds the classified error for a non-2xx response, with the
// password and both session tokens scrubbed from the message.
func (c *Client) apiError(status int, body []byte) *APIError {
	var eb struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &eb)
	return classify(status, eb.Error, c.redact(eb.Message))
}

func (c *Client) redact(s string) string {
	if s == "" {
		return s
	}
	secrets := []string{c.appPassword}
	c.mu.Lock()
	if c.sess != nil {
		secrets = append(secrets, c.sess.access, c.sess.refresh)
	}
	c.mu.Unlock()
	return redactAll(s, secrets...)
}

func redactAll(s string, secrets ...string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "[redacted]")
		}
	}
	return s
}
