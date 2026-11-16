package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/cadence"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// The admin session routes for event cadence (#0678), on the same
// EventCadenceHandler v1 uses: Settings → Event cadence and the Events
// page's "Next month" panel.
//
//	GET    /admin/event-cadence        {rules: [...]}
//	POST   /admin/event-cadence        create
//	PATCH  /admin/event-cadence/{id}   replace the whole rule
//	DELETE /admin/event-cadence/{id}
//	GET    /admin/event-coverage?month=YYYY-MM   the v1 report, same JSON

// cadenceRuleFields is a rule as the admin form and v1's set_cadence_rule
// send it.
type cadenceRuleFields struct {
	Name        string  `json:"name"`
	EventTypeID *int64  `json:"event_type_id,omitempty" jsonschema:"an id from list_event_types; set this, title_prefix, or both"`
	TitlePrefix *string `json:"title_prefix,omitempty" jsonschema:"case-insensitive title prefix, e.g. Programming LEDs"`
	Frequency   string  `json:"frequency" jsonschema:"weekly or monthly"`
	Weekday     *string `json:"weekday,omitempty" jsonschema:"weekly only: sunday … saturday"`
	PerMonth    *int    `json:"per_month,omitempty" jsonschema:"monthly only: 1 to 31"`
	VenueID     *int64  `json:"venue_id,omitempty" jsonschema:"an id from list_venues; omit for any venue"`
	Active      *bool   `json:"active,omitempty" jsonschema:"default true; false retires the rule"`
}

// rule maps the fields onto a cadence.Rule. A weekday that is not a
// lowercase English name is refused here; everything else is Validate's.
func (f cadenceRuleFields) rule() (cadence.Rule, error) {
	r := cadence.Rule{Name: f.Name, EventTypeID: f.EventTypeID, Frequency: cadence.Frequency(f.Frequency), VenueID: f.VenueID, Active: true}
	if f.TitlePrefix != nil {
		r.TitlePrefix = *f.TitlePrefix
	}
	if f.Weekday != nil && strings.TrimSpace(*f.Weekday) != "" {
		d, ok := cadence.ParseWeekday(strings.ToLower(strings.TrimSpace(*f.Weekday)))
		if !ok {
			return cadence.Rule{}, &cadenceFieldError{fields: map[string]string{"weekday": "must be sunday, monday, tuesday, wednesday, thursday, friday or saturday"}}
		}
		r.Weekday = &d
	}
	if f.PerMonth != nil {
		r.PerMonth = *f.PerMonth
	}
	if f.Active != nil {
		r.Active = *f.Active
	}
	return r, nil
}

type cadenceRulesResponse struct {
	Rules []cadenceRuleV1 `json:"rules"`
}

// SetCadence attaches the event coverage service, so its admin routes are
// mounted next to /admin/workshops without another mountAndServe parameter.
func (h *AdminWorkshopsHandler) SetCadence(c *EventCadenceHandler) { h.cadence = c }

// Cadence is the attached event coverage service, or nil.
func (h *AdminWorkshopsHandler) Cadence() *EventCadenceHandler { return h.cadence }

// SetNow pins the handler's clock; tests use it for the default month.
func (h *EventCadenceHandler) SetNow(now func() time.Time) { h.now = now }

// adminCadenceError writes a service error as the admin routes answer.
func adminCadenceError(w http.ResponseWriter, err error) {
	var fe *cadenceFieldError
	switch {
	case errors.As(err, &fe):
		writeError(w, http.StatusBadRequest, fe.message())
	case errors.Is(err, cadence.ErrNotFound):
		writeError(w, http.StatusNotFound, "cadence rule not found")
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

// ListRules handles GET /admin/event-cadence.
func (h *EventCadenceHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.listRules(r.Context())
	if err != nil {
		adminCadenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cadenceRulesResponse{Rules: rules})
}

// CreateRule handles POST /admin/event-cadence.
func (h *EventCadenceHandler) CreateRule(w http.ResponseWriter, r *http.Request) {
	h.saveRule(w, r, nil)
}

// UpdateRule handles PATCH /admin/event-cadence/{id}: the form always sends
// the whole rule, so this replaces it.
func (h *EventCadenceHandler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	id, ok := cadenceRuleID(w, r)
	if !ok {
		return
	}
	h.saveRule(w, r, &id)
}

func (h *EventCadenceHandler) saveRule(w http.ResponseWriter, r *http.Request, id *int64) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req cadenceRuleFields
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule, err := req.rule()
	if err != nil {
		adminCadenceError(w, err)
		return
	}
	actorID := actor.ID
	saved, err := h.setRule(r.Context(), clientIP(r), &actorID, id, rule)
	if err != nil {
		adminCadenceError(w, err)
		return
	}
	n, err := h.names(r.Context())
	if err != nil {
		adminCadenceError(w, err)
		return
	}
	status := http.StatusOK
	if id == nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, n.rule(saved))
}

// DeleteRule handles DELETE /admin/event-cadence/{id}.
func (h *EventCadenceHandler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := cadenceRuleID(w, r)
	if !ok {
		return
	}
	actorID := actor.ID
	if err := h.deleteRule(r.Context(), clientIP(r), &actorID, id); err != nil {
		adminCadenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "cadence rule deleted"})
}

// Coverage handles GET /admin/event-coverage?month=YYYY-MM (default next
// month): the same JSON as v1's event_coverage.
func (h *EventCadenceHandler) Coverage(w http.ResponseWriter, r *http.Request) {
	rep, err := h.report(r.Context(), r.URL.Query().Get("month"))
	if err != nil {
		adminCadenceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func cadenceRuleID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}
