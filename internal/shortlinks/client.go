// Package shortlinks is a small client for the ShortLinks REST API
// (the separate go.opencircuitsf.com deploy). It authenticates with a bearer
// API token, bounds every call with a timeout and a response size limit, and
// decodes ShortLinks' error body into typed sentinel errors.
//
// A nil *Client means "outreach unavailable": New returns nil when either the
// base URL or the token is unset, and Enabled reports that without panicking.
package shortlinks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultTimeout bounds each call end to end.
	DefaultTimeout = 15 * time.Second
	// maxResponseBytes caps how much of a response body is read (QR SVGs and
	// campaign stats are the largest payloads; both are well under this).
	maxResponseBytes = 4 << 20
)

// Sentinel errors, matched with errors.Is. The concrete *APIError carries the
// status, ShortLinks' message, and Retry-After for 429.
var (
	ErrUnauthenticated = errors.New("shortlinks: unauthenticated")
	ErrForbidden       = errors.New("shortlinks: forbidden")
	ErrNotFound        = errors.New("shortlinks: not found")
	ErrConflict        = errors.New("shortlinks: conflict")
	ErrRateLimited     = errors.New("shortlinks: rate limited")
	ErrUnavailable     = errors.New("shortlinks: client not configured")
)

// APIError is a non-2xx response. It never contains the token.
type APIError struct {
	Status     int
	Message    string        // ShortLinks' {"error": ...} text, when present
	RetryAfter time.Duration // set for 429 when the header parses
	kind       error
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("shortlinks: HTTP %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("shortlinks: HTTP %d", e.Status)
}

// Unwrap lets errors.Is match the sentinel for the status class.
func (e *APIError) Unwrap() error { return e.kind }

// Client talks to one ShortLinks server.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// New returns a client, or nil when baseURL or token is empty (outreach
// disabled). An unparsable baseURL is an error.
func New(baseURL, token string) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil, nil
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("shortlinks: SHORTLINKS_BASE_URL must be an absolute http(s) URL")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return &Client{base: u, token: token, http: &http.Client{Timeout: DefaultTimeout}}, nil
}

// Enabled reports whether the client is usable; safe on a nil receiver.
func (c *Client) Enabled() bool { return c != nil }

// Me is the GET /api/me body.
type Me struct {
	ID       int64  `json:"id"`
	Email    string `json:"email"`
	IsAdmin  bool   `json:"is_admin"`
	BaseURL  string `json:"base_url"` // short-URL host to display
	APIToken *struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		ReadOnly bool   `json:"read_only"`
	} `json:"api_token"`
}

// Campaign is the subset of a ShortLinks campaign this site uses.
type Campaign struct {
	ID               int64   `json:"id"`
	Name             string  `json:"name"`
	Slug             string  `json:"slug"`
	Description      string  `json:"description"`
	DefaultUTMSource string  `json:"default_utm_source"`
	DefaultUTMMedium string  `json:"default_utm_medium"`
	StartsAt         *string `json:"starts_at"`
	EndsAt           *string `json:"ends_at"`
	Archived         bool    `json:"archived"`
	LinkCount        int64   `json:"link_count"`
	TotalClicks      int64   `json:"total_clicks"`
}

// CreateCampaignRequest mirrors ShortLinks' createCampaignRequest. The slug is
// derived server-side from Name.
type CreateCampaignRequest struct {
	Name               string `json:"name"`
	Description        string `json:"description,omitempty"`
	StartsAt           string `json:"starts_at,omitempty"` // RFC 3339
	EndsAt             string `json:"ends_at,omitempty"`
	DefaultUTMSource   string `json:"default_utm_source,omitempty"`
	DefaultUTMMedium   string `json:"default_utm_medium,omitempty"`
	DefaultUTMCampaign string `json:"default_utm_campaign,omitempty"`
	DefaultUTMTerm     string `json:"default_utm_term,omitempty"`
	DefaultUTMContent  string `json:"default_utm_content,omitempty"`
}

// CreateLinkRequest mirrors ShortLinks' createLinkRequest. Key is an optional
// custom key. POST /api/links de-duplicates on DestinationURL.
type CreateLinkRequest struct {
	DestinationURL string `json:"destination_url"`
	Title          string `json:"title,omitempty"`
	Key            string `json:"key,omitempty"`
	CampaignSlug   string `json:"campaign_slug,omitempty"`
	UTMSource      string `json:"utm_source,omitempty"`
	UTMMedium      string `json:"utm_medium,omitempty"`
	UTMCampaign    string `json:"utm_campaign,omitempty"`
	UTMTerm        string `json:"utm_term,omitempty"`
	UTMContent     string `json:"utm_content,omitempty"`
	Placement      string `json:"placement,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"` // RFC 3339
}

