package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
	"github.com/brennanMKE/OpenCircuitSF/internal/guideposts"
	"github.com/brennanMKE/OpenCircuitSF/internal/guides"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// guidePostChannelNames are the channels a guide post can go to, in order.
var guidePostChannelNames = []string{"bluesky", "mastodon"}

// guidePostStatuses are guide_posts_status_check's values.
var guidePostStatuses = []string{"draft", "scheduled", "queued", "posted", "failed"}

// guideOperations are the twelve /api/v1 guide and guide-post operations
// (#0676). Building them never touches h: the closures read it only when
// called, so a parity test may pass nil. Every write goes through h's
// transport-free methods, the same code the /admin/guide-posts routes run,
// and is audited against the calling token (recordCtx).
func guideOperations(baseURL string, h *AdminGuidePostsHandler) []api.Operation {
	ops := guideOps(baseURL, h)
	return append(ops, guidePostOps(h)...)
}

// guidePostAPIError maps a guide-post error to the v1 envelope, from the
// same table the admin routes use (guidePostErrInfo). An unrecognised error
// is returned as is, so api.AsError logs it and answers 500.
func guidePostAPIError(err error) error {
	if err == nil {
		return nil
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return err
	}
	var sl errShortLinks
	if errors.As(err, &sl) {
		return api.Unavailable("short link could not be made: " + linkFailureReason(sl.err))
	}
	status, field, msg := guidePostErrInfo(err)
	switch status {
	case http.StatusNotFound:
		return api.NotFound("guide post")
	case http.StatusConflict:
		return api.Conflict(msg)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		if field == "" {
			field = "body"
		}
		return api.Validation(map[string]string{field: msg})
	}
	return err
}

type guidePostIDIn struct {
	ID int64 `json:"id" path:"id"`
}

type listGuidePostsIn struct {
	api.Page
	Status   *string    `json:"status,omitempty" query:"status" jsonschema:"draft, scheduled, queued, posted or failed"`
	Slug     *string    `json:"slug,omitempty" query:"slug" jsonschema:"only posts for this guide"`
	Channel  *string    `json:"channel,omitempty" query:"channel" jsonschema:"bluesky or mastodon"`
	Campaign *string    `json:"campaign,omitempty" query:"campaign" jsonschema:"only posts in this ShortLinks campaign"`
	From     *time.Time `json:"from,omitempty" query:"from" jsonschema:"only posts whose time (posted, else scheduled, else created) is at or after this"`
	To       *time.Time `json:"to,omitempty" query:"to" jsonschema:"only posts whose time is before this"`
}

// guidePostCursor is list_guide_posts' cursor: the last id returned.
type guidePostCursor struct {
	ID int64 `json:"id"`
}

type createGuidePostsIn struct {
	Slug         string   `json:"slug" jsonschema:"a topic guide slug (list_guides with kind=topic)"`
	Channels     []string `json:"channels" jsonschema:"bluesky, mastodon or both; one post is made per channel"`
	Body         string   `json:"body,omitempty" jsonschema:"post text; put {link} where the post's own short link goes. Blank uses the guide's default body"`
	Campaign     string   `json:"campaign,omitempty" jsonschema:"ShortLinks campaign slug (lowercase, digits, single hyphens); blank is guide-posts"`
	ScheduledFor *string  `json:"scheduled_for,omitempty" jsonschema:"RFC 3339 time, at least a minute and at most a year ahead; schedules every post that got its short link"`
}

type channelResultV1 struct {
	Channel       string           `json:"channel"`
	Post          *guideposts.Post `json:"post" jsonschema:"the created post; null when creating it failed"`
	LinkError     *string          `json:"link_error" jsonschema:"set when the post was kept as a draft without its short link"`
	ScheduleError *string          `json:"schedule_error" jsonschema:"set when scheduled_for was given but this post could not be scheduled"`
	Error         *api.ErrorDetail `json:"error" jsonschema:"set when this channel's post could not be created"`
}

type createGuidePostsOut struct {
	Results []channelResultV1 `json:"results"`
}

