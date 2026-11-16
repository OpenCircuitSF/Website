package handlers

import (
	"cmp"
	"context"
	"net/http"
	"slices"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
)

// The /api/v1 lookups (#0677): the ids a tool fills an event's venue_id,
// event_type_id and interest_ids with. Disabled or inactive rows are
// included and flagged, so a tool can see why an id is refused. Each list is
// sorted by id, with cursor {"id": last}.

type venueV1 struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	Address   *string `json:"address"`
	Note      *string `json:"note"`
	Enabled   bool    `json:"enabled" jsonschema:"false: a new event cannot use it"`
	IsDefault bool    `json:"is_default" jsonschema:"what create_event uses when neither venue_id nor no_venue is sent"`
}

type eventTypeV1 struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Enabled   bool   `json:"enabled" jsonschema:"false: a new event cannot use it"`
	IsDefault bool   `json:"is_default" jsonschema:"what create_event uses when event_type_id is not sent"`
}

type interestV1 struct {
	ID          int64   `json:"id"`
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Active      bool    `json:"active"`
}

type lookupIn struct {
	api.Page
}

// idCursor is the cursor of a list sorted by id.
type idCursor struct {
	ID int64 `json:"id"`
}

// pageByID sorts items by id and pages them.
func pageByID[T any](items []T, p api.Page, id func(T) int64) (api.List[T], error) {
	slices.SortFunc(items, func(a, b T) int { return cmp.Compare(id(a), id(b)) })
	return paginate(items, p,
		func(v T) idCursor { return idCursor{ID: id(v)} },
		func(v T, c idCursor) bool { return id(v) > c.ID })
}

func lookupOperations(d V1Deps) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "list_venues", Method: http.MethodGet, Path: "/api/v1/venues",
			Scope:   apitokens.ScopeVenuesRead,
			Summary: "List venues, including disabled ones, for an event's venue_id.",
		}, func(ctx context.Context, in lookupIn) (api.List[venueV1], error) {
			all, err := d.Venues.ListAll(ctx)
			if err != nil {
				return api.List[venueV1]{}, err
			}
			items := make([]venueV1, 0, len(all))
			for _, v := range all {
				items = append(items, venueV1{ID: v.ID, Name: v.Name, Address: v.Address, Note: v.Note, Enabled: v.Enabled, IsDefault: v.IsDefault})
			}
			return pageByID(items, in.Page, func(v venueV1) int64 { return v.ID })
		}),
		api.Define(api.Operation{
			Name: "list_event_types", Method: http.MethodGet, Path: "/api/v1/event-types",
			Scope:   apitokens.ScopeEventsRead,
			Summary: "List event types, including disabled ones, for an event's event_type_id.",
		}, func(ctx context.Context, in lookupIn) (api.List[eventTypeV1], error) {
			all, err := d.EventTypes.ListAll(ctx)
			if err != nil {
				return api.List[eventTypeV1]{}, err
			}
			items := make([]eventTypeV1, 0, len(all))
			for _, et := range all {
				items = append(items, eventTypeV1{ID: et.ID, Slug: et.Slug, Name: et.Name, Enabled: et.Enabled, IsDefault: et.IsDefault})
			}
			return pageByID(items, in.Page, func(v eventTypeV1) int64 { return v.ID })
		}),
		api.Define(api.Operation{
			Name: "list_interests", Method: http.MethodGet, Path: "/api/v1/interests",
			Scope:   apitokens.ScopeEventsRead,
			Summary: "List mailing-list interests, including inactive ones, for an event's interest_ids.",
		}, func(ctx context.Context, in lookupIn) (api.List[interestV1], error) {
			all, err := d.Interests.ListAll(ctx)
			if err != nil {
				return api.List[interestV1]{}, err
			}
			items := make([]interestV1, 0, len(all))
			for _, i := range all {
				items = append(items, interestV1{ID: i.ID, Slug: i.Slug, Name: i.Name, Description: i.Description, Active: i.Active})
			}
			return pageByID(items, in.Page, func(v interestV1) int64 { return v.ID })
		}),
	}
}
