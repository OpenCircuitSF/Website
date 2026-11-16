package handlers

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/guideposts"
	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// guideV1 is one catalog guide over /api/v1 (#0676).
type guideV1 struct {
	Slug        string   `json:"slug"`
	Kind        string   `json:"kind" jsonschema:"workshop or topic"`
	Set         string   `json:"set" jsonschema:"the guide set id, e.g. esp32"`
	Title       string   `json:"title"`
	Hook        string   `json:"hook"`
	Description string   `json:"description"`
	Minutes     int      `json:"minutes"`
	Tags        []string `json:"tags"`
	URL         string   `json:"url"`
	Postable    bool     `json:"postable" jsonschema:"true for topic guides, which create_guide_posts accepts"`
	Post        string   `json:"post,omitempty" jsonschema:"suggested post copy without the link; topic guides only"`
	DefaultBody string   `json:"default_body,omitempty" jsonschema:"the body a draft starts from: post copy then the {link} token; topic guides only"`
}

func toGuideV1(baseURL string, g guides.Guide) guideV1 {
	tags := g.Tags
	if tags == nil {
		tags = []string{}
	}
	v := guideV1{
		Slug: g.Slug, Kind: g.Kind.String(), Set: g.Set, Title: g.Title, Hook: g.Hook,
		Description: g.Description, Minutes: g.Minutes, Tags: tags,
		URL: strings.TrimRight(baseURL, "/") + g.Path(), Postable: g.Kind == guides.Topic,
	}
	if g.Kind == guides.Topic {
		v.Post = g.Post
		v.DefaultBody = guideposts.DefaultBody(g)
	}
	return v
}

type listGuidesIn struct {
	api.Page
	Set  *string `json:"set,omitempty" query:"set" jsonschema:"only guides in this set (see the set field)"`
	Kind *string `json:"kind,omitempty" query:"kind" jsonschema:"workshop or topic"`
}

type getGuideIn struct {
	Slug string `json:"slug" path:"slug"`
}

// guideCursor is list_guides' cursor: the last slug returned.
type guideCursor struct {
	Slug string `json:"slug"`
}

// guideStatusCounts counts one channel's posts by status.
type guideStatusCounts struct {
	Draft     int `json:"draft"`
	Scheduled int `json:"scheduled"`
	Queued    int `json:"queued"`
	Posted    int `json:"posted"`
	Failed    int `json:"failed"`
}

// guideHistoryV1 is a topic guide's post history.
type guideHistoryV1 struct {
	LastPostedAt    *time.Time                   `json:"last_posted_at"`
	LastCampaign    string                       `json:"last_campaign"`
	PostedCount     int                          `json:"posted_count"`
	NextScheduledAt *time.Time                   `json:"next_scheduled_at"`
	Clicks          *int64                       `json:"clicks" jsonschema:"total short-link clicks; null when ShortLinks could not answer"`
	PostsByChannel  map[string]guideStatusCounts `json:"posts_by_channel"`
	Signups         *guideSignupCounts           `json:"signups" jsonschema:"subscribe requests and confirmations attributed to the guide; null when unavailable"`
	ClicksError     *string                      `json:"clicks_error"`
	SignupsError    *string                      `json:"signups_error"`
}

type guideDetailV1 struct {
	guideV1
	History *guideHistoryV1 `json:"history" jsonschema:"post history; null for a workshop guide"`
}

func guideKindNames() []string { return []string{guides.Workshop.String(), guides.Topic.String()} }

func guideSetIDs() []string {
	var ids []string
	for _, s := range guides.Sets() {
		ids = append(ids, s.ID)
	}
	return ids
}

