package handlers

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
)

// outreachLocationStore is the data-layer behavior the location routes need
// (#0548). *outreach.Store satisfies it.
type outreachLocationStore interface {
	ListLocations(ctx context.Context) ([]outreach.Location, error)
	GetLocation(ctx context.Context, id int64) (outreach.Location, error)
	CreateLocation(ctx context.Context, in outreach.LocationInput) (outreach.Location, error)
	UpdateLocation(ctx context.Context, id int64, p outreach.LocationPatch) (outreach.Location, error)
	DeleteLocation(ctx context.Context, id int64) error
}

const (
	outreachLocNameMax    = 100
	outreachLocAddressMax = 300
	outreachLocNotesMax   = 2000

	msgLocCodeInvalid = "code must be 2–16 lowercase letters, digits or dashes"
)

type outreachLocationView struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Code         string  `json:"code"`
	Address      string  `json:"address"`
	Notes        string  `json:"notes"`
	Active       bool    `json:"active"`
	CodeLocked   bool    `json:"code_locked"`
	CodeLockedAt *string `json:"code_locked_at"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    *string `json:"updated_at"`
}

func locationView(l outreach.Location) outreachLocationView {
	return outreachLocationView{
		ID: l.ID, Name: l.Name, Code: l.Code, Address: l.Address, Notes: l.Notes, Active: l.Active,
		CodeLocked: l.CodeLockedAt != nil, CodeLockedAt: rfc3339(l.CodeLockedAt),
		CreatedAt: l.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: rfc3339(l.UpdatedAt),
	}
}

// ListLocations handles GET /admin/outreach-locations.
func (h *AdminOutreachHandler) ListLocations(w http.ResponseWriter, r *http.Request) {
	list, err := h.locations.ListLocations(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views := make([]outreachLocationView, 0, len(list))
	for _, l := range list {
		views = append(views, locationView(l))
	}
	writeJSON(w, http.StatusOK, map[string]any{"locations": views})
}

type outreachLocationRequest struct {
	Name    *string `json:"name"`
	Code    *string `json:"code"`
	Address *string `json:"address"`
	Notes   *string `json:"notes"`
	Active  *bool   `json:"active"`
}

// validateLocationFields trims name/address/notes in place and returns the
// first problem found, or "". The code is validated as given, never
// normalised: the SPA normalises, the server rejects.
func validateLocationFields(req *outreachLocationRequest) string {
	if req.Name != nil {
		t := strings.TrimSpace(*req.Name)
		req.Name = &t
		if n := utf8.RuneCountInString(t); n < 1 || n > outreachLocNameMax {
			return "name must be 1–100 characters"
		}
	}
	if req.Code != nil && !outreach.ValidCode(*req.Code) {
		return msgLocCodeInvalid
	}
	if req.Address != nil {
		t := strings.TrimSpace(*req.Address)
		req.Address = &t
		if utf8.RuneCountInString(t) > outreachLocAddressMax {
			return "address must be at most 300 characters"
		}
	}
	if req.Notes != nil {
		t := strings.TrimSpace(*req.Notes)
		req.Notes = &t
		if utf8.RuneCountInString(t) > outreachLocNotesMax {
			return "notes must be at most 2000 characters"
		}
	}
	return ""
}

// CreateLocation handles POST /admin/outreach-locations.
func (h *AdminOutreachHandler) CreateLocation(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req outreachLocationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	actorID := actor.ID
	loc, err := h.createLocation(r.Context(), clientIP(r), &actorID, req)
	if err != nil {
		writeOutreachError(w, "create location", err)
		return
	}
	writeJSON(w, http.StatusCreated, locationView(loc))
}

// locationFieldOf names the request field a validateLocationFields message
// is about, for the v1 validation envelope.
func locationFieldOf(msg string) string {
	for _, f := range []string{"name", "code", "address", "notes"} {
		if strings.HasPrefix(msg, f+" ") {
			return f
		}
	}
	return ""
}

func locationBadRequest(msg string) error {
	return outreachRefusal(http.StatusBadRequest, locationFieldOf(msg), msg)
}

// createLocation is CreateLocation without the transport (#0702).
func (h *AdminOutreachHandler) createLocation(ctx context.Context, ip string, actorID *int64, req outreachLocationRequest) (outreach.Location, error) {
	if req.Name == nil {
		return outreach.Location{}, locationBadRequest("name must be 1–100 characters")
	}
	if req.Code == nil {
		return outreach.Location{}, locationBadRequest(msgLocCodeInvalid)
	}
	if msg := validateLocationFields(&req); msg != "" {
		return outreach.Location{}, locationBadRequest(msg)
	}
	in := outreach.LocationInput{Name: *req.Name, Code: *req.Code, Active: true}
	if req.Address != nil {
		in.Address = *req.Address
	}
	if req.Notes != nil {
		in.Notes = *req.Notes
	}
	if req.Active != nil {
		in.Active = *req.Active
	}
	loc, err := h.locations.CreateLocation(ctx, in)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrCodeTaken):
		return outreach.Location{}, outreachRefusal(http.StatusConflict, "code", "code already in use")
	default:
		return outreach.Location{}, err
	}
	h.recordCtx(ctx, ip, audit.TargetOutreachLocation, loc.ID, audit.Entry{
		ActorID:  actorID,
		Action:   audit.ActionOutreachLocationCreated,
		Metadata: map[string]any{"code": loc.Code, "name": loc.Name, "active": loc.Active},
	})
	return loc, nil
}

// changedLocationFields lists, sorted, the fields whose value differs between
// before and after. "address" appears here only as a value; the audit
// metadata guard forbids it as a key.
func changedLocationFields(before, after outreach.Location) []string {
	changed := []string{}
	if before.Name != after.Name {
		changed = append(changed, "name")
	}
	if before.Code != after.Code {
		changed = append(changed, "code")
	}
	if before.Address != after.Address {
		changed = append(changed, "address")
	}
	if before.Notes != after.Notes {
		changed = append(changed, "notes")
	}
	if before.Active != after.Active {
		changed = append(changed, "active")
	}
	sort.Strings(changed)
	return changed
}

// PatchLocation handles PATCH /admin/outreach-locations/{id}.
func (h *AdminOutreachHandler) PatchLocation(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachLocationPathID(w, r)
	if !ok {
		return
	}
	var req outreachLocationRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	actorID := actor.ID
	after, err := h.patchLocation(r.Context(), clientIP(r), &actorID, id, req)
	if err != nil {
		writeOutreachError(w, "update location", err)
		return
	}
	writeJSON(w, http.StatusOK, locationView(after))
}

var errLocationNotFound = outreachRefusal(http.StatusNotFound, "", "location not found")

// patchLocation is PatchLocation without the transport (#0702).
func (h *AdminOutreachHandler) patchLocation(ctx context.Context, ip string, actorID *int64, id int64, req outreachLocationRequest) (outreach.Location, error) {
	if req.Name == nil && req.Code == nil && req.Address == nil && req.Notes == nil && req.Active == nil {
		return outreach.Location{}, locationBadRequest("no fields to update")
	}
	if msg := validateLocationFields(&req); msg != "" {
		return outreach.Location{}, locationBadRequest(msg)
	}
	before, err := h.locations.GetLocation(ctx, id)
	if errors.Is(err, outreach.ErrLocationNotFound) {
		return outreach.Location{}, errLocationNotFound
	} else if err != nil {
		return outreach.Location{}, err
	}
	after, err := h.locations.UpdateLocation(ctx, id, outreach.LocationPatch{
		Name: req.Name, Code: req.Code, Address: req.Address, Notes: req.Notes, Active: req.Active,
	})
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrLocationNotFound):
		return outreach.Location{}, errLocationNotFound
	case errors.Is(err, outreach.ErrCodeTaken):
		return outreach.Location{}, outreachRefusal(http.StatusConflict, "code", "code already in use")
	case errors.Is(err, outreach.ErrCodeLocked):
		return outreach.Location{}, outreachRefusal(http.StatusConflict, "code", "code is locked: flyer links already use it")
	default:
		return outreach.Location{}, err
	}
	if changed := changedLocationFields(before, after); len(changed) > 0 {
		meta := map[string]any{"code": after.Code, "changed": changed, "active": after.Active}
		if before.Code != after.Code {
			meta["previous_code"] = before.Code
		}
		h.recordCtx(ctx, ip, audit.TargetOutreachLocation, after.ID, audit.Entry{
			ActorID:  actorID,
			Action:   audit.ActionOutreachLocationUpdated,
			Metadata: meta,
		})
	}
	return after, nil
}

// DeleteLocation handles DELETE /admin/outreach-locations/{id}. A location a
// flyer link uses is 409: deactivate it instead.
func (h *AdminOutreachHandler) DeleteLocation(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := outreachLocationPathID(w, r)
	if !ok {
		return
	}
	actorID := actor.ID
	if err := h.deleteLocation(r.Context(), clientIP(r), &actorID, id); err != nil {
		writeOutreachError(w, "delete location", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteLocation is DeleteLocation without the transport (#0702).
func (h *AdminOutreachHandler) deleteLocation(ctx context.Context, ip string, actorID *int64, id int64) error {
	loc, err := h.locations.GetLocation(ctx, id)
	if errors.Is(err, outreach.ErrLocationNotFound) {
		return errLocationNotFound
	} else if err != nil {
		return err
	}
	switch err := h.locations.DeleteLocation(ctx, id); {
	case err == nil:
	case errors.Is(err, outreach.ErrLocationNotFound):
		return errLocationNotFound
	case errors.Is(err, outreach.ErrLocationInUse):
		return outreachRefusal(http.StatusConflict, "", "location has flyer links; deactivate it instead")
	default:
		return err
	}
	h.recordCtx(ctx, ip, audit.TargetOutreachLocation, loc.ID, audit.Entry{
		ActorID:  actorID,
		Action:   audit.ActionOutreachLocationDeleted,
		Metadata: map[string]any{"code": loc.Code, "name": loc.Name},
	})
	return nil
}

// outreachLocationPathID parses {id}; a malformed id is 404, matching the
// neighbouring admin handlers' behaviour for a record that cannot exist.
func outreachLocationPathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "location not found")
		return 0, false
	}
	return id, true
}
