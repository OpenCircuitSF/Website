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
	if req.Name == nil {
		writeError(w, http.StatusBadRequest, "name must be 1–100 characters")
		return
	}
	if req.Code == nil {
		writeError(w, http.StatusBadRequest, msgLocCodeInvalid)
		return
	}
	if msg := validateLocationFields(&req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
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
	loc, err := h.locations.CreateLocation(r.Context(), in)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrCodeTaken):
		writeError(w, http.StatusConflict, "code already in use")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.auditor != nil {
		actorID, targetID := actor.ID, loc.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachLocationCreated,
			TargetType: audit.TargetOutreachLocation,
			TargetID:   &targetID,
			Metadata:   map[string]any{"code": loc.Code, "name": loc.Name, "active": loc.Active},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusCreated, locationView(loc))
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
	if req.Name == nil && req.Code == nil && req.Address == nil && req.Notes == nil && req.Active == nil {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}
	if msg := validateLocationFields(&req); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	before, err := h.locations.GetLocation(r.Context(), id)
	if errors.Is(err, outreach.ErrLocationNotFound) {
		writeError(w, http.StatusNotFound, "location not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	after, err := h.locations.UpdateLocation(r.Context(), id, outreach.LocationPatch{
		Name: req.Name, Code: req.Code, Address: req.Address, Notes: req.Notes, Active: req.Active,
	})
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrLocationNotFound):
		writeError(w, http.StatusNotFound, "location not found")
		return
	case errors.Is(err, outreach.ErrCodeTaken):
		writeError(w, http.StatusConflict, "code already in use")
		return
	case errors.Is(err, outreach.ErrCodeLocked):
		writeError(w, http.StatusConflict, "code is locked: flyer links already use it")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if changed := changedLocationFields(before, after); len(changed) > 0 && h.auditor != nil {
		meta := map[string]any{"code": after.Code, "changed": changed, "active": after.Active}
		if before.Code != after.Code {
			meta["previous_code"] = before.Code
		}
		actorID, targetID := actor.ID, after.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachLocationUpdated,
			TargetType: audit.TargetOutreachLocation,
			TargetID:   &targetID,
			Metadata:   meta,
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, locationView(after))
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
	loc, err := h.locations.GetLocation(r.Context(), id)
	if errors.Is(err, outreach.ErrLocationNotFound) {
		writeError(w, http.StatusNotFound, "location not found")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	switch err := h.locations.DeleteLocation(r.Context(), id); {
	case err == nil:
	case errors.Is(err, outreach.ErrLocationNotFound):
		writeError(w, http.StatusNotFound, "location not found")
		return
	case errors.Is(err, outreach.ErrLocationInUse):
		writeError(w, http.StatusConflict, "location has flyer links; deactivate it instead")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.auditor != nil {
		actorID, targetID := actor.ID, loc.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionOutreachLocationDeleted,
			TargetType: audit.TargetOutreachLocation,
			TargetID:   &targetID,
			Metadata:   map[string]any{"code": loc.Code, "name": loc.Name},
			IP:         clientIP(r),
		})
	}
	w.WriteHeader(http.StatusNoContent)
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
