package mastodon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// Counts are a status's public engagement numbers as this instance sees them.
type Counts struct{ Likes, Reposts, Replies, Quotes int }

// get performs an authenticated GET and decodes a 2xx JSON body into out.
// The token never appears in the returned error.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if c == nil {
		return &APIError{Message: "client not configured", Permanent: true, kind: ErrAuth}
	}
	u := *c.base
	u.Path += path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("mastodon: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("mastodon: GET %s: %w", path, ue.Err)
		}
		return fmt.Errorf("mastodon: GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("mastodon: read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.apiError(resp.StatusCode, body)
	}
	if len(body) > maxResponseBytes || json.Unmarshal(body, out) != nil {
		return &APIError{Status: resp.StatusCode, Message: "malformed response", kind: ErrServer}
	}
	return nil
}

// GetStatusCounts reads one status's counters from this instance. A status
// that is gone is ErrNotFound. quotes_count is absent before Mastodon 4.5 and
// reads as 0. An id is a short opaque token; one that is empty or could change
// the path is refused before any request.
func (c *Client) GetStatusCounts(ctx context.Context, id string) (Counts, error) {
	if id == "" || strings.ContainsAny(id, "/?#\\") {
		return Counts{}, &APIError{Message: "invalid status id", Permanent: true, kind: ErrInvalid}
	}
	var out struct {
		Favourites *int `json:"favourites_count"`
		Reblogs    *int `json:"reblogs_count"`
		Replies    *int `json:"replies_count"`
		Quotes     *int `json:"quotes_count"`
	}
	if err := c.get(ctx, "/api/v1/statuses/"+id, nil, &out); err != nil {
		return Counts{}, err
	}
	z := func(p *int) int {
		if p == nil || *p < 0 {
			return 0
		}
		return *p
	}
	return Counts{Likes: z(out.Favourites), Reposts: z(out.Reblogs), Replies: z(out.Replies), Quotes: z(out.Quotes)}, nil
}

// Notification is one mention or quote of this account. Every string is
// third-party content and is empty when absent. ContentHTML is HTML and must
// go through PlainText before it is stored or shown.
type Notification struct {
	ID, Type, CreatedAt                                                          string
	Acct                                                                         string
	StatusID, StatusURI, StatusURL, StatusCreatedAt, InReplyToID, QuotedStatusID string
	ContentHTML                                                                  string
	// HasStatus is false for a notification that carries no status.
	HasStatus bool
}

// ListNotifications reads mention and quote notifications newer than minID
// (all recent ones when empty), newest first.
func (c *Client) ListNotifications(ctx context.Context, minID string, limit int) ([]Notification, error) {
	if limit <= 0 || limit > 80 {
		limit = 80
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}, "types[]": {"mention", "quote"}}
	if minID != "" {
		q.Set("min_id", minID)
	}
	var raw []struct {
		ID        string `json:"id"`
		Type      string `json:"type"`
		CreatedAt string `json:"created_at"`
		Account   struct {
			Acct string `json:"acct"`
		} `json:"account"`
		Status *struct {
			ID          string `json:"id"`
			URI         string `json:"uri"`
			URL         string `json:"url"`
			CreatedAt   string `json:"created_at"`
			InReplyToID string `json:"in_reply_to_id"`
			Content     string `json:"content"`
			Quote       *struct {
				QuotedStatus *struct {
					ID string `json:"id"`
				} `json:"quoted_status"`
			} `json:"quote"`
		} `json:"status"`
	}
	if err := c.get(ctx, "/api/v1/notifications", q, &raw); err != nil {
		return nil, err
	}
	out := make([]Notification, 0, len(raw))
	for _, n := range raw {
		item := Notification{ID: n.ID, Type: n.Type, CreatedAt: n.CreatedAt, Acct: n.Account.Acct}
		if s := n.Status; s != nil {
			item.HasStatus = true
			item.StatusID, item.StatusURI, item.StatusURL, item.StatusCreatedAt = s.ID, s.URI, s.URL, s.CreatedAt
			item.InReplyToID, item.ContentHTML = s.InReplyToID, s.Content
			if s.Quote != nil && s.Quote.QuotedStatus != nil {
				item.QuotedStatusID = s.Quote.QuotedStatus.ID
			}
		}
		out = append(out, item)
	}
	return out, nil
}

