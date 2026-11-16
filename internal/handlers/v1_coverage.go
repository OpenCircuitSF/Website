package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/cadence"
)

// The /api/v1 event coverage operations (#0678). They run
// EventCadenceHandler's transport-free methods, the same code as the admin
// routes in admin_event_cadence.go. There is no delete tool: set_cadence_rule
// with active false retires a rule, and deleting stays in the admin.

type eventCoverageIn struct {
	Month *string `json:"month,omitempty" query:"month" jsonschema:"YYYY-MM; default next month in America/Los_Angeles"`
}

type listCadenceRulesIn struct {
	api.Page
	Active *bool `json:"active,omitempty" query:"active" jsonschema:"only active (true) or retired (false) rules"`
}

type setCadenceRuleIn struct {
	ID *int64 `json:"id,omitempty" jsonschema:"omit to create; an existing id to replace that rule"`
	cadenceRuleFields
}

// cadenceAPIError maps a service error to the v1 envelope; anything else is
// returned as is, so api.AsError logs it and answers 500.
func cadenceAPIError(err error) error {
	var fe *cadenceFieldError
	switch {
	case errors.As(err, &fe):
		return api.Validation(fe.fields)
	case errors.Is(err, cadence.ErrNotFound):
		return api.NotFound("cadence rule")
	}
	return err
}

func coverageOperations(h *EventCadenceHandler) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "event_coverage", Method: http.MethodGet, Path: "/api/v1/events/coverage",
			Scope:       apitokens.ScopeEventsRead,
			Summary:     "Are we missing any events? A month's events checked against the cadence rules, plus a Luma comparison.",
			Description: "For each active rule: the expected dates or count, the matching events, missing dates, and dates covered only by drafts. Canceled events never count. Also lists events matching no rule, and which published events are missing from Luma or differ there. The Luma part reports a status instead of failing when the feed cannot be read. Times and dates are Pacific; admin_url opens the event in the admin.",
		}, func(ctx context.Context, in eventCoverageIn) (coverageV1, error) {
			month := ""
			if in.Month != nil {
				month = *in.Month
			}
			rep, err := h.report(ctx, month)
			return rep, cadenceAPIError(err)
		}),
		api.Define(api.Operation{
			Name: "list_cadence_rules", Method: http.MethodGet, Path: "/api/v1/cadence-rules",
			Scope:   apitokens.ScopeEventsRead,
			Summary: "List the cadence rules event_coverage checks: what a normal month of events looks like.",
		}, func(ctx context.Context, in listCadenceRulesIn) (api.List[cadenceRuleV1], error) {
			all, err := h.listRules(ctx)
			if err != nil {
				return api.List[cadenceRuleV1]{}, err
			}
			items := make([]cadenceRuleV1, 0, len(all))
			for _, r := range all {
				if in.Active == nil || r.Active == *in.Active {
					items = append(items, r)
				}
			}
			return pageByID(items, in.Page, func(r cadenceRuleV1) int64 { return r.ID })
		}),
		api.Define(api.Operation{
			Name: "set_cadence_rule", Method: http.MethodPost, Path: "/api/v1/cadence-rules",
			Scope:       apitokens.ScopeEventsWrite,
			Summary:     "Create a cadence rule, or replace one by id: weekly on a weekday, or N a month.",
			Description: "A rule matches events by event type, by a case-insensitive title prefix, or both, and optionally by venue. Replacing sends the whole rule. Set active false to retire a rule; there is no delete.",
		}, func(ctx context.Context, in setCadenceRuleIn) (cadenceRuleV1, error) {
			r, err := in.rule()
			if err != nil {
				return cadenceRuleV1{}, cadenceAPIError(err)
			}
			saved, err := h.setRule(ctx, "", nil, in.ID, r)
			if err != nil {
				return cadenceRuleV1{}, cadenceAPIError(err)
			}
			n, err := h.names(ctx)
			if err != nil {
				return cadenceRuleV1{}, err
			}
			return n.rule(saved), nil
		}),
	}
}
