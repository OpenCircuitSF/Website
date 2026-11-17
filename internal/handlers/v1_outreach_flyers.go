package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

// flyerV1 is a flyer's editor view with its times in Pacific and the admin
// page a person prints it from.
type flyerV1 struct {
	flyerView
	PrintURL string `json:"print_url" jsonschema:"the admin page that prints this flyer; needs an admin sign-in"`
}

type flyersV1 struct {
	Flyers []flyerRowView `json:"flyers"`
}

type createFlyerIn struct {
	OutreachID       int64    `json:"outreach_id" jsonschema:"the outreach (one per event) the flyer belongs to"`
	Photo            *string  `json:"photo,omitempty" jsonschema:"a /media/ path from upload_media"`
	FocalX           *float64 `json:"focal_x,omitempty" jsonschema:"0-1, defaults to 0.5"`
	FocalY           *float64 `json:"focal_y,omitempty" jsonschema:"0-1, defaults to 0.5"`
	Template         *string  `json:"template,omitempty" jsonschema:"defaults to full-photo"`
	Tagline          *string  `json:"tagline,omitempty" jsonschema:"at most 140 characters"`
	Description      *string  `json:"description,omitempty" jsonschema:"at most 360 characters"`
	DescriptionShort *string  `json:"description_short,omitempty" jsonschema:"at most 140 characters"`
	TitlePosition    *string  `json:"title_position,omitempty" jsonschema:"defaults to auto"`
}

type updateFlyerIn struct {
	ID               int64    `json:"id" path:"id"`
	Photo            *string  `json:"photo,omitempty" jsonschema:"a /media/ path from upload_media; empty clears it"`
	FocalX           *float64 `json:"focal_x,omitempty" jsonschema:"0-1"`
	FocalY           *float64 `json:"focal_y,omitempty" jsonschema:"0-1"`
	Template         *string  `json:"template,omitempty"`
	Tagline          *string  `json:"tagline,omitempty" jsonschema:"at most 140 characters"`
	Description      *string  `json:"description,omitempty" jsonschema:"at most 360 characters"`
	DescriptionShort *string  `json:"description_short,omitempty" jsonschema:"at most 140 characters"`
	TitlePosition    *string  `json:"title_position,omitempty"`
}

type setFlyerLocationsIn struct {
	ID        int64                `json:"id" path:"id"`
	Locations []flyerSelectionItem `json:"locations" jsonschema:"the whole selection; replaces what is there"`
}

type setFlyerLinkPutUpIn struct {
	ID    int64 `json:"id" path:"id" jsonschema:"a flyer link id, from a flyer's selection"`
	PutUp bool  `json:"put_up" jsonschema:"true marks the flyer as put up (keeping the first time); false clears it"`
}

// flyerPhotoV1 refuses a v1 photo that is not a /media/ path; the admin's
// own same-site check still runs after it.
func flyerPhotoV1(photo *string) error {
	if photo == nil {
		return nil
	}
	t := strings.TrimSpace(*photo)
	if t != "" && !strings.HasPrefix(t, "/media/") {
		return api.Validation(map[string]string{"photo": "photo must be a /media/ path"})
	}
	return nil
}

func flyerRowV1(r flyerRowView) flyerRowView {
	r.CreatedAt = pacificRFC3339(r.CreatedAt)
	r.UpdatedAt = pacificRFC3339Ptr(r.UpdatedAt)
	return r
}

func flyerLinkV1(l flyerLinkView) *flyerLinkView {
	l.PutUpAt = pacificRFC3339Ptr(l.PutUpAt)
	return &l
}

func (h *AdminOutreachHandler) flyerV1Of(v flyerView) flyerV1 {
	v.Flyer = flyerRowV1(v.Flyer)
	sel := make([]flyerSelectionView, 0, len(v.Selection))
	for _, s := range v.Selection {
		if s.Link != nil {
			s.Link = flyerLinkV1(*s.Link)
		}
		sel = append(sel, s)
	}
	v.Selection = sel
	return flyerV1{flyerView: v, PrintURL: h.baseURL + "/admin#/outreach?view=print&flyer=" + strconv.FormatInt(v.Flyer.ID, 10) + "&layout=full"}
}

func (h *AdminOutreachHandler) flyerResultV1(v flyerView, err error) (flyerV1, error) {
	if err != nil {
		return flyerV1{}, outreachAPIError(err)
	}
	return h.flyerV1Of(v), nil
}

