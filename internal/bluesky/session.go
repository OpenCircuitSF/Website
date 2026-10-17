package bluesky

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// session is the cached login. createSession is capped (30 per 5 minutes,
// 300 per day per account), so the pair is kept in memory and refreshed;
// a process restart costs one login.
type session struct {
	access, refresh, did, handle string
}

type sessionResponse struct {
	AccessJwt  string `json:"accessJwt"`
	RefreshJwt string `json:"refreshJwt"`
	DID        string `json:"did"`
	Handle     string `json:"handle"`
}

// identity returns the account DID and handle, logging in if needed.
func (c *Client) identity(ctx context.Context) (did, handle string, err error) {
	s, err := c.session(ctx)
	if err != nil {
		return "", "", err
	}
	return s.did, s.handle, nil
}

// session returns the cached session, creating one when there is none.
func (c *Client) session(ctx context.Context) (session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess != nil {
		return *c.sess, nil
	}
	return c.loginLocked(ctx)
}

// loginLocked calls createSession. Callers hold c.mu.
func (c *Client) loginLocked(ctx context.Context) (session, error) {
	status, body, err := c.roundTrip(ctx, http.MethodPost, "/xrpc/com.atproto.server.createSession", nil, "application/json", "",
		mustJSON(map[string]string{"identifier": c.handle, "password": c.appPassword}))
	if err != nil {
		return session{}, err
	}
	if status < 200 || status >= 300 {
		return session{}, c.apiErrorLocked(status, body)
	}
	return c.storeLocked(body)
}

// refreshLocked calls refreshSession with the refresh token. Callers hold c.mu.
func (c *Client) refreshLocked(ctx context.Context, refresh string) (session, error) {
	status, body, err := c.roundTrip(ctx, http.MethodPost, "/xrpc/com.atproto.server.refreshSession", nil, "", refresh, nil)
	if err != nil {
		return session{}, err
	}
	if status < 200 || status >= 300 {
		return session{}, c.apiErrorLocked(status, body)
	}
	return c.storeLocked(body)
}

func (c *Client) storeLocked(body []byte) (session, error) {
	var sr sessionResponse
	if err := json.Unmarshal(body, &sr); err != nil || sr.AccessJwt == "" || sr.RefreshJwt == "" || sr.DID == "" {
		return session{}, &APIError{Message: "malformed session response", kind: ErrServer}
	}
	s := session{access: sr.AccessJwt, refresh: sr.RefreshJwt, did: sr.DID, handle: sr.Handle}
	if s.handle == "" {
		s.handle = c.handle
	}
	c.sess = &s
	return s, nil
}

// apiErrorLocked is apiError for callers already holding c.mu.
func (c *Client) apiErrorLocked(status int, body []byte) *APIError {
	var eb struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &eb)
	secrets := []string{c.appPassword}
	if c.sess != nil {
		secrets = append(secrets, c.sess.access, c.sess.refresh)
	}
	return classify(status, eb.Error, redactAll(eb.Message, secrets...))
}

// isAuthFailure reports a response that means the access token is no good.
func isAuthFailure(status int, body []byte) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	if status == http.StatusBadRequest {
		var eb struct {
			Error string `json:"error"`
		}
		return json.Unmarshal(body, &eb) == nil && eb.Error == "ExpiredToken"
	}
	return false
}

// authed performs an authenticated call and decodes a 2xx JSON body into out.
// On an expired or rejected access token it refreshes once and retries; if
// the refresh itself is refused it logs in afresh once and retries; a second
// auth failure is a permanent ErrAuth.
func (c *Client) authed(ctx context.Context, method, path string, q url.Values, contentType string, payload []byte, out any) error {
	return c.authedWith(ctx, nil, method, path, q, contentType, payload, out)
}

// authedWith is authed with extra request headers on every attempt.
func (c *Client) authedWith(ctx context.Context, extra http.Header, method, path string, q url.Values, contentType string, payload []byte, out any) error {
	s, err := c.session(ctx)
	if err != nil {
		return err
	}
	status, body, err := c.roundTripTo(ctx, c.base, extra, method, path, q, contentType, s.access, payload)
	if err != nil {
		return err
	}
	if isAuthFailure(status, body) {
		s, err = c.renew(ctx, s)
		if err != nil {
			return err
		}
		status, body, err = c.roundTripTo(ctx, c.base, extra, method, path, q, contentType, s.access, payload)
		if err != nil {
			return err
		}
		if isAuthFailure(status, body) {
			return &APIError{Status: status, Code: "AuthenticationRequired", Message: "session rejected after renewal", Permanent: true, kind: ErrAuth}
		}
	}
	if status < 200 || status >= 300 {
		return c.apiError(status, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &APIError{Status: status, Message: "malformed response", kind: ErrServer}
	}
	return nil
}

// renew replaces a rejected session: refreshSession first, a fresh
// createSession if the refresh is refused. If another caller already
// replaced the rejected session, that newer one is returned instead.
func (c *Client) renew(ctx context.Context, stale session) (session, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sess != nil && c.sess.access != stale.access {
		return *c.sess, nil
	}
	s, err := c.refreshLocked(ctx, stale.refresh)
	if err == nil {
		return s, nil
	}
	if IsPermanent(err) || Code(err) == "ExpiredToken" {
		c.sess = nil
		return c.loginLocked(ctx)
	}
	return session{}, err
}