type updateGuidePostIn struct {
	ID          int64   `json:"id" path:"id"`
	Body        *string `json:"body,omitempty" jsonschema:"new text; {link} means the post's own short link"`
	Campaign    *string `json:"campaign,omitempty" jsonschema:"new ShortLinks campaign; only before the post has its short link"`
	ScheduledAt *string `json:"scheduled_at,omitempty" jsonschema:"RFC 3339 time to schedule the post for"`
}

type scheduleGuidePostIn struct {
	ID          int64  `json:"id" path:"id"`
	ScheduledAt string `json:"scheduled_at" jsonschema:"RFC 3339 time, at least a minute and at most a year ahead"`
}

type againGuidePostIn struct {
	ID       int64  `json:"id" path:"id"`
	Campaign string `json:"campaign,omitempty" jsonschema:"ShortLinks campaign for the copy; blank is guide-posts"`
}

type deletedV1 struct {
	ID      int64 `json:"id"`
	Deleted bool  `json:"deleted"`
}

type channelV1 struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	CharLimit  int    `json:"char_limit"`
	Configured bool   `json:"configured" jsonschema:"the account is connected, so posts can be created for it"`
	Counting   string `json:"counting" jsonschema:"how the limit is counted"`
	URLWeight  int    `json:"url_weight" jsonschema:"characters each URL counts as; 0 means its real length"`
	ByteLimit  int    `json:"byte_limit" jsonschema:"a UTF-8 byte cap on top of char_limit; 0 means none"`
}

type channelsV1 struct {
	Channels       []channelV1 `json:"channels"`
	LinkToken      string      `json:"link_token" jsonschema:"write this in a body where the post's short link goes"`
	LinkMeasuredAs string      `json:"link_measured_as" jsonschema:"a draft's link token is measured as this URL"`
}

