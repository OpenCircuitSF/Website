package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

// outreachAPIError maps an outreach write's error to the v1 envelope, the
// way guidePostAPIError does for guide posts (#0699). An unrecognised error
// is returned as is, so api.AsError logs it and answers 500.
func outreachAPIError(err error) error {
	if err == nil {
		return nil
	}
	var ae *api.Error
	if errors.As(err, &ae) {
		return err
	}
	var le outreachLinksError
	if errors.As(err, &le) {
		return api.Unavailable("ShortLinks request failed: " + linkFailureReason(le.err))
	}
	var oe *outreachOpError
	if !errors.As(err, &oe) {
		return err
	}
	switch oe.status {
	case http.StatusNotFound:
		return api.NotFound(notFoundWhat(oe.msg))
	case http.StatusConflict:
		return api.Conflict(oe.msg)
	case http.StatusServiceUnavailable, http.StatusBadGateway:
		return api.Unavailable(oe.msg)
	case http.StatusBadRequest:
		field := oe.field
		if field == "" {
			field = "body"
		}
		return api.Validation(map[string]string{field: oe.msg})
	}
	return err
}

// notFoundWhat names the missing thing from an admin not-found message.
func notFoundWhat(msg string) string {
	switch msg {
	case "event not found":
		return "event"
	case "post not found":
		return "outreach post"
	case "location not found":
		return "outreach location"
	}
	return "outreach"
}

type outreachIDIn struct {
	ID int64 `json:"id" path:"id"`
}

type createOutreachPostIn struct {
	ID      int64  `json:"id" path:"id" jsonschema:"the outreach id"`
	Channel string `json:"channel" jsonschema:"a channel name, e.g. bluesky, mastodon"`
	Wave    string `json:"wave" jsonschema:"a wave name"`
	Body    string `json:"body,omitempty" jsonschema:"the post text; omitted, it is drafted from the channel template"`
}

type updateOutreachPostIn struct {
	ID         int64   `json:"id" path:"id"`
	Body       *string `json:"body,omitempty" jsonschema:"new post text; send this or regenerate, not both"`
	Regenerate *bool   `json:"regenerate,omitempty" jsonschema:"true redrafts the body from the template with the post's link"`
	Customized *bool   `json:"customized,omitempty" jsonschema:"with body: mark a hand edit so a shared-message save skips this post"`
}

// outreachPostInPacific gives a post view's times a Pacific offset.
func outreachPostInPacific(v outreachPostView) outreachPostView {
	v.ScheduledAt = pacificRFC3339Ptr(v.ScheduledAt)
	v.PostedAt = pacificRFC3339Ptr(v.PostedAt)
	v.UpdatedAt = pacificRFC3339Ptr(v.UpdatedAt)
	v.CreatedAt = pacificRFC3339(v.CreatedAt)
	return v
}

// outreachWriteOperations are the /api/v1 outreach writes (#0699). Each
// runs the same transport-free method as its /admin route, with no session
// actor: recordCtx attributes the audit row to the API token.
func outreachWriteOperations(h *AdminOutreachHandler) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "start_event_outreach", Method: http.MethodPost, Path: "/api/v1/events/{id}/outreach",
			Scope:            apitokens.ScopeOutreachWrite,
			Summary:          "Start outreach for a published event: make its ShortLinks campaign and tracked links.",
			Description:      "Safe to repeat: when outreach already exists it is returned unchanged. link_report is set only on the call that starts it.",
			MayBeUnavailable: true,
		}, func(ctx context.Context, in outreachIDIn) (eventOutreachV1, error) {
			v, _, err := h.startOutreach(ctx, "", nil, in.ID)
			if err != nil {
				return eventOutreachV1{}, outreachAPIError(err)
			}
			return eventOutreachV1{Started: true, outreachPanelView: pacificPanel(v)}, nil
		}),
		api.Define(api.Operation{
			Name: "create_outreach_post", Method: http.MethodPost, Path: "/api/v1/outreach/{id}/posts",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Create a draft outreach post for one channel and wave.",
			Description: "One post per channel and wave; a second is a conflict. Times have a Pacific offset.",
			Idempotency: true,
			Status:      http.StatusCreated,
		}, func(ctx context.Context, in createOutreachPostIn) (outreachPostView, error) {
			v, err := h.createPost(ctx, "", nil, in.ID, createOutreachPostRequest{Channel: in.Channel, Wave: in.Wave, Body: in.Body})
			if err != nil {
				return outreachPostView{}, outreachAPIError(err)
			}
			return outreachPostInPacific(v), nil
		}),
		api.Define(api.Operation{
			Name: "update_outreach_post", Method: http.MethodPatch, Path: "/api/v1/outreach-posts/{id}",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Change an outreach post's body, or regenerate it from the template.",
			Description: "Send exactly one of body or regenerate. A posted or sending post cannot change.",
		}, func(ctx context.Context, in updateOutreachPostIn) (outreachPostView, error) {
			v, err := h.updatePost(ctx, "", nil, in.ID, patchOutreachPostRequest{Body: in.Body, Regenerate: in.Regenerate, Customized: in.Customized})
			if err != nil {
				return outreachPostView{}, outreachAPIError(err)
			}
			return outreachPostInPacific(v), nil
		}),
		api.Define(api.Operation{
			Name: "delete_outreach_post", Method: http.MethodDelete, Path: "/api/v1/outreach-posts/{id}",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Delete an outreach post that has not been posted.",
			Description: "A posted or sending post cannot be deleted. Its short link stays in ShortLinks.",
			Destructive: true,
		}, func(ctx context.Context, in outreachIDIn) (deletedV1, error) {
			if err := h.deletePost(ctx, "", nil, in.ID); err != nil {
				return deletedV1{}, outreachAPIError(err)
			}
			return deletedV1{ID: in.ID, Deleted: true}, nil
		}),
		api.Define(api.Operation{
			Name: "reset_event_outreach", Method: http.MethodPost, Path: "/api/v1/events/{id}/outreach/reset",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Remove every outreach post and flyer for an event so drafting can start over.",
			Description: "Refused once anything has been posted, queued or put up. The outreach and its short links are kept.",
			Destructive: true,
		}, func(ctx context.Context, in outreachIDIn) (eventOutreachV1, error) {
			v, err := h.resetOutreach(ctx, "", nil, in.ID)
			if err != nil {
				return eventOutreachV1{}, outreachAPIError(err)
			}
			return eventOutreachV1{Started: true, outreachPanelView: pacificPanel(v)}, nil
		}),
	}
}
