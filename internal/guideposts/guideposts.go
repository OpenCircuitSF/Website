// Package guideposts schedules and sends social posts that link a topic
// guide (/guides/{slug}) to Bluesky and Mastodon (#0658, plan #0657). It is
// separate from workshop Outreach (internal/outreach) but reuses its channel
// list, length rules, platform clients and the outbound queue. Each post gets
// its own tracked ShortLinks link, minted while it is a draft (Linker), so
// the publisher never calls ShortLinks.
package guideposts

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// CampaignSlug is the ShortLinks campaign a draft's link is minted into when
// none is given. A later repost may name a new campaign so its clicks are
// counted apart.
const CampaignSlug = "guide-posts"

// LinkMeasuredAs stands in for the short link while a draft still holds the
// link token: a 12-character key, longer than ShortLinks' real 6, so a draft
// that passes here still fits once the real link is in. The v1 API reports it
// (list_channels, #0676) so a tool can size a post before creating it.
const LinkMeasuredAs = "https://go.opencircuitsf.com/u/XXXXXXXXXXXX"

// sampleShortURL is the package's own name for LinkMeasuredAs.
const sampleShortURL = LinkMeasuredAs

var campaignSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

var (
	ErrNotFound        = errors.New("guideposts: post not found")
	ErrUnknownGuide    = errors.New("guideposts: not a topic guide")
	ErrPostLocked      = errors.New("guideposts: post is already posted")
	ErrPostInFlight    = errors.New("guideposts: post is being sent")
	ErrWrongStatus     = errors.New("guideposts: post is not in a state that allows this")
	ErrChannelUnknown  = errors.New("guideposts: unknown or manual channel")
	ErrNoLink          = errors.New("guideposts: post has no short link yet")
	ErrForeignLink     = errors.New("guideposts: ShortLinks returned a link for another destination")
	ErrBadCampaignSlug = errors.New("guideposts: campaign slug may use only lowercase letters, digits and single hyphens")
	ErrLinkRemoved     = errors.New("guideposts: the post must keep its short link")
	ErrLinkFixed       = errors.New("guideposts: the campaign is fixed once the post has its short link")
)

// BodyError is a post body that fails validation (too long, empty); the
// admin API answers it with 422 and its message.
type BodyError struct{ Err error }

func (e *BodyError) Error() string { return e.Err.Error() }
func (e *BodyError) Unwrap() error { return e.Err }

// maxClickDays bounds a campaign's stats window.
const maxClickDays = 366

// SuggestedCampaign is the campaign slug the admin form prefills: guides-
// plus the Pacific year and month of now.
func SuggestedCampaign(now time.Time) string {
	return "guides-" + outreach.PacificDate(now)[:7]
}

// ClickWindow is the stats window for a campaign whose first post was made at
// since: UTC midnight of since through UTC midnight after now, at most
// maxClickDays long.
func ClickWindow(since, now time.Time) (from, to time.Time) {
	s, n := since.UTC(), now.UTC()
	from = time.Date(s.Year(), s.Month(), s.Day(), 0, 0, 0, 0, time.UTC)
	to = time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
	if to.Sub(from) > maxClickDays*24*time.Hour {
		from = to.AddDate(0, 0, -maxClickDays)
	}
	return from, to
}

// PostRef is post id's utm_content value, gp{id}.
func PostRef(id int64) string { return "gp" + strconv.FormatInt(id, 10) }

// containsURL reports whether text holds u followed by end of text or a byte
// that cannot continue a short-link key, so a longer key starting with u
// does not count.
func containsURL(text, u string) bool {
	if u == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(text[i:], u)
		if j < 0 {
			return false
		}
		end := i + j + len(u)
		if end == len(text) || !keyByte(text[end]) {
			return true
		}
		i += j + 1
	}
}

func keyByte(b byte) bool {
	return b == '_' || b == '-' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// Post is one guide_posts row.
type Post struct {
	ID                int64      `json:"id"`
	GuideSlug         string     `json:"guide_slug"`
	Channel           string     `json:"channel"`
	Body              string     `json:"body"`
	DestinationURL    string     `json:"destination_url"`
	CampaignSlug      string     `json:"campaign_slug"`
	ShortLinkKey      *string    `json:"short_link_key"`
	ShortURL          *string    `json:"short_url"`
	Status            string     `json:"status"`
	ScheduledAt       *time.Time `json:"scheduled_at"`
	PostedAt          *time.Time `json:"posted_at"`
	RemoteID          *string    `json:"remote_id"`
	RemoteURL         *string    `json:"remote_url"`
	LastError         *string    `json:"last_error"`
	AttemptGeneration int        `json:"attempt_generation"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         *time.Time `json:"updated_at"`
}

// NormalizeCampaignSlug returns slug, or CampaignSlug when it is blank, and
// ErrBadCampaignSlug when it is not ShortLinks-slug-safe.
func NormalizeCampaignSlug(slug string) (string, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return CampaignSlug, nil
	}
	if len(slug) > 100 || !campaignSlugPattern.MatchString(slug) {
		return "", ErrBadCampaignSlug
	}
	return slug, nil
}

// DestinationURL is where post id's short link points: the guide's page with
// UTM tags, utm_content=gp{id} making it unique per post. ShortLinks
// de-duplicates links on destination, so uniqueness here is what gives every
// post its own link and makes a retried mint return the same one.
func DestinationURL(base, slug string, ch outreach.Channel, id int64) string {
	q := url.Values{
		"utm_source":   {ch.UTMSource},
		"utm_medium":   {"social"},
		"utm_campaign": {"guides"},
		"utm_content":  {PostRef(id)},
	}
	return strings.TrimRight(base, "/") + "/guides/" + slug + "?" + q.Encode()
}

// DefaultBody is a new draft's text: the guide's post copy, then the link.
func DefaultBody(g guides.Guide) string {
	return g.Post + "\n\n" + outreach.LinkToken
}

// ValidateDraft checks body as it would be sent, measuring the link token as
// a short URL.
func ValidateDraft(ch outreach.Channel, body string) error {
	return outreach.ValidateForSend(ch, strings.ReplaceAll(body, outreach.LinkToken, sampleShortURL))
}

// MastodonIdempotencyKey is the Idempotency-Key for post id's attempt
// generation. Its prefix differs from Outreach's, so the two never collide.
func MastodonIdempotencyKey(id int64, generation int) string {
	k := "opencircuitsf-guide-post-" + strconv.FormatInt(id, 10)
	if generation > 0 {
		k += "-g" + strconv.Itoa(generation)
	}
	return k
}

// topic finds a postable guide: a topic guide, never a workshop guide.
func topic(slug string) (guides.Guide, bool) {
	g, ok := guides.Lookup(slug)
	return g, ok && g.Kind == guides.Topic
}

// automaticChannel resolves a channel the site posts to itself.
func automaticChannel(name string) (outreach.Channel, bool) {
	ch, ok := outreach.ChannelByName(name)
	return ch, ok && ch.Automatic
}