// Link is the subset of a ShortLinks link this site uses.
type Link struct {
	ID             int64  `json:"id"`
	Key            string `json:"key"`
	DestinationURL string `json:"destination_url"`
	Title          string `json:"title"`
	Active         bool   `json:"active"`
	ClickCount     int64  `json:"click_count"`
	UTMSource      string `json:"utm_source"`
	UTMMedium      string `json:"utm_medium"`
	UTMCampaign    string `json:"utm_campaign"`
	UTMTerm        string `json:"utm_term"`
	UTMContent     string `json:"utm_content"`
	Placement      string `json:"placement"`
	Duplicate      *bool  `json:"duplicate,omitempty"`
}

// Me calls GET /api/me.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var out Me
	if err := c.doJSON(ctx, http.MethodGet, "/api/me", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCampaign calls GET /api/campaigns/{slug}; ErrNotFound when absent.
func (c *Client) GetCampaign(ctx context.Context, slug string) (*Campaign, error) {
	var out Campaign
	if err := c.doJSON(ctx, http.MethodGet, "/api/campaigns/"+url.PathEscape(slug), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateCampaign calls POST /api/campaigns.
func (c *Client) CreateCampaign(ctx context.Context, req CreateCampaignRequest) (*Campaign, error) {
	var out Campaign
	if err := c.doJSON(ctx, http.MethodPost, "/api/campaigns", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EnsureCampaign returns the campaign whose slug is slug, creating it when
// GET answers 404. ShortLinks has no client-chosen slug and no idempotency
// key: the slug is derived from the name, so the campaign is always named
// with the slug itself (ShortLinks' slugify leaves an already-slugged string
// unchanged) and req.Name is overwritten. A separate display name therefore
// cannot exist; put a human-readable label in req.Description. The create
// response's own slug is authoritative and is what the returned Campaign
// carries. Idempotent by convention only: two concurrent callers can both see
// 404 and the second create then gets a suffixed slug.
func (c *Client) EnsureCampaign(ctx context.Context, slug string, req CreateCampaignRequest) (*Campaign, error) {
	got, err := c.GetCampaign(ctx, slug)
	if err == nil {
		return got, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	req.Name = slug
	return c.CreateCampaign(ctx, req)
}

// CreateLink calls POST /api/links. Without a custom Key, a retried create
// with the same destination returns the existing link (Duplicate true). That
// de-duplication does NOT apply when req.Key is set: a retry then answers 409
// (ErrConflict). Use EnsureLink for an idempotent create with a custom key.
func (c *Client) CreateLink(ctx context.Context, req CreateLinkRequest) (*Link, error) {
	var out Link
	if err := c.doJSON(ctx, http.MethodPost, "/api/links", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EnsureLink creates a link with a custom Key idempotently: on 409 it fetches
// the existing link with GetLink and accepts it only when its destination
// matches strings.TrimSpace(req.DestinationURL), the value ShortLinks stores (any other holder of the key stays ErrConflict).
// req.Key must be set.
func (c *Client) EnsureLink(ctx context.Context, req CreateLinkRequest) (*Link, error) {
	if req.Key == "" {
		return nil, errors.New("shortlinks: EnsureLink requires a Key")
	}
	l, err := c.CreateLink(ctx, req)
	if err == nil || !errors.Is(err, ErrConflict) {
		return l, err
	}
	got, gerr := c.GetLink(ctx, req.Key)
	if gerr != nil {
		if errors.Is(gerr, ErrNotFound) {
			// GET is scoped to the caller: a 404 after a 409 means the key
			// belongs to another user.
			return nil, err
		}
		return nil, gerr
	}
	if got.DestinationURL != strings.TrimSpace(req.DestinationURL) {
		return nil, err
	}
	return got, nil
}

// GetLink calls GET /api/links/{key}.
func (c *Client) GetLink(ctx context.Context, key string) (*Link, error) {
	var out Link
	if err := c.doJSON(ctx, http.MethodGet, "/api/links/"+url.PathEscape(key), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// LinkQRSVG calls GET /api/links/{key}/qr.svg and returns the SVG bytes.
func (c *Client) LinkQRSVG(ctx context.Context, key string) ([]byte, error) {
	return c.do(ctx, http.MethodGet, "/api/links/"+url.PathEscape(key)+"/qr.svg", nil, nil)
}

// Bucket is one value/count pair in a rollup breakdown.
type Bucket struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// DayBucket is one UTC day's click count; Date is "YYYY-MM-DD".
type DayBucket struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

// LinkBucket is one link's click total. Key is "" when the link was deleted.
type LinkBucket struct {
	LinkID *int64 `json:"link_id"`
	Key    string `json:"key"`
	Title  string `json:"title"`
	Count  int64  `json:"count"`
}

// LinkSeries is one link's daily clicks. ShortLinks names at most six links
// and folds the remainder into one IsOther series.
type LinkSeries struct {
	LinkID  *int64      `json:"link_id"`
	Key     string      `json:"key"`
	Title   string      `json:"title"`
	IsOther bool        `json:"is_other"`
	Days    []DayBucket `json:"days"`
}

// CampaignRollup is GET /api/campaigns/{slug}/stats. ShortLinks embeds its
// stats struct in the response, so these fields are top-level. Every count
// excludes bot clicks; the window is half-open, [WindowFrom, WindowTo).
type CampaignRollup struct {
	ClickCount       int64    `json:"click_count"`
	ExcludedBotCount int64    `json:"excluded_bot_count"`
	BySource         []Bucket `json:"by_source"`
	ByMedium         []Bucket `json:"by_medium"`
	ByContent        []Bucket `json:"by_content"`
	ByReferer        []Bucket `json:"by_referer"`
	WindowFrom       string   `json:"window_from"`
	WindowTo         string   `json:"window_to"`
	Timeseries       struct {
		Days []DayBucket `json:"days"`
	} `json:"timeseries"`
	ByLink       []LinkBucket `json:"by_link"`
	SeriesByLink []LinkSeries `json:"series_by_link"`
}

// CampaignStats calls GET /api/campaigns/{slug}/stats. from and to are
// optional ("YYYY-MM-DD", UTC); a zero time leaves that bound to ShortLinks'
// default window, which is the campaign's own start-to-end span.
func (c *Client) CampaignStats(ctx context.Context, slug string, from, to time.Time) (*CampaignRollup, error) {
	q := url.Values{}
	if !from.IsZero() {
		q.Set("from", from.UTC().Format("2006-01-02"))
	}
	if !to.IsZero() {
		q.Set("to", to.UTC().Format("2006-01-02"))
	}
	var out CampaignRollup
	if err := c.doJSON(ctx, http.MethodGet, "/api/campaigns/"+url.PathEscape(slug)+"/stats", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return fmt.Errorf("shortlinks: encode request: %w", err)
		}
	}
	b, err := c.do(ctx, method, path, q, payload)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, out); err != nil {
		return fmt.Errorf("shortlinks: decode response: %w", err)
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, payload []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrUnavailable
	}
	u := *c.base
	u.Path += path
	u.RawQuery = q.Encode()
	var rd io.Reader
	if payload != nil {
		rd = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
	if err != nil {
		return nil, fmt.Errorf("shortlinks: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json, image/svg+xml")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// url.Error text holds the URL (no secrets: the token is only in a
		// header), but rebuild it anyway so nothing else can leak in.
		var ue *url.Error
		if errors.As(err, &ue) {
			return nil, fmt.Errorf("shortlinks: %s %s: %w", method, path, ue.Err)
		}
		return nil, fmt.Errorf("shortlinks: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("shortlinks: read response: %w", err)
	}
	if len(b) > maxResponseBytes {
		return nil, fmt.Errorf("shortlinks: response exceeds %d bytes", maxResponseBytes)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return b, nil
	}
	return nil, decodeError(resp, b, c.token)
}

func decodeError(resp *http.Response, body []byte, token string) *APIError {
	e := &APIError{Status: resp.StatusCode}
	var eb struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &eb) == nil {
		e.Message = eb.Error
		if token != "" {
			// A server (or proxy) could echo the Authorization header into
			// its error text; never let the token reach err.Error().
			e.Message = strings.ReplaceAll(e.Message, token, "[redacted]")
		}
	}
	switch resp.StatusCode {
	case http.StatusUnauthorized:
		e.kind = ErrUnauthenticated
	case http.StatusForbidden:
		e.kind = ErrForbidden
	case http.StatusNotFound:
		e.kind = ErrNotFound
	case http.StatusConflict:
		e.kind = ErrConflict
	case http.StatusTooManyRequests:
		e.kind = ErrRateLimited
		if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s >= 0 {
			e.RetryAfter = time.Duration(s) * time.Second
		}
	}
	return e
}
