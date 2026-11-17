package handlers

import (
	"context"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

type outreachLocationsV1 struct {
	Locations []outreachLocationView `json:"locations"`
}

type createOutreachLocationIn struct {
	Name    string  `json:"name" jsonschema:"1-100 characters, e.g. Noisebridge"`
	Code    string  `json:"code" jsonschema:"2-16 lowercase letters, digits or dashes, e.g. noisebridge; used in flyer short links"`
	Address *string `json:"address,omitempty" jsonschema:"optional street address, at most 300 characters"`
	Notes   *string `json:"notes,omitempty" jsonschema:"optional notes, at most 2000 characters"`
	Active  *bool   `json:"active,omitempty" jsonschema:"defaults to true; false hides it from new flyers"`
}

type updateOutreachLocationIn struct {
	ID      int64   `json:"id" path:"id"`
	Name    *string `json:"name,omitempty" jsonschema:"1-100 characters"`
	Code    *string `json:"code,omitempty" jsonschema:"2-16 lowercase letters, digits or dashes; locked once a flyer link uses it"`
	Address *string `json:"address,omitempty" jsonschema:"at most 300 characters"`
	Notes   *string `json:"notes,omitempty" jsonschema:"at most 2000 characters"`
	Active  *bool   `json:"active,omitempty" jsonschema:"false hides it from new flyers"`
}

// locationViewV1 is locationView with its times in Pacific.
func locationViewV1(l outreach.Location) outreachLocationView {
	v := locationView(l)
	v.CodeLockedAt = pacificRFC3339Ptr(v.CodeLockedAt)
	v.CreatedAt = pacificRFC3339(v.CreatedAt)
	v.UpdatedAt = pacificRFC3339Ptr(v.UpdatedAt)
	return v
}

// outreachLocationOperations are the /api/v1 flyer-location operations
// (#0702). Each runs the same transport-free method as its /admin route.
func outreachLocationOperations(h *AdminOutreachHandler) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "list_outreach_locations", Method: http.MethodGet, Path: "/api/v1/outreach/locations",
			Scope:       apitokens.ScopeOutreachRead,
			Summary:     "List the places flyers are put up, active and inactive.",
			Description: "Times have a Pacific offset.",
		}, func(ctx context.Context, _ struct{}) (outreachLocationsV1, error) {
			list, err := h.locations.ListLocations(ctx)
			if err != nil {
				return outreachLocationsV1{}, err
			}
			out := outreachLocationsV1{Locations: make([]outreachLocationView, 0, len(list))}
			for _, l := range list {
				out.Locations = append(out.Locations, locationViewV1(l))
			}
			return out, nil
		}),
		api.Define(api.Operation{
			Name: "create_outreach_location", Method: http.MethodPost, Path: "/api/v1/outreach/locations",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Add a place flyers are put up.",
			Description: "The code must be unique. Times have a Pacific offset.",
			Idempotency: true,
			Status:      http.StatusCreated,
		}, func(ctx context.Context, in createOutreachLocationIn) (outreachLocationView, error) {
			l, err := h.createLocation(ctx, "", nil, outreachLocationRequest{
				Name: &in.Name, Code: &in.Code, Address: in.Address, Notes: in.Notes, Active: in.Active,
			})
			if err != nil {
				return outreachLocationView{}, outreachAPIError(err)
			}
			return locationViewV1(l), nil
		}),
		api.Define(api.Operation{
			Name: "update_outreach_location", Method: http.MethodPatch, Path: "/api/v1/outreach/locations/{id}",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Change a flyer location's name, code, address, notes or active flag.",
			Description: "Send only the fields to change. The code cannot change once a flyer link uses it. Times have a Pacific offset.",
		}, func(ctx context.Context, in updateOutreachLocationIn) (outreachLocationView, error) {
			l, err := h.patchLocation(ctx, "", nil, in.ID, outreachLocationRequest{
				Name: in.Name, Code: in.Code, Address: in.Address, Notes: in.Notes, Active: in.Active,
			})
			if err != nil {
				return outreachLocationView{}, outreachAPIError(err)
			}
			return locationViewV1(l), nil
		}),
		api.Define(api.Operation{
			Name: "delete_outreach_location", Method: http.MethodDelete, Path: "/api/v1/outreach/locations/{id}",
			Scope:       apitokens.ScopeOutreachWrite,
			Summary:     "Delete a flyer location no flyer link uses.",
			Description: "A location with flyer links is a conflict: set active to false instead.",
			Destructive: true,
		}, func(ctx context.Context, in outreachIDIn) (deletedV1, error) {
			if err := h.deleteLocation(ctx, "", nil, in.ID); err != nil {
				return deletedV1{}, outreachAPIError(err)
			}
			return deletedV1{ID: in.ID, Deleted: true}, nil
		}),
	}
}
