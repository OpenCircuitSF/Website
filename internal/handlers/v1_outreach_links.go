package handlers

import (
	"context"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

type draftEventOutreachIn struct {
	ID    int64    `json:"id" path:"id"`
	Waves []string `json:"waves,omitempty" jsonschema:"optional waves to draft besides announce: reminder, last-call"`
}

// outreachLinkOperations are the /api/v1 outreach drafting and link writes
// (#0700). Each runs the same transport-free method as its /admin route.
func outreachLinkOperations(h *AdminOutreachHandler) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "draft_event_outreach", Method: http.MethodPost, Path: "/api/v1/events/{id}/outreach/draft-all",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Draft a post for every switched-on channel in the announce wave, and in any other waves named.",
			Description: "Safe to repeat: a slot that already has a post is skipped and never changed. No ShortLinks call is made; a slot with a link opens prepared.",
		}, func(ctx context.Context, in draftEventOutreachIn) (draftAllReport, error) {
			rep, err := h.draftAll(ctx, "", nil, in.ID, in.Waves)
			if err != nil {
				return draftAllReport{}, outreachAPIError(err)
			}
			return rep, nil
		}),
		api.Define(api.Operation{
			Name: "prepare_outreach_post", Method: http.MethodPost, Path: "/api/v1/outreach-posts/{id}/prepare",
			Scope:            apitokens.ScopeOutreachWrite,
			Summary:          "Prepare an outreach post: make its tracked short link and put the URL in the body.",
			Description:      "Safe to repeat: an already prepared post is returned unchanged with no ShortLinks call. Times have a Pacific offset.",
			MayBeUnavailable: true,
		}, func(ctx context.Context, in outreachIDIn) (outreachPostView, error) {
			v, err := h.prepare(ctx, "", nil, in.ID)
			if err != nil {
				return outreachPostView{}, outreachAPIError(err)
			}
			return outreachPostInPacific(v), nil
		}),
		api.Define(api.Operation{
			Name: "ensure_outreach_hand_links", Method: http.MethodPost, Path: "/api/v1/events/{id}/outreach/hand-links",
			Scope:            apitokens.ScopeOutreachWrite,
			Summary:          "Make every missing short link for the hand-posted channels, in all waves.",
			Description:      "Safe to repeat. A ShortLinks failure for a slot is named in failed, not an error; the next call retries it. Nothing changes once the event has started.",
			MayBeUnavailable: true,
		}, func(ctx context.Context, in outreachIDIn) (outreachLinkReport, error) {
			rep, err := h.ensureHandLinks(ctx, "", nil, in.ID)
			if err != nil {
				return outreachLinkReport{}, outreachAPIError(err)
			}
			return rep, nil
		}),
	}
}
