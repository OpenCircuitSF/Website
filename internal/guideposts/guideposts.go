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

// sampleShortURL stands in for the short link while a draft still holds the
// link token: a 12-character key, longer than ShortLinks' real 6, so a draft
// that passes here still fits once the real link is in.
const sampleShortURL = "https://go.opencircuitsf.com/u/XXXXXXXXXXXX"

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
)

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
		"utm_content":  {"gp" + strconv.FormatInt(id, 10)},
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