// outreachFlyerOperations are the /api/v1 flyer operations (#0703). Each runs
// the same transport-free method as its /admin route.
func outreachFlyerOperations(h *AdminOutreachHandler) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "list_flyers", Method: http.MethodGet, Path: "/api/v1/flyers",
			Scope:       apitokens.ScopeOutreachRead,
			Summary:     "List saved flyers with their event.",
			Description: "Times have a Pacific offset. get_flyer has a flyer's locations, links and print_url.",
		}, func(ctx context.Context, _ struct{}) (flyersV1, error) {
			rows, err := h.listFlyerRows(ctx)
			if err != nil {
				return flyersV1{}, err
			}
			out := flyersV1{Flyers: make([]flyerRowView, 0, len(rows))}
			for _, r := range rows {
				out.Flyers = append(out.Flyers, flyerRowV1(r))
			}
			return out, nil
		}),
		api.Define(api.Operation{
			Name: "get_flyer", Method: http.MethodGet, Path: "/api/v1/flyers/{id}",
			Scope:       apitokens.ScopeOutreachRead,
			Summary:     "Get one flyer: its design, the event facts it prints, its locations and their short links.",
			Description: "print_url is the admin page a person prints it from. Times have a Pacific offset.",
		}, func(ctx context.Context, in outreachIDIn) (flyerV1, error) {
			return h.flyerResultV1(h.flyerByID(ctx, in.ID))
		}),
		api.Define(api.Operation{
			Name: "create_flyer", Method: http.MethodPost, Path: "/api/v1/flyers",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Create a flyer for an event's outreach.",
			Description: "Then set_flyer_locations and generate_flyer. Times have a Pacific offset.",
			Idempotency: true,
			Status:      http.StatusCreated,
		}, func(ctx context.Context, in createFlyerIn) (flyerV1, error) {
			if err := flyerPhotoV1(in.Photo); err != nil {
				return flyerV1{}, err
			}
			outreachID := in.OutreachID
			return h.flyerResultV1(h.createFlyer(ctx, "", nil, flyerRequest{
				OutreachID: &outreachID, Photo: in.Photo, FocalX: in.FocalX, FocalY: in.FocalY, Template: in.Template,
				Tagline: in.Tagline, Description: in.Description, DescriptionShort: in.DescriptionShort, TitlePosition: in.TitlePosition,
			}))
		}),
		api.Define(api.Operation{
			Name: "update_flyer", Method: http.MethodPatch, Path: "/api/v1/flyers/{id}",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Change a flyer's photo, focal point, template, tagline, descriptions or title position.",
			Description: "Send only the fields to change. Times have a Pacific offset.",
		}, func(ctx context.Context, in updateFlyerIn) (flyerV1, error) {
			if err := flyerPhotoV1(in.Photo); err != nil {
				return flyerV1{}, err
			}
			return h.flyerResultV1(h.patchFlyer(ctx, "", nil, in.ID, flyerRequest{
				Photo: in.Photo, FocalX: in.FocalX, FocalY: in.FocalY, Template: in.Template,
				Tagline: in.Tagline, Description: in.Description, DescriptionShort: in.DescriptionShort, TitlePosition: in.TitlePosition,
			}))
		}),
		api.Define(api.Operation{
			Name: "set_flyer_locations", Method: http.MethodPut, Path: "/api/v1/flyers/{id}/locations",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Replace the places a flyer goes up and how many copies each gets.",
			Description: "Each location must be active and listed once, with 1-50 copies. Times have a Pacific offset.",
		}, func(ctx context.Context, in setFlyerLocationsIn) (flyerV1, error) {
			return h.flyerResultV1(h.putFlyerLocations(ctx, "", nil, in.ID, in.Locations))
		}),
		api.Define(api.Operation{
			Name: "generate_flyer", Method: http.MethodPost, Path: "/api/v1/flyers/{id}/generate",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Make each selected location's tracked short link and QR code.",
			Description: "The event must be published. Safe to repeat: existing links are reused, and when remaining is above 0 calling it again continues. Times have a Pacific offset.",
		}, func(ctx context.Context, in outreachIDIn) (flyerV1, error) {
			return h.flyerResultV1(h.generateFlyer(ctx, "", nil, in.ID))
		}),
		api.Define(api.Operation{
			Name: "set_flyer_link_put_up", Method: http.MethodPost, Path: "/api/v1/flyer-links/{id}/put-up",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Mark a flyer link's flyer as put up, or clear that.",
			Description: "Times have a Pacific offset.",
		}, func(ctx context.Context, in setFlyerLinkPutUpIn) (flyerLinkView, error) {
			l, err := h.setFlyerLinkPutUp(ctx, "", nil, in.ID, in.PutUp)
			if err != nil {
				return flyerLinkView{}, outreachAPIError(err)
			}
			return *flyerLinkV1(*flyerLinkViewOf(l)), nil
		}),
		api.Define(api.Operation{
			Name: "delete_flyer", Method: http.MethodDelete, Path: "/api/v1/flyers/{id}",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Delete a flyer.",
			Description: "Its short links stay, since they belong to the event's outreach.",
			Destructive: true,
		}, func(ctx context.Context, in outreachIDIn) (deletedV1, error) {
			if err := h.deleteFlyer(ctx, "", nil, in.ID); err != nil {
				return deletedV1{}, outreachAPIError(err)
			}
			return deletedV1{ID: in.ID, Deleted: true}, nil
		}),
	}
}
