package handlers

import (
	"context"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

// outreachResultsOperations are the /api/v1 outreach reports (#0698): the
// results, the timeline and the Luma check. Each runs the same transport-free
// method as its /admin route, then gives every time a Pacific offset; the
// admin bodies stay UTC.
func outreachResultsOperations(h *AdminOutreachHandler) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "get_outreach_results", Method: http.MethodGet, Path: "/api/v1/events/{id}/outreach/results",
			Scope:   apitokens.ScopeOutreachRead,
			Summary: "An event's outreach results: clicks per post and channel from ShortLinks, and each post's latest likes, reposts, replies and quotes.",
			Description: "status says whether the clicks are real: ok, not_configured, not_started, campaign_missing, rejected or unreachable. " +
				"A ShortLinks failure never fails the call; the posts, engagement and mentions come from the site's own database. Times have a Pacific offset.",
			MayBeUnavailable: true,
		}, func(ctx context.Context, in outreachIDIn) (outreachResultsView, error) {
			v, err := h.resultsFor(ctx, in.ID)
			if err != nil {
				return outreachResultsView{}, outreachAPIError(err)
			}
			return resultsInPacific(v), nil
		}),
		api.Define(api.Operation{
			Name: "outreach_results_summary", Method: http.MethodGet, Path: "/api/v1/outreach/results",
			Scope:   apitokens.ScopeOutreachRead,
			Summary: "One results row per event with outreach: posts, clicks and reactions, plus the recent mentions.",
			Description: "status is ok, or why clicks are null (after the first rejected or unreachable answer ShortLinks is not asked again). " +
				"Times have a Pacific offset.",
			MayBeUnavailable: true,
		}, func(ctx context.Context, _ struct{}) (resultsSummaryView, error) {
			v, err := h.resultsSummary(ctx)
			if err != nil {
				return resultsSummaryView{}, err
			}
			for i := range v.Rows {
				v.Rows[i].StartsAt = pacificRFC3339Ptr(v.Rows[i].StartsAt)
			}
			mentionsInPacific(v.Mentions)
			return v, nil
		}),
		api.Define(api.Operation{
			Name: "outreach_timeline", Method: http.MethodGet, Path: "/api/v1/outreach/timeline",
			Scope:       apitokens.ScopeOutreachRead,
			Summary:     "Published events starting in the next 30 days, with their outreach and posts.",
			Description: "Makes no network call. outreach is null for an event whose outreach has not started. Times have a Pacific offset.",
		}, func(ctx context.Context, _ struct{}) (outreachTimelineView, error) {
			v, err := h.timeline(ctx)
			if err != nil {
				return outreachTimelineView{}, err
			}
			v.Now = pacificRFC3339(v.Now)
			for i := range v.Events {
				ev := &v.Events[i]
				p := pacificPanel(outreachPanelView{Workshop: ev.Workshop, Outreach: ev.Outreach, Waves: ev.Waves, Posts: ev.Posts})
				ev.Workshop, ev.Outreach, ev.Waves, ev.Posts = p.Workshop, p.Outreach, p.Waves, p.Posts
			}
			return v, nil
		}),
		api.Define(api.Operation{
			Name: "outreach_luma", Method: http.MethodGet, Path: "/api/v1/outreach/luma",
			Scope:   apitokens.ScopeOutreachRead,
			Summary: "Each upcoming published event against the Luma calendar: on Luma, differs (with the fields), or not on Luma; and Luma events that match none.",
			Description: "status is off, ok or a failure; on a failure events and leftovers are empty and last_good_at says when the feed last read. " +
				"Times have a Pacific offset.",
			MayBeUnavailable: true,
		}, func(ctx context.Context, _ struct{}) (outreachLumaView, error) {
			v, err := h.lumaReport(ctx)
			if err != nil {
				return outreachLumaView{}, err
			}
			v.ReadAt = pacificRFC3339Ptr(v.ReadAt)
			v.LastGoodAt = pacificRFC3339Ptr(v.LastGoodAt)
			for i := range v.Leftovers {
				v.Leftovers[i].StartsAt = pacificRFC3339(v.Leftovers[i].StartsAt)
			}
			return v, nil
		}),
	}
}

// resultsInPacific gives a results view's times a Pacific offset. v is built
// fresh for each call, so it is changed in place.
func resultsInPacific(v outreachResultsView) outreachResultsView {
	v.Workshop.StartsAt = pacificRFC3339Ptr(v.Workshop.StartsAt)
	v.FetchedAt = pacificRFC3339Ptr(v.FetchedAt)
	for i := range v.Posts {
		v.Posts[i].PostedAt = pacificRFC3339Ptr(v.Posts[i].PostedAt)
		if e := v.Posts[i].Engagement; e != nil {
			e.CapturedAt = pacificRFC3339(e.CapturedAt)
		}
	}
	for i := range v.FlyerLocations {
		v.FlyerLocations[i].PutUpAt = pacificRFC3339Ptr(v.FlyerLocations[i].PutUpAt)
	}
	mentionsInPacific(v.Mentions)
	return v
}

func mentionsInPacific(ms []mentionView) {
	for i := range ms {
		ms[i].CreatedAt = pacificRFC3339(ms[i].CreatedAt)
	}
}
