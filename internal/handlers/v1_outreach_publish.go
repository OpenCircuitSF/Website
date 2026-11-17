package handlers

import (
	"context"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

type scheduleOutreachWaveIn struct {
	ID          int64  `json:"id" path:"id"`
	Wave        string `json:"wave" jsonschema:"the wave: announce, reminder or last-call"`
	ScheduledAt string `json:"scheduled_at" jsonschema:"RFC 3339 time with an offset, e.g. 2026-12-01T10:00:00-08:00; at least a minute and at most a year from now"`
}

type outreachWaveIn struct {
	ID   int64  `json:"id" path:"id"`
	Wave string `json:"wave" jsonschema:"the wave: announce, reminder or last-call"`
}

type markOutreachPostedIn struct {
	ID        int64   `json:"id" path:"id"`
	RemoteURL *string `json:"remote_url,omitempty" jsonschema:"optional absolute http(s) URL of the post made by hand"`
}

// sectionInPacific gives a section response's post times a Pacific offset.
func sectionInPacific(res sectionResponse) sectionResponse {
	for i := range res.Posts {
		res.Posts[i] = outreachPostInPacific(res.Posts[i])
	}
	return res
}

// outreachPublishOperations are the /api/v1 outreach scheduling and posting
// writes (#0701). Each runs the same transport-free method as its /admin
// route; tests use a fake publisher.
func outreachPublishOperations(h *AdminOutreachHandler) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "schedule_outreach_wave", Method: http.MethodPost, Path: "/api/v1/events/{id}/outreach/schedule",
			Scope:        apitokens.ScopeOutreachWrite,
			PublicEffect: "the wave's posts go out publicly on Bluesky and Mastodon at scheduled_at",
			Summary:      "Schedule every automatic post of one wave for the same time, all or nothing.",
			Description:  "Posts already posted or posting now, and channels not connected, are left alone and listed in left_alone. Every post scheduled needs its link. Times have a Pacific offset.",
		}, func(ctx context.Context, in scheduleOutreachWaveIn) (sectionResponse, error) {
			res, err := h.scheduleWave(ctx, "", nil, in.ID, in.Wave, in.ScheduledAt)
			if err != nil {
				return sectionResponse{}, outreachAPIError(err)
			}
			return sectionInPacific(res), nil
		}),
		api.Define(api.Operation{
			Name: "unschedule_outreach_wave", Method: http.MethodPost, Path: "/api/v1/events/{id}/outreach/unschedule",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Put every scheduled automatic post of one wave back to draft, all or nothing.",
			Description: "Includes a post on a channel since disconnected or switched off. 409 when nothing in the wave is scheduled. Times have a Pacific offset.",
		}, func(ctx context.Context, in outreachWaveIn) (sectionResponse, error) {
			res, err := h.unscheduleWave(ctx, "", nil, in.ID, in.Wave)
			if err != nil {
				return sectionResponse{}, outreachAPIError(err)
			}
			return sectionInPacific(res), nil
		}),
		api.Define(api.Operation{
			Name: "send_outreach_post", Method: http.MethodPost, Path: "/api/v1/outreach-posts/{id}/send",
			Scope:            apitokens.ScopeOutreachWrite,
			PublicEffect:     "posts publicly to Bluesky or Mastodon right away",
			Summary:          "Post a prepared automatic outreach post now, or retry a failed one.",
			Description:      "The post is queued for the publisher, so it reads queued until the network answers. 503 when the channel is not configured. Times have a Pacific offset.",
			Status:           http.StatusAccepted,
			MayBeUnavailable: true,
		}, func(ctx context.Context, in outreachIDIn) (outreachPostView, error) {
			v, err := h.sendNow(ctx, "", nil, in.ID)
			if err != nil {
				return outreachPostView{}, outreachAPIError(err)
			}
			return outreachPostInPacific(v), nil
		}),
		api.Define(api.Operation{
			Name: "mark_outreach_post_posted", Method: http.MethodPost, Path: "/api/v1/outreach-posts/{id}/posted",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Record that a prepared outreach post was posted by hand.",
			Description: "Nothing is posted by this call. Times have a Pacific offset.",
		}, func(ctx context.Context, in markOutreachPostedIn) (outreachPostView, error) {
			v, err := h.markPosted(ctx, "", nil, in.ID, in.RemoteURL)
			if err != nil {
				return outreachPostView{}, outreachAPIError(err)
			}
			return outreachPostInPacific(v), nil
		}),
		api.Define(api.Operation{
			Name: "unmark_outreach_post_posted", Method: http.MethodPost, Path: "/api/v1/outreach-posts/{id}/unmark",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Take back a mark-posted on a hand-posted channel.",
			Description: "A post the site sent itself is refused; use takedown_outreach_post. Times have a Pacific offset.",
		}, func(ctx context.Context, in outreachIDIn) (outreachPostView, error) {
			v, err := h.unmarkPosted(ctx, "", nil, in.ID)
			if err != nil {
				return outreachPostView{}, outreachAPIError(err)
			}
			return outreachPostInPacific(v), nil
		}),
		api.Define(api.Operation{
			Name: "takedown_outreach_post", Method: http.MethodPost, Path: "/api/v1/outreach-posts/{id}/takedown",
			Scope:            apitokens.ScopeOutreachWrite,
			Destructive:      true,
			PublicEffect:     "deletes the post from Bluesky or Mastodon right away",
			Summary:          "Delete a post the site sent from its network and put it back to draft.",
			Description:      "A post already gone from the network counts as deleted. If the network refuses, the post stays posted and the call fails. Safe to retry. Times have a Pacific offset.",
			MayBeUnavailable: true,
		}, func(ctx context.Context, in outreachIDIn) (outreachPostView, error) {
			v, err := h.takedown(ctx, "", nil, in.ID)
			if err != nil {
				return outreachPostView{}, outreachAPIError(err)
			}
			return outreachPostInPacific(v), nil
		}),
	}
}
