package guideposts

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
)

// ShortLinker is the part of the ShortLinks client the Linker uses.
// *outreachconn.LiveShortLinks satisfies it.
type ShortLinker interface {
	Enabled() bool
	Me(ctx context.Context) (*shortlinks.Me, error)
	EnsureCampaign(ctx context.Context, slug string, req shortlinks.CreateCampaignRequest) (*shortlinks.Campaign, error)
	CreateLink(ctx context.Context, req shortlinks.CreateLinkRequest) (*shortlinks.Link, error)
}

// Linker mints a draft's tracked short link.
type Linker struct {
	store *Store
	sl    ShortLinker
}

// NewLinker builds a Linker.
func NewLinker(store *Store, sl ShortLinker) *Linker {
	return &Linker{store: store, sl: sl}
}

// Ensure gives post id its short link, in the post's own campaign, unless it
// already has one. It never runs inside a transaction. ShortLinks
// de-duplicates on the post's unique destination, so a retry after a lost
// write gets the same link back. On failure the draft is kept unlinked and
// the error returned (wrapping shortlinks.ErrUnavailable when ShortLinks is
// not configured).
func (l *Linker) Ensure(ctx context.Context, id int64) (Post, error) {
	p, err := l.store.Get(ctx, id)
	if err != nil {
		return Post{}, err
	}
	if p.ShortURL != nil {
		return p, nil
	}
	if l.sl == nil || !l.sl.Enabled() {
		return p, fmt.Errorf("guideposts: ShortLinks is not configured: %w", shortlinks.ErrUnavailable)
	}
	g, ok := topic(p.GuideSlug)
	if !ok {
		return p, ErrUnknownGuide
	}
	ch, _ := outreach.ChannelByName(p.Channel)
	me, err := l.sl.Me(ctx)
	if err != nil {
		return p, err
	}
	base := strings.TrimRight(me.BaseURL, "/")
	if base == "" {
		return p, errors.New("guideposts: ShortLinks reported no short-link host")
	}
	camp, err := l.sl.EnsureCampaign(ctx, p.CampaignSlug, shortlinks.CreateCampaignRequest{
		Description: "Topic guide social posts", DefaultUTMCampaign: "guides",
	})
	if err != nil {
		return p, err
	}
	sid := strconv.FormatInt(p.ID, 10)
	link, err := l.sl.CreateLink(ctx, shortlinks.CreateLinkRequest{
		DestinationURL: p.DestinationURL,
		Title:          g.Title + " — " + ch.Label + " guide post " + sid,
		CampaignSlug:   camp.Slug,
		UTMSource:      ch.UTMSource, UTMMedium: "social", UTMCampaign: "guides", UTMContent: "gp" + sid,
		Placement: ch.Name,
	})
	if err != nil {
		return p, err
	}
	if link.DestinationURL != p.DestinationURL {
		return p, ErrForeignLink
	}
	out, _, err := l.store.SetLink(ctx, p.ID, link.Key, shortlinks.ShortURL(base, link.Key), camp.Slug)
	return out, err
}
