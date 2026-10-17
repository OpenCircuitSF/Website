package bluesky

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// getPostsBatch is the most uris app.bsky.feed.getPosts accepts per call.
const getPostsBatch = 25

// appViewProxy routes a notification read through the PDS to the AppView; a
// self-hosted PDS may not proxy app.bsky.* without it.
const appViewProxy = "did:web:api.bsky.app#bsky_appview"

// SetAppViewURL points engagement reads at a different AppView. Tests use it;
// no deploy needs to (#0547).
func (c *Client) SetAppViewURL(raw string) error {
	if c == nil {
		return errors.New("bluesky: client not configured")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("bluesky: AppView URL must be an absolute http(s) URL")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	c.appView = u
	return nil
}

// Counts are a post's public engagement numbers.
type Counts struct{ Likes, Reposts, Replies, Quotes int }

// GetPostCounts calls app.bsky.feed.getPosts on the public AppView with no
// credentials, batching 25 uris per request. Posts the AppView omits
// (deleted, taken down) are absent from the map.
func (c *Client) GetPostCounts(ctx context.Context, uris []string) (map[string]Counts, error) {
	out := map[string]Counts{}
	if c == nil {
		return out, &APIError{Message: "client not configured", Permanent: true, kind: ErrAuth}
	}
	for start := 0; start < len(uris); start += getPostsBatch {
		end := min(start+getPostsBatch, len(uris))
		q := url.Values{"uris": uris[start:end]}
		// No bearer: the AppView read is public, and the session token must
		// never leave for a host other than the user's PDS.
		status, body, err := c.roundTripTo(ctx, c.appView, nil, http.MethodGet, "/xrpc/app.bsky.feed.getPosts", q, "", "", nil)
		if err != nil {
			return nil, err
		}
		if status < 200 || status >= 300 {
			return nil, c.apiError(status, body)
		}
		var resp struct {
			Posts []struct {
				URI         string `json:"uri"`
				LikeCount   int    `json:"likeCount"`
				RepostCount int    `json:"repostCount"`
				ReplyCount  int    `json:"replyCount"`
				QuoteCount  int    `json:"quoteCount"`
			} `json:"posts"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, &APIError{Status: status, Message: "malformed response", kind: ErrServer}
		}
		for _, p := range resp.Posts {
			out[p.URI] = Counts{Likes: p.LikeCount, Reposts: p.RepostCount, Replies: p.ReplyCount, Quotes: p.QuoteCount}
		}
	}
	return out, nil
}

// Notification is one mention, reply or quote of this account. Every string
// is third-party content and is empty when absent.
type Notification struct {
	URI, Reason, ReasonSubject, IndexedAt string
	AuthorHandle                          string
	Text, CreatedAt                       string
	ReplyParent, ReplyRoot, QuoteURI      string
}

// NotificationPage is one page of notifications, newest first.
type NotificationPage struct {
	Items  []Notification
	Cursor string // empty on the last page
}

// ListNotifications reads app.bsky.notification.listNotifications for the
// mention, reply and quote reasons. cursor is empty for the first page.
func (c *Client) ListNotifications(ctx context.Context, cursor string, limit int) (NotificationPage, error) {
	if c == nil {
		return NotificationPage{}, &APIError{Message: "client not configured", Permanent: true, kind: ErrAuth}
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	q := url.Values{"limit": {strconv.Itoa(limit)}, "reasons": {"mention", "reply", "quote"}}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	var resp struct {
		Cursor        string `json:"cursor"`
		Notifications []struct {
			URI           string `json:"uri"`
			Reason        string `json:"reason"`
			ReasonSubject string `json:"reasonSubject"`
			IndexedAt     string `json:"indexedAt"`
			Author        struct {
				Handle string `json:"handle"`
			} `json:"author"`
			Record json.RawMessage `json:"record"`
		} `json:"notifications"`
	}
	hdr := http.Header{"Atproto-Proxy": {appViewProxy}}
	if err := c.authedWith(ctx, hdr, http.MethodGet, "/xrpc/app.bsky.notification.listNotifications", q, "", nil, &resp); err != nil {
		return NotificationPage{}, err
	}
	page := NotificationPage{Cursor: resp.Cursor}
	for _, n := range resp.Notifications {
		// The server should filter by reason; do not rely on it.
		if n.Reason != "mention" && n.Reason != "reply" && n.Reason != "quote" {
			continue
		}
		item := Notification{URI: n.URI, Reason: n.Reason, ReasonSubject: n.ReasonSubject, IndexedAt: n.IndexedAt, AuthorHandle: n.Author.Handle}
		parseRecord(n.Record, &item)
		page.Items = append(page.Items, item)
	}
	return page, nil
}

type uriRef struct {
	URI string `json:"uri"`
}

// parseRecord pulls the fields this site uses out of an app.bsky.feed.post
// record. A record that does not parse leaves them empty.
func parseRecord(raw json.RawMessage, n *Notification) {
	if len(raw) == 0 {
		return
	}
	var rec struct {
		Text      string `json:"text"`
		CreatedAt string `json:"createdAt"`
		Reply     *struct {
			Parent uriRef `json:"parent"`
			Root   uriRef `json:"root"`
		} `json:"reply"`
		Embed *struct {
			Type   string `json:"$type"`
			Record *struct {
				URI    string  `json:"uri"`
				Record *uriRef `json:"record"`
			} `json:"record"`
		} `json:"embed"`
	}
	if json.Unmarshal(raw, &rec) != nil {
		return
	}
	n.Text, n.CreatedAt = rec.Text, rec.CreatedAt
	if rec.Reply != nil {
		n.ReplyParent, n.ReplyRoot = rec.Reply.Parent.URI, rec.Reply.Root.URI
	}
	if rec.Embed != nil && rec.Embed.Record != nil {
		switch rec.Embed.Type {
		case "app.bsky.embed.record":
			n.QuoteURI = rec.Embed.Record.URI
		case "app.bsky.embed.recordWithMedia":
			if rec.Embed.Record.Record != nil {
				n.QuoteURI = rec.Embed.Record.Record.URI
			}
		}
	}
}