func guidePostOps(h *AdminGuidePostsHandler) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "list_guide_posts", Method: http.MethodGet, Path: "/api/v1/guide-posts",
			Scope:       apitokens.ScopeGuidePostsRead,
			Summary:     "List guide posts, newest first, filtered by status, guide, channel, campaign or time.",
			Description: "Times are UTC. Clicks are not included; see get_guide and guide_posts_overview.",
		}, func(ctx context.Context, in listGuidePostsIn) (api.List[guideposts.Post], error) {
			return h.listGuidePostsV1(ctx, in)
		}),
		api.Define(api.Operation{
			Name: "create_guide_posts", Method: http.MethodPost, Path: "/api/v1/guide-posts",
			Scope:            apitokens.ScopeGuidePostsWrite,
			Summary:          "Create one draft per channel for a topic guide, each with its own tracked short link, and optionally schedule them all.",
			Description:      "Every field is checked first, including each channel's length limit, and a problem creates nothing. After that, one channel failing (a short link or schedule step) does not undo the other; see each result.",
			Idempotency:      true,
			Status:           http.StatusCreated,
			MayBeUnavailable: true,
			PublicEffect:     "with scheduled_for, the posts go out publicly on Bluesky and Mastodon at that time",
		}, func(ctx context.Context, in createGuidePostsIn) (createGuidePostsOut, error) {
			return h.createGuidePostsV1(ctx, "create_guide_posts", in)
		}),
		api.Define(api.Operation{
			Name: "update_guide_post", Method: http.MethodPatch, Path: "/api/v1/guide-posts/{id}",
			Scope:            apitokens.ScopeGuidePostsWrite,
			Summary:          "Change a draft or scheduled guide post's body, campaign or schedule.",
			Description:      "Steps run in the order campaign, body, schedule.",
			MayBeUnavailable: true,
			PublicEffect:     "a scheduled_at here makes the post go out publicly at that time",
		}, func(ctx context.Context, in updateGuidePostIn) (guideposts.Post, error) {
			return h.updateGuidePostV1(ctx, in)
		}),
		api.Define(api.Operation{
			Name: "schedule_guide_post", Method: http.MethodPost, Path: "/api/v1/guide-posts/{id}/schedule",
			Scope:            apitokens.ScopeGuidePostsWrite,
			Summary:          "Schedule a guide post to go out at a time; a draft gets its short link first.",
			MayBeUnavailable: true,
			PublicEffect:     "posts publicly to Bluesky or Mastodon at scheduled_at",
		}, func(ctx context.Context, in scheduleGuidePostIn) (guideposts.Post, error) {
			at, reason := parseScheduleAt(in.ScheduledAt, h.now())
			if reason != "" {
				return guideposts.Post{}, api.Validation(map[string]string{"scheduled_at": reason})
			}
			p, err := h.schedule(ctx, "", nil, in.ID, at)
			return p, guidePostAPIError(err)
		}),
		api.Define(api.Operation{
			Name: "unschedule_guide_post", Method: http.MethodPost, Path: "/api/v1/guide-posts/{id}/unschedule",
			Scope:   apitokens.ScopeGuidePostsWrite,
			Summary: "Return a scheduled guide post to draft.",
		}, func(ctx context.Context, in guidePostIDIn) (guideposts.Post, error) {
			p, err := h.unschedule(ctx, "", nil, in.ID)
			return p, guidePostAPIError(err)
		}),
		api.Define(api.Operation{
			Name: "send_guide_post", Method: http.MethodPost, Path: "/api/v1/guide-posts/{id}/send",
			Scope:            apitokens.ScopeGuidePostsWrite,
			Summary:          "Send a guide post now, or retry a failed one; the publisher posts it shortly.",
			Status:           http.StatusAccepted,
			MayBeUnavailable: true,
			PublicEffect:     "posts publicly to Bluesky or Mastodon right away",
		}, func(ctx context.Context, in guidePostIDIn) (guideposts.Post, error) {
			p, err := h.queueNow(ctx, "", nil, in.ID)
			return p, guidePostAPIError(err)
		}),
		api.Define(api.Operation{
			Name: "post_guide_again", Method: http.MethodPost, Path: "/api/v1/guide-posts/{id}/again",
			Scope:   apitokens.ScopeGuidePostsWrite,
			Summary: "Copy a posted guide post into a new draft with its own short link, optionally in a new campaign.",
			Status:  http.StatusCreated,
		}, func(ctx context.Context, in againGuidePostIn) (guidePostLinkResponse, error) {
			resp, err := h.again(ctx, "", nil, in.ID, in.Campaign)
			return resp, guidePostAPIError(err)
		}),
		api.Define(api.Operation{
			Name: "delete_guide_post", Method: http.MethodDelete, Path: "/api/v1/guide-posts/{id}",
			Scope:       apitokens.ScopeGuidePostsWrite,
			Summary:     "Delete a draft guide post.",
			Description: "Only drafts; unschedule a scheduled post first. Its short link, if any, stays in ShortLinks.",
			Destructive: true,
		}, func(ctx context.Context, in guidePostIDIn) (deletedV1, error) {
			if err := h.deletePost(ctx, "", nil, in.ID, true); err != nil {
				return deletedV1{}, guidePostAPIError(err)
			}
			return deletedV1{ID: in.ID, Deleted: true}, nil
		}),
		api.Define(api.Operation{
			Name: "guide_posts_overview", Method: http.MethodGet, Path: "/api/v1/guide-posts/overview",
			Scope:   apitokens.ScopeGuidePostsRead,
			Summary: "One row per topic guide: when it was last shared, where, its campaigns, next scheduled post, clicks and signups.",
		}, func(ctx context.Context, _ struct{}) (guidePostsOverviewResponse, error) {
			resp, err := h.overview(ctx)
			return resp, guidePostAPIError(err)
		}),
		api.Define(api.Operation{
			Name: "list_channels", Method: http.MethodGet, Path: "/api/v1/channels",
			Scope:   apitokens.ScopeGuidePostsRead,
			Summary: "The channels guide posts go to: whether each is connected, its character limit and how URLs are counted, so a post can be sized before it is created.",
		}, func(_ context.Context, _ struct{}) (channelsV1, error) {
			return h.channelsV1(), nil
		}),
	}
}