func guideOps(baseURL string, h *AdminGuidePostsHandler) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "list_guides", Method: http.MethodGet, Path: "/api/v1/guides",
			Scope:       apitokens.ScopeGuidesRead,
			Summary:     "List the site's workshop and topic guides, with each topic guide's suggested post copy and URL.",
			Description: "Catalog order. Filter with set and kind.",
		}, func(_ context.Context, in listGuidesIn) (api.List[guideV1], error) {
			return listGuides(baseURL, in)
		}),
		api.Define(api.Operation{
			Name: "get_guide", Method: http.MethodGet, Path: "/api/v1/guides/{slug}",
			Scope:   apitokens.ScopeGuidesRead,
			Summary: "Get one guide, plus its post history: last posted, posts per channel, clicks and signups.",
		}, func(ctx context.Context, in getGuideIn) (guideDetailV1, error) {
			return h.getGuideV1(ctx, baseURL, in.Slug)
		}),
	}
}

func listGuides(baseURL string, in listGuidesIn) (api.List[guideV1], error) {
	bad := map[string]string{}
	if in.Kind != nil && !slices.Contains(guideKindNames(), *in.Kind) {
		bad["kind"] = "must be one of " + strings.Join(guideKindNames(), ", ")
	}
	if in.Set != nil && !slices.Contains(guideSetIDs(), *in.Set) {
		bad["set"] = "must be one of " + strings.Join(guideSetIDs(), ", ")
	}
	if len(bad) > 0 {
		return api.List[guideV1]{}, api.Validation(bad)
	}
	all := guides.All()
	pos := map[string]int{}
	var items []guideV1
	for i, g := range all {
		pos[g.Slug] = i
		if in.Kind != nil && g.Kind.String() != *in.Kind {
			continue
		}
		if in.Set != nil && g.Set != *in.Set {
			continue
		}
		items = append(items, toGuideV1(baseURL, g))
	}
	if in.Cursor != "" {
		var c guideCursor
		if err := api.DecodeCursor(in.Cursor, &c); err != nil {
			return api.List[guideV1]{}, err
		}
		if _, ok := pos[c.Slug]; !ok {
			return api.List[guideV1]{}, api.Validation(map[string]string{"cursor": "names a guide that no longer exists; start again without a cursor"})
		}
	}
	return paginate(items, in.Page,
		func(g guideV1) guideCursor { return guideCursor{Slug: g.Slug} },
		func(g guideV1, c guideCursor) bool { return pos[g.Slug] > pos[c.Slug] })
}

// getGuideV1 is get_guide: the catalog entry, and for a topic guide its post
// history built from the same reads the admin overview uses.
func (h *AdminGuidePostsHandler) getGuideV1(ctx context.Context, baseURL, slug string) (guideDetailV1, error) {
	g, ok := guides.Lookup(slug)
	if !ok {
		return guideDetailV1{}, api.NotFound("guide")
	}
	out := guideDetailV1{guideV1: toGuideV1(baseURL, g)}
	if g.Kind != guides.Topic {
		return out, nil
	}
	all, err := h.store.List(ctx)
	if err != nil {
		return guideDetailV1{}, err
	}
	var posts []guideposts.Post
	byChannel := map[string]guideStatusCounts{"bluesky": {}, "mastodon": {}}
	for _, p := range all {
		if p.GuideSlug != slug {
			continue
		}
		posts = append(posts, p)
		c := byChannel[p.Channel]
		switch p.Status {
		case "draft":
			c.Draft++
		case "scheduled":
			c.Scheduled++
		case "queued":
			c.Queued++
		case "posted":
			c.Posted++
		case "failed":
			c.Failed++
		}
		byChannel[p.Channel] = c
	}
	clicks, known, clicksErr, err := h.clicks(ctx)
	if err != nil {
		return guideDetailV1{}, err
	}
	share := guideposts.Overview([]guides.Guide{g}, posts, h.baseURL, clicks, known)[0]
	hist := &guideHistoryV1{
		LastPostedAt: share.LastPostedAt, LastCampaign: share.LastCampaign, PostedCount: share.PostedCount,
		NextScheduledAt: share.NextScheduledAt, Clicks: share.Clicks, PostsByChannel: byChannel,
		ClicksError: clicksErr,
	}
	rows, signupsErr := h.signupRows(ctx, posts)
	hist.SignupsError = signupsErr
	if rows != nil {
		hist.Signups = sumSignups(rows, func(s subscribers.GuideSignups) bool { return s.Guide == slug })
	}
	out.History = hist
	return out, nil
}