// PlainText turns a status's HTML into plain text with the HTML parser:
// <br> is a newline, a paragraph boundary is a blank line, text nodes are
// concatenated and entities decoded. No tag survives, and no regular
// expression is involved.
func PlainText(contentHTML string) string {
	root, err := html.Parse(strings.NewReader(contentHTML))
	if err != nil {
		return ""
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
			return
		case html.ElementNode:
			switch n.Data {
			case "script", "style":
				return
			case "br":
				b.WriteString("\n")
			case "p":
				// A paragraph that follows text starts a new one.
				if b.Len() > 0 && !strings.HasSuffix(b.String(), "\n\n") {
					b.WriteString("\n\n")
				}
			}
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			walk(ch)
		}
	}
	walk(root)
	return strings.TrimSpace(b.String())
}

// maxRecentStatuses bounds how far back FindRecentStatusWithURL looks.
const maxRecentStatuses = 40

// FindRecentStatusWithURL looks through this account's most recent statuses
// for one whose text carries link, and returns it (found false when none
// does). It exists for #0553: an Idempotency-Key is forgotten by the instance
// after about an hour, so a retry after an attempt of unknown outcome checks
// for the post it may already have made instead of posting again. The match
// is on the link as a whole token, so a link that merely starts with another
// one does not match.
func (c *Client) FindRecentStatusWithURL(ctx context.Context, link string) (Status, bool, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return Status{}, false, &APIError{Message: "empty link", Permanent: true, kind: ErrInvalid}
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := c.get(ctx, "/api/v1/accounts/verify_credentials", nil, &me); err != nil {
		return Status{}, false, err
	}
	if me.ID == "" || strings.ContainsAny(me.ID, "/?#\\") {
		return Status{}, false, &APIError{Message: "malformed response", kind: ErrServer}
	}
	var list []struct {
		ID      string `json:"id"`
		URL     string `json:"url"`
		URI     string `json:"uri"`
		Content string `json:"content"`
		Card    *struct {
			URL string `json:"url"`
		} `json:"card"`
	}
	q := url.Values{"limit": {strconv.Itoa(maxRecentStatuses)}, "exclude_reblogs": {"true"}}
	if err := c.get(ctx, "/api/v1/accounts/"+me.ID+"/statuses", q, &list); err != nil {
		return Status{}, false, err
	}
	for _, st := range list {
		if st.ID == "" {
			continue
		}
		if containsLinkToken(PlainText(st.Content), link) || containsLinkToken(st.Content, `"`+link+`"`) ||
			(st.Card != nil && st.Card.URL == link) {
			return Status{ID: st.ID, URL: st.URL, URI: st.URI}, true, nil
		}
	}
	return Status{}, false, nil
}

// containsLinkToken reports whether text contains link not immediately
// followed by another URL character.
func containsLinkToken(text, link string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], link)
		if j < 0 {
			return false
		}
		end := i + j + len(link)
		if end >= len(text) || !isURLChar(text[end]) {
			return true
		}
		i += j + 1
	}
}

func isURLChar(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || strings.IndexByte("-._~/?#%&=+", c) >= 0
}

// Account is the part of the authenticated account that Test connection
// reports.
type Account struct{ Acct, URL string }

// VerifyCredentials calls GET /api/v1/accounts/verify_credentials, a harmless
// read that proves the token is valid. It needs the profile scope (or
// read:accounts on instances older than 4.3) (#0564).
func (c *Client) VerifyCredentials(ctx context.Context) (Account, error) {
	var out struct {
		Acct string `json:"acct"`
		URL  string `json:"url"`
	}
	if err := c.get(ctx, "/api/v1/accounts/verify_credentials", nil, &out); err != nil {
		return Account{}, err
	}
	return Account{Acct: out.Acct, URL: out.URL}, nil
}