func (h *AdminGuidePostsHandler) channelsV1() channelsV1 {
	out := channelsV1{Channels: []channelV1{}, LinkToken: outreach.LinkToken, LinkMeasuredAs: guideposts.LinkMeasuredAs}
	for _, name := range guidePostChannelNames {
		ch, _ := outreach.ChannelByName(name)
		v := channelV1{Name: ch.Name, Label: ch.Label, CharLimit: ch.CharLimit, Configured: h.configured(ch.Name)}
		if name == "bluesky" {
			v.Counting, v.ByteLimit = "graphemes", bluesky.MaxTextBytes
		} else {
			v.Counting, v.URLWeight = "code points; each URL counts as 23", 23
		}
		out.Channels = append(out.Channels, v)
	}
	return out
}

// guidePostTime is the time list_guide_posts' from/to bound: posted, else
// scheduled, else created.
func guidePostTime(p guideposts.Post) time.Time {
	switch {
	case p.PostedAt != nil:
		return *p.PostedAt
	case p.ScheduledAt != nil:
		return *p.ScheduledAt
	}
	return p.CreatedAt
}

func (h *AdminGuidePostsHandler) listGuidePostsV1(ctx context.Context, in listGuidePostsIn) (api.List[guideposts.Post], error) {
	bad := map[string]string{}
	if in.Status != nil && !slices.Contains(guidePostStatuses, *in.Status) {
		bad["status"] = "must be one of " + strings.Join(guidePostStatuses, ", ")
	}
	if in.Channel != nil && !slices.Contains(guidePostChannelNames, *in.Channel) {
		bad["channel"] = "must be one of " + strings.Join(guidePostChannelNames, ", ")
	}
	if in.From != nil && in.To != nil && !in.From.Before(*in.To) {
		bad["to"] = "must be after from"
	}
	if len(bad) > 0 {
		return api.List[guideposts.Post]{}, api.Validation(bad)
	}
	all, err := h.store.List(ctx)
	if err != nil {
		return api.List[guideposts.Post]{}, err
	}
	var items []guideposts.Post
	for _, p := range all {
		switch {
		case in.Status != nil && p.Status != *in.Status,
			in.Slug != nil && p.GuideSlug != *in.Slug,
			in.Channel != nil && p.Channel != *in.Channel,
			in.Campaign != nil && p.CampaignSlug != *in.Campaign,
			in.From != nil && guidePostTime(p).Before(*in.From),
			in.To != nil && !guidePostTime(p).Before(*in.To):
			continue
		}
		items = append(items, p)
	}
	slices.SortFunc(items, func(a, b guideposts.Post) int {
		switch {
		case a.ID > b.ID:
			return -1
		case a.ID < b.ID:
			return 1
		}
		return 0
	})
	return paginate(items, in.Page,
		func(p guideposts.Post) guidePostCursor { return guidePostCursor{ID: p.ID} },
		func(p guideposts.Post, c guidePostCursor) bool { return p.ID < c.ID })
}

// createGuidePostsV1 validates every field, then makes one draft per
// channel in request order. A failure after validation affects only its own
// channel. If no channel produced a post, the first channel's error is the
// answer.
func (h *AdminGuidePostsHandler) createGuidePostsV1(ctx context.Context, opName string, in createGuidePostsIn) (createGuidePostsOut, error) {
	bad := map[string]string{}
	g, isTopic := guides.Lookup(in.Slug)
	isTopic = isTopic && g.Kind == guides.Topic
	switch {
	case in.Slug == "":
		bad["slug"] = "is required"
	case !isTopic:
		bad["slug"] = "must be a topic guide slug"
	}
	var channels []string
	switch {
	case len(in.Channels) == 0:
		bad["channels"] = "must list bluesky, mastodon or both"
	case len(in.Channels) > len(guidePostChannelNames):
		bad["channels"] = "lists more than bluesky and mastodon"
	default:
		seen := map[string]bool{}
		for _, c := range in.Channels {
			switch {
			case !slices.Contains(guidePostChannelNames, c):
				bad["channels"] = fmt.Sprintf("%q is not a channel; use bluesky or mastodon", c)
			case seen[c]:
				bad["channels"] = c + " is listed twice"
			default:
				seen[c] = true
				channels = append(channels, c)
			}
		}
	}
	for _, c := range channels {
		ch, _ := outreach.ChannelByName(c)
		if !h.configured(c) {
			bad["channels."+c] = ch.Label + " is not connected"
		}
		if isTopic {
			body := in.Body
			if strings.TrimSpace(body) == "" {
				body = guideposts.DefaultBody(g)
			}
			if err := guideposts.ValidateDraft(ch, body); err != nil {
				bad["body."+c] = err.Error()
			}
		}
	}
	if _, err := guideposts.NormalizeCampaignSlug(in.Campaign); err != nil {
		_, _, msg := guidePostErrInfo(err)
		bad["campaign"] = msg
	}
	var at time.Time
	if in.ScheduledFor != nil {
		var reason string
		if at, reason = parseScheduleAt(*in.ScheduledFor, h.now()); reason != "" {
			bad["scheduled_for"] = reason
		}
	}
	if len(bad) > 0 {
		return createGuidePostsOut{}, api.Validation(bad)
	}

	out := createGuidePostsOut{Results: []channelResultV1{}}
	var firstErr error
	created := 0
	for _, c := range channels {
		res := channelResultV1{Channel: c}
		resp, err := h.createDraft(ctx, "", nil, in.Slug, c, in.Body, in.Campaign)
		if err != nil {
			mapped := guidePostAPIError(err)
			if firstErr == nil {
				firstErr = mapped
			}
			d := api.AsError(opName, mapped).Body().Error
			res.Error = &d
			out.Results = append(out.Results, res)
			continue
		}
		created++
		p := resp.Post
		res.Post, res.LinkError = &p, resp.LinkError
		if in.ScheduledFor != nil {
			if resp.Post.ShortURL == nil {
				msg := "the post has no short link yet"
				res.ScheduleError = &msg
			} else if sp, err := h.schedule(ctx, "", nil, p.ID, at); err != nil {
				msg := api.AsError(opName, guidePostAPIError(err)).Message
				res.ScheduleError = &msg
			} else {
				res.Post = &sp
			}
		}
		out.Results = append(out.Results, res)
	}
	if created == 0 && firstErr != nil {
		return createGuidePostsOut{}, firstErr
	}
	return out, nil
}

// updateCampaign changes an unlinked draft's campaign, audited as
// guide_post.updated.
func (h *AdminGuidePostsHandler) updateCampaign(ctx context.Context, ip string, actorID *int64, id int64, campaign string) (guideposts.Post, error) {
	p, err := h.store.UpdateCampaign(ctx, id, campaign)
	if err != nil {
		return guideposts.Post{}, err
	}
	h.recordCtx(ctx, ip, p.ID, audit.Entry{ActorID: actorID, Action: audit.ActionGuidePostUpdated, Metadata: map[string]any{"slug": p.GuideSlug, "channel": p.Channel, "campaign": p.CampaignSlug}})
	return p, nil
}

func (h *AdminGuidePostsHandler) updateGuidePostV1(ctx context.Context, in updateGuidePostIn) (guideposts.Post, error) {
	if in.Body == nil && in.Campaign == nil && in.ScheduledAt == nil {
		return guideposts.Post{}, api.Validation(map[string]string{"body": "send body, campaign or scheduled_at"})
	}
	var at time.Time
	if in.ScheduledAt != nil {
		var reason string
		if at, reason = parseScheduleAt(*in.ScheduledAt, h.now()); reason != "" {
			return guideposts.Post{}, api.Validation(map[string]string{"scheduled_at": reason})
		}
	}
	var (
		p   guideposts.Post
		err error
	)
	if in.Campaign != nil {
		if p, err = h.updateCampaign(ctx, "", nil, in.ID, *in.Campaign); err != nil {
			return guideposts.Post{}, guidePostAPIError(err)
		}
	}
	if in.Body != nil {
		if p, err = h.updateBody(ctx, "", nil, in.ID, *in.Body); err != nil {
			return guideposts.Post{}, guidePostAPIError(err)
		}
	}
	if in.ScheduledAt != nil {
		if p, err = h.schedule(ctx, "", nil, in.ID, at); err != nil {
			return guideposts.Post{}, guidePostAPIError(err)
		}
	}
	return p, nil
}
