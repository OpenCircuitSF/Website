package handlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/shortlinks"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// outreachFlyerStore is the data-layer behavior the flyer routes need
// (#0549). *outreach.Store satisfies it.
type outreachFlyerStore interface {
	ListFlyers(ctx context.Context, outreachID int64) ([]outreach.Flyer, error)
	GetFlyer(ctx context.Context, id int64) (outreach.Flyer, error)
	CreateFlyer(ctx context.Context, in outreach.FlyerInput) (outreach.Flyer, error)
	UpdateFlyer(ctx context.Context, id int64, p outreach.FlyerPatch) (outreach.Flyer, error)
	DeleteFlyer(ctx context.Context, id int64) error
	ListFlyerSelection(ctx context.Context, flyerID int64) ([]outreach.FlyerSelection, error)
	ReplaceFlyerSelection(ctx context.Context, flyerID int64, sel []outreach.SelectionInput) error
	GetFlyerLink(ctx context.Context, id int64) (outreach.FlyerLink, error)
	ListAllFlyerLinks(ctx context.Context) ([]outreach.FlyerLinkAll, error)
	ListFlyerLinks(ctx context.Context, outreachID int64) ([]outreach.FlyerLink, error)
	InsertFlyerLink(ctx context.Context, outreachID, locationID int64, expectedCode, key, shortURL string) (outreach.FlyerLink, bool, error)
	SetFlyerLinkQR(ctx context.Context, id int64, svg []byte) error
	FlyerLinkQR(ctx context.Context, id int64) ([]byte, string, error)
	SetPutUp(ctx context.Context, id int64, putUp bool) (outreach.FlyerLink, error)
}

// flyerGenerateTimeout bounds one Generate call. Apache's proxy timeout is
// its 60 s default, so the handler stops first and answers with what it did;
// a variable so a test can shorten it.
var flyerGenerateTimeout = 45 * time.Second

const (
	flyerPhotoMax = 300

	msgFlyerGenerateStopped = "Some links or QR codes could not be fetched. Everything done so far is saved. Click Generate again to continue."
	msgFlyerGenerateTimeout = "That took too long. Everything done so far is saved. Click Generate again to continue."
)

type flyerRowView struct {
	ID               int64   `json:"id"`
	OutreachID       int64   `json:"outreach_id"`
	WorkshopID       int64   `json:"workshop_id"`
	WorkshopSlug     string  `json:"workshop_slug"`
	WorkshopTitle    string  `json:"workshop_title"`
	Photo            *string `json:"photo"`
	FocalX           float64 `json:"focal_x"`
	FocalY           float64 `json:"focal_y"`
	Template         string  `json:"template"`
	Tagline          string  `json:"tagline"`
	Description      string  `json:"description"`
	DescriptionShort string  `json:"description_short"`
	TitlePosition    string  `json:"title_position"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        *string `json:"updated_at"`
}

type flyerFactsView struct {
	Title      string  `json:"title"`
	DateLine   string  `json:"date_line"`
	PlaceLine  string  `json:"place_line"`
	CoverImage *string `json:"cover_image"`
	// Description is the workshop's trimmed summary, or "" when it has none.
	Description string `json:"description"`
}

type flyerLinkView struct {
	ID       int64   `json:"id"`
	Key      string  `json:"short_link_key"`
	ShortURL string  `json:"short_url"`
	HasQR    bool    `json:"has_qr"`
	QRURL    *string `json:"qr_url"`
	PutUpAt  *string `json:"put_up_at"`
}

type flyerSelectionView struct {
	LocationID int64          `json:"location_id"`
	Name       string         `json:"name"`
	Code       string         `json:"code"`
	Address    string         `json:"address"`
	Active     bool           `json:"active"`
	Copies     int            `json:"copies"`
	Link       *flyerLinkView `json:"link"`
}

type flyerView struct {
	Flyer     flyerRowView         `json:"flyer"`
	Facts     flyerFactsView       `json:"facts"`
	Selection []flyerSelectionView `json:"selection"`
	// Remaining and Message are set only by Generate: how many selected
	// locations still lack a link or QR code, and why it stopped.
	Remaining *int   `json:"remaining,omitempty"`
	Message   string `json:"message,omitempty"`
}

func flyerLinkViewOf(l outreach.FlyerLink) *flyerLinkView {
	v := &flyerLinkView{ID: l.ID, Key: l.Key, ShortURL: l.ShortURL, HasQR: l.HasQR, PutUpAt: rfc3339(l.PutUpAt)}
	if l.HasQR {
		u := "/admin/outreach-flyer-links/" + strconv.FormatInt(l.ID, 10) + "/qr.svg"
		v.QRURL = &u
	}
	return v
}

func flyerRowViewOf(f outreach.Flyer, o outreach.Outreach, slug, title string) flyerRowView {
	return flyerRowView{
		ID: f.ID, OutreachID: f.OutreachID, WorkshopID: o.WorkshopID, WorkshopSlug: slug, WorkshopTitle: title,
		Photo: f.Photo, FocalX: f.FocalX, FocalY: f.FocalY, Template: f.Template, Tagline: f.Tagline,
		Description: f.Description, DescriptionShort: f.DescriptionShort, TitlePosition: f.TitlePosition,
		CreatedAt: f.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: rfc3339(f.UpdatedAt),
	}
}

// flyerView loads everything a flyer's editor and print page need (#0703:
// transport-free, for the admin routes and /api/v1).
func (h *AdminOutreachHandler) flyerView(ctx context.Context, f outreach.Flyer) (flyerView, error) {
	o, err := h.store.GetByID(ctx, f.OutreachID)
	if err != nil {
		return flyerView{}, err
	}
	wk, err := h.workshopFor(ctx, o.WorkshopID)
	if err != nil {
		return flyerView{}, err
	}
	sel, err := h.flyers.ListFlyerSelection(ctx, f.ID)
	if err != nil {
		return flyerView{}, err
	}
	v := flyerView{
		Flyer: flyerRowViewOf(f, o, wk.Slug, wk.Title),
		Facts: flyerFactsView{
			Title: wk.Title, DateLine: outreach.FlyerDateLine(wk.StartsAt, wk.EndsAt),
			PlaceLine: outreach.FlyerPlaceLine(wk.LocationName, wk.LocationAddress), CoverImage: wk.CoverImage,
		},
		Selection: make([]flyerSelectionView, 0, len(sel)),
	}
	if wk.Summary != nil {
		v.Facts.Description = strings.TrimSpace(*wk.Summary)
	}
	for _, s := range sel {
		sv := flyerSelectionView{LocationID: s.LocationID, Name: s.Name, Code: s.Code, Address: s.Address, Active: s.Active, Copies: s.Copies}
		if s.Link != nil {
			sv.Link = flyerLinkViewOf(*s.Link)
		}
		v.Selection = append(v.Selection, sv)
	}
	return v, nil
}

func flyerPathID(w http.ResponseWriter, r *http.Request, what string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, what+" not found")
		return 0, false
	}
	return id, true
}

var (
	errFlyerNotFound     = outreachRefusal(http.StatusNotFound, "", "flyer not found")
	errFlyerLinkNotFound = outreachRefusal(http.StatusNotFound, "", "flyer link not found")
	errOutreachNotFound  = outreachRefusal(http.StatusNotFound, "", "outreach not found")
)

// getFlyer loads flyer id, turning not-found into a refusal.
func (h *AdminOutreachHandler) getFlyer(ctx context.Context, id int64) (outreach.Flyer, error) {
	f, err := h.flyers.GetFlyer(ctx, id)
	if errors.Is(err, outreach.ErrFlyerNotFound) {
		return f, errFlyerNotFound
	}
	return f, err
}

// flyerByID is GetFlyer without the transport (#0703).
func (h *AdminOutreachHandler) flyerByID(ctx context.Context, id int64) (flyerView, error) {
	f, err := h.getFlyer(ctx, id)
	if err != nil {
		return flyerView{}, err
	}
	return h.flyerView(ctx, f)
}

// ListFlyers handles GET /admin/outreach-flyers.
func (h *AdminOutreachHandler) ListFlyers(w http.ResponseWriter, r *http.Request) {
	rows, err := h.listFlyerRows(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"flyers": rows})
}

// listFlyerRows is ListFlyers without the transport (#0703).
func (h *AdminOutreachHandler) listFlyerRows(ctx context.Context) ([]flyerRowView, error) {
	list, err := h.flyers.ListFlyers(ctx, 0)
	if err != nil {
		return nil, err
	}
	rows := make([]flyerRowView, 0, len(list))
	for _, f := range list {
		rows = append(rows, flyerRowViewOf(f, outreach.Outreach{ID: f.OutreachID, WorkshopID: f.WorkshopID}, f.WorkshopSlug, f.WorkshopTitle))
	}
	return rows, nil
}

type flyerRequest struct {
	OutreachID *int64   `json:"outreach_id"`
	Photo      *string  `json:"photo"`
	FocalX     *float64 `json:"focal_x"`
	FocalY     *float64 `json:"focal_y"`
	Template   *string  `json:"template"`
	Tagline    *string  `json:"tagline"`

	Description      *string `json:"description"`
	DescriptionShort *string `json:"description_short"`
	TitlePosition    *string `json:"title_position"`
}

// validateFlyerFields trims the tagline and photo in place and returns the
// first problem found, or "".
func validateFlyerFields(req *flyerRequest) string {
	if req.Template != nil && !outreach.ValidFlyerTemplate(*req.Template) {
		return "unknown template"
	}
	for _, f := range []*float64{req.FocalX, req.FocalY} {
		if f != nil && (math.IsNaN(*f) || *f < 0 || *f > 1) {
			return "focal point must be between 0 and 1"
		}
	}
	if req.Tagline != nil {
		t := strings.TrimSpace(*req.Tagline)
		req.Tagline = &t
		if utf8.RuneCountInString(t) > outreach.MaxTaglineRunes {
			return "tagline must be at most 140 characters"
		}
	}
	if req.Description != nil {
		t := strings.TrimSpace(*req.Description)
		req.Description = &t
		if utf8.RuneCountInString(t) > outreach.MaxFlyerDescriptionRunes {
			return "description must be at most 360 characters"
		}
	}
	if req.DescriptionShort != nil {
		t := strings.TrimSpace(*req.DescriptionShort)
		req.DescriptionShort = &t
		if utf8.RuneCountInString(t) > outreach.MaxFlyerDescriptionShortRunes {
			return "short description must be at most 140 characters"
		}
	}
	if req.TitlePosition != nil && !outreach.ValidFlyerTitlePosition(*req.TitlePosition) {
		return "unknown title position"
	}
	if req.Photo != nil {
		t := strings.TrimSpace(*req.Photo)
		req.Photo = &t
		if t != "" && (len(t) > flyerPhotoMax || !isSafeCoverImage(t)) {
			return "photo must be a same-site path"
		}
	}
	return ""
}

// flyerFieldOf names the request field a flyer validation message is about,
// for the v1 validation envelope.
func flyerFieldOf(msg string) string {
	switch {
	case strings.HasPrefix(msg, "outreach_id"):
		return "outreach_id"
	case strings.HasPrefix(msg, "unknown template"):
		return "template"
	case strings.HasPrefix(msg, "focal"):
		return "focal_x"
	case strings.HasPrefix(msg, "tagline"):
		return "tagline"
	case strings.HasPrefix(msg, "description"):
		return "description"
	case strings.HasPrefix(msg, "short description"):
		return "description_short"
	case strings.HasPrefix(msg, "unknown title position"):
		return "title_position"
	case strings.HasPrefix(msg, "photo"):
		return "photo"
	}
	return ""
}

func flyerBadRequest(msg string) error {
	return outreachRefusal(http.StatusBadRequest, flyerFieldOf(msg), msg)
}

// writeFlyerResult answers an admin flyer request with v or err.
func writeFlyerResult(w http.ResponseWriter, op string, status int, v flyerView, err error) {
	if err != nil {
		writeOutreachError(w, op, err)
		return
	}
	writeJSON(w, status, v)
}

// CreateFlyer handles POST /admin/outreach-flyers.
func (h *AdminOutreachHandler) CreateFlyer(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req flyerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	actorID := actor.ID
	v, err := h.createFlyer(r.Context(), clientIP(r), &actorID, req)
	writeFlyerResult(w, "create flyer", http.StatusCreated, v, err)
}

// createFlyer is CreateFlyer without the transport (#0703).
func (h *AdminOutreachHandler) createFlyer(ctx context.Context, ip string, actorID *int64, req flyerRequest) (flyerView, error) {
	if req.OutreachID == nil || *req.OutreachID <= 0 {
		return flyerView{}, flyerBadRequest("outreach_id is required")
	}
	if msg := validateFlyerFields(&req); msg != "" {
		return flyerView{}, flyerBadRequest(msg)
	}
	if _, err := h.store.GetByID(ctx, *req.OutreachID); errors.Is(err, outreach.ErrNotFound) {
		return flyerView{}, errOutreachNotFound
	} else if err != nil {
		return flyerView{}, err
	}
	in := outreach.FlyerInput{OutreachID: *req.OutreachID, FocalX: 0.5, FocalY: 0.5, Template: "full-photo", TitlePosition: "auto"}
	if req.Template != nil {
		in.Template = *req.Template
	}
	if req.FocalX != nil {
		in.FocalX = *req.FocalX
	}
	if req.FocalY != nil {
		in.FocalY = *req.FocalY
	}
	if req.Tagline != nil {
		in.Tagline = *req.Tagline
	}
	if req.Description != nil {
		in.Description = *req.Description
	}
	if req.DescriptionShort != nil {
		in.DescriptionShort = *req.DescriptionShort
	}
	if req.TitlePosition != nil {
		in.TitlePosition = *req.TitlePosition
	}
	if req.Photo != nil && *req.Photo != "" {
		in.Photo = req.Photo
	}
	f, err := h.flyers.CreateFlyer(ctx, in)
	if err != nil {
		return flyerView{}, err
	}
	h.auditFlyerCtx(ctx, ip, actorID, audit.ActionOutreachFlyerCreated, f.ID, f.OutreachID, map[string]any{"template": f.Template})
	return h.flyerView(ctx, f)
}

// auditFlyerCtx records a flyer action. extra adds to the always-present
// outreach_id; it must never carry tagline or description text or a photo
// path. recordCtx attributes an API-token request to its token.
func (h *AdminOutreachHandler) auditFlyerCtx(ctx context.Context, ip string, actorID *int64, action string, flyerID, outreachID int64, extra map[string]any) {
	meta := map[string]any{"outreach_id": outreachID}
	for k, v := range extra {
		meta[k] = v
	}
	h.recordCtx(ctx, ip, audit.TargetOutreachFlyer, flyerID, audit.Entry{ActorID: actorID, Action: action, Metadata: meta})
}

// GetFlyer handles GET /admin/outreach-flyers/{id}.
func (h *AdminOutreachHandler) GetFlyer(w http.ResponseWriter, r *http.Request) {
	id, ok := flyerPathID(w, r, "flyer")
	if !ok {
		return
	}
	v, err := h.flyerByID(r.Context(), id)
	writeFlyerResult(w, "get flyer", http.StatusOK, v, err)
}

// PatchFlyer handles PATCH /admin/outreach-flyers/{id}: the design fields.
func (h *AdminOutreachHandler) PatchFlyer(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := flyerPathID(w, r, "flyer")
	if !ok {
		return
	}
	var req flyerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	actorID := actor.ID
	v, err := h.patchFlyer(r.Context(), clientIP(r), &actorID, id, req)
	writeFlyerResult(w, "update flyer", http.StatusOK, v, err)
}

// patchFlyer is PatchFlyer without the transport (#0703).
func (h *AdminOutreachHandler) patchFlyer(ctx context.Context, ip string, actorID *int64, id int64, req flyerRequest) (flyerView, error) {
	if req.Photo == nil && req.FocalX == nil && req.FocalY == nil && req.Template == nil && req.Tagline == nil &&
		req.Description == nil && req.DescriptionShort == nil && req.TitlePosition == nil {
		return flyerView{}, flyerBadRequest("no fields to update")
	}
	if msg := validateFlyerFields(&req); msg != "" {
		return flyerView{}, flyerBadRequest(msg)
	}
	before, err := h.getFlyer(ctx, id)
	if err != nil {
		return flyerView{}, err
	}
	p := outreach.FlyerPatch{FocalX: req.FocalX, FocalY: req.FocalY, Template: req.Template, Tagline: req.Tagline,
		Description: req.Description, DescriptionShort: req.DescriptionShort, TitlePosition: req.TitlePosition}
	if req.Photo != nil {
		if *req.Photo == "" {
			p.ClearPhoto = true
		} else {
			p.Photo = req.Photo
		}
	}
	after, err := h.flyers.UpdateFlyer(ctx, id, p)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrFlyerNotFound):
		return flyerView{}, errFlyerNotFound
	default:
		return flyerView{}, err
	}
	if changed := changedFlyerFields(before, after); len(changed) > 0 {
		h.auditFlyerCtx(ctx, ip, actorID, audit.ActionOutreachFlyerUpdated, after.ID, after.OutreachID, map[string]any{"changed": changed})
	}
	return h.flyerView(ctx, after)
}

// changedFlyerFields lists the design fields whose value differs, by name
// only: the audit row never carries tagline or description text or a photo path.
func changedFlyerFields(before, after outreach.Flyer) []string {
	changed := []string{}
	same := func(a, b *string) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
	if !same(before.Photo, after.Photo) {
		changed = append(changed, "photo")
	}
	if before.FocalX != after.FocalX || before.FocalY != after.FocalY {
		changed = append(changed, "focal")
	}
	if before.Template != after.Template {
		changed = append(changed, "template")
	}
	if before.Tagline != after.Tagline {
		changed = append(changed, "tagline")
	}
	if before.Description != after.Description {
		changed = append(changed, "description")
	}
	if before.DescriptionShort != after.DescriptionShort {
		changed = append(changed, "description_short")
	}
	if before.TitlePosition != after.TitlePosition {
		changed = append(changed, "title_position")
	}
	return changed
}

type flyerSelectionItem struct {
	LocationID int64 `json:"location_id" jsonschema:"an outreach location id"`
	Copies     int   `json:"copies" jsonschema:"copies to print, 1-50"`
}

type flyerSelectionRequest struct {
	Locations []flyerSelectionItem `json:"locations"`
}

// PutFlyerLocations handles PUT /admin/outreach-flyers/{id}/locations:
// replace the flyer's location selection.
func (h *AdminOutreachHandler) PutFlyerLocations(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := flyerPathID(w, r, "flyer")
	if !ok {
		return
	}
	var req flyerSelectionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	actorID := actor.ID
	v, err := h.putFlyerLocations(r.Context(), clientIP(r), &actorID, id, req.Locations)
	writeFlyerResult(w, "set flyer locations", http.StatusOK, v, err)
}

// putFlyerLocations is PutFlyerLocations without the transport (#0703).
func (h *AdminOutreachHandler) putFlyerLocations(ctx context.Context, ip string, actorID *int64, id int64, locs []flyerSelectionItem) (flyerView, error) {
	if len(locs) > outreach.MaxFlyerLocations {
		return flyerView{}, outreachRefusal(http.StatusBadRequest, "locations", fmt.Sprintf("a flyer can have at most %d locations", outreach.MaxFlyerLocations))
	}
	sel := make([]outreach.SelectionInput, 0, len(locs))
	for _, l := range locs {
		sel = append(sel, outreach.SelectionInput{LocationID: l.LocationID, Copies: l.Copies})
	}
	f, err := h.getFlyer(ctx, id)
	if err != nil {
		return flyerView{}, err
	}
	switch err := h.flyers.ReplaceFlyerSelection(ctx, id, sel); {
	case err == nil:
	case errors.Is(err, outreach.ErrFlyerNotFound):
		return flyerView{}, errFlyerNotFound
	case errors.Is(err, outreach.ErrLocationInactive):
		return flyerView{}, outreachRefusal(http.StatusBadRequest, "locations", "an inactive location cannot be added")
	case errors.Is(err, outreach.ErrFlyerSelectionInvalid):
		return flyerView{}, outreachRefusal(http.StatusBadRequest, "locations", "each location must be known and used once, with 1–50 copies")
	default:
		return flyerView{}, err
	}
	h.auditFlyerCtx(ctx, ip, actorID, audit.ActionOutreachFlyerUpdated, id, f.OutreachID, map[string]any{"changed": []string{"locations"}, "locations": len(sel)})
	return h.flyerView(ctx, f)
}

// DeleteFlyer handles DELETE /admin/outreach-flyers/{id}. Links stay: they
// belong to the outreach, so other flyers for the same workshop and location
// keep sharing them.
func (h *AdminOutreachHandler) DeleteFlyer(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := flyerPathID(w, r, "flyer")
	if !ok {
		return
	}
	actorID := actor.ID
	if err := h.deleteFlyer(r.Context(), clientIP(r), &actorID, id); err != nil {
		writeOutreachError(w, "delete flyer", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteFlyer is DeleteFlyer without the transport (#0703).
func (h *AdminOutreachHandler) deleteFlyer(ctx context.Context, ip string, actorID *int64, id int64) error {
	f, err := h.getFlyer(ctx, id)
	if err != nil {
		return err
	}
	switch err := h.flyers.DeleteFlyer(ctx, id); {
	case err == nil:
	case errors.Is(err, outreach.ErrFlyerNotFound):
		return errFlyerNotFound
	default:
		return err
	}
	h.auditFlyerCtx(ctx, ip, actorID, audit.ActionOutreachFlyerDeleted, id, f.OutreachID, nil)
	return nil
}

// GenerateFlyer handles POST /admin/outreach-flyers/{id}/generate: one
// tracked link per selected location (reusing any the workshop already has),
// then each link's QR code, fetched once and cached sanitized. It is bounded
// and resumable: on the deadline or an upstream failure it answers 200 with
// what exists, how many locations remain and a generic message (the upstream
// text only reaches the log), and everything done is already saved, so
// calling it again continues. 429 is passed through only when nothing was
// done yet.
func (h *AdminOutreachHandler) GenerateFlyer(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := flyerPathID(w, r, "flyer")
	if !ok {
		return
	}
	actorID := actor.ID
	v, err := h.generateFlyer(r.Context(), clientIP(r), &actorID, id)
	writeFlyerResult(w, "generate flyer links", http.StatusOK, v, err)
}

// generateFlyer is GenerateFlyer without the transport (#0703). A
// ShortLinks failure it passes through is an outreachLinksError.
func (h *AdminOutreachHandler) generateFlyer(ctx context.Context, ip string, actorID *int64, id int64) (flyerView, error) {
	if !h.linksEnabled() {
		return flyerView{}, outreachRefusal(http.StatusServiceUnavailable, "", "ShortLinks is not configured")
	}
	f, err := h.getFlyer(ctx, id)
	if err != nil {
		return flyerView{}, err
	}
	o, err := h.store.GetByID(ctx, f.OutreachID)
	if err != nil {
		return flyerView{}, err
	}
	wk, err := h.workshopFor(ctx, o.WorkshopID)
	if err != nil {
		return flyerView{}, err
	}
	if wk.Status != "published" {
		return flyerView{}, outreachRefusal(http.StatusConflict, "", "flyer links can only be generated for a published event")
	}
	sel, err := h.flyers.ListFlyerSelection(ctx, id)
	if err != nil {
		return flyerView{}, err
	}
	if len(sel) == 0 {
		return flyerView{}, outreachRefusal(http.StatusBadRequest, "locations", "select at least one location first")
	}

	gctx, cancel := context.WithTimeout(ctx, flyerGenerateTimeout)
	defer cancel()
	base, err := h.shortLinksBase(gctx)
	if err != nil {
		return flyerView{}, outreachLinksError{err}
	}

	progress := 0
	stopErr := error(nil)
	for _, s := range sel {
		link := s.Link
		if link == nil {
			l, created, err := h.generateLink(gctx, o, wk, s, base)
			if errors.Is(err, outreach.ErrLocationCodeChanged) {
				return flyerView{}, outreachRefusal(http.StatusConflict, "", "a location's code changed; try again")
			}
			if err != nil {
				stopErr = err
				break
			}
			progress++
			link = &l
			if created {
				h.auditFlyerLinkCtx(ctx, ip, actorID, audit.ActionOutreachFlyerLinkCreated, l.ID, o.ID, s.LocationID, map[string]any{
					"code": s.Code, "short_link_key": l.Key,
				})
			}
		}
		if !link.HasQR {
			if err := h.cacheQR(gctx, *link); err != nil {
				stopErr = err
				break
			}
			progress++
		}
	}

	if stopErr != nil && progress == 0 && errors.Is(stopErr, shortlinks.ErrRateLimited) {
		return flyerView{}, outreachLinksError{stopErr}
	}
	if stopErr != nil {
		slog.Default().Error("admin_outreach: flyer generate stopped", "flyer_id", id, "err", stopErr)
	}

	v, err := h.flyerView(ctx, f)
	if err != nil {
		return flyerView{}, err
	}
	remaining := 0
	for _, s := range v.Selection {
		if s.Link == nil || !s.Link.HasQR {
			remaining++
		}
	}
	v.Remaining = &remaining
	switch {
	case remaining == 0:
	case errors.Is(stopErr, context.DeadlineExceeded) || gctx.Err() != nil:
		v.Message = msgFlyerGenerateTimeout
	default:
		v.Message = msgFlyerGenerateStopped
	}
	return v, nil
}

// auditFlyerLinkCtx records a flyer-link action (#0699); extra adds to the
// always-present outreach_id and location_id. recordCtx attributes an
// API-token request to its token.
func (h *AdminOutreachHandler) auditFlyerLinkCtx(ctx context.Context, ip string, actorID *int64, action string, linkID, outreachID, locationID int64, extra map[string]any) {
	meta := map[string]any{"outreach_id": outreachID, "location_id": locationID}
	for k, v := range extra {
		meta[k] = v
	}
	h.recordCtx(ctx, ip, audit.TargetOutreachFlyerLink, linkID, audit.Entry{ActorID: actorID, Action: action, Metadata: meta})
}

// generateLink creates the ShortLinks link for one (workshop, location) and
// records it. The upstream call comes first because an HTTP call cannot sit
// inside the transaction InsertFlyerLink opens.
func (h *AdminOutreachHandler) generateLink(ctx context.Context, o outreach.Outreach, wk workshops.Workshop, s outreach.FlyerSelection, base string) (outreach.FlyerLink, bool, error) {
	link, err := h.links.CreateLink(ctx, shortlinks.CreateLinkRequest{
		DestinationURL: outreach.FlyerDestinationURL(h.baseURL, wk.Slug, s.Code),
		Title:          wk.Title + " — Flyer · " + s.Name,
		CampaignSlug:   o.CampaignSlug,
		UTMSource:      "flyer",
		UTMMedium:      "print",
		UTMCampaign:    wk.Slug,
		UTMContent:     s.Code,
		Placement:      s.Name,
	})
	if err != nil {
		return outreach.FlyerLink{}, false, err
	}
	return h.flyers.InsertFlyerLink(ctx, o.ID, s.LocationID, s.Code, link.Key, shortlinks.ShortURL(base, link.Key))
}

// cacheQR fetches a link's QR code, rebuilds it through SanitizeQRSVG and
// stores the result. A QR that fails sanitizing is never cached.
func (h *AdminOutreachHandler) cacheQR(ctx context.Context, l outreach.FlyerLink) error {
	raw, err := h.links.LinkQRSVG(ctx, l.Key)
	if err != nil {
		return err
	}
	clean, err := outreach.SanitizeQRSVG(raw)
	if err != nil {
		return err
	}
	return h.flyers.SetFlyerLinkQR(ctx, l.ID, clean)
}

// FlyerLinkQR handles GET /admin/outreach-flyer-links/{id}/qr.svg. The bytes
// were rebuilt from integers by SanitizeQRSVG before they were stored; on top
// of that the response is an attachment with nosniff, so opening the URL
// directly downloads it rather than rendering it in this origin, and the SPA
// only ever loads it through an img element.
func (h *AdminOutreachHandler) FlyerLinkQR(w http.ResponseWriter, r *http.Request) {
	id, ok := flyerPathID(w, r, "flyer link")
	if !ok {
		return
	}
	svg, code, err := h.flyers.FlyerLinkQR(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrFlyerLinkNotFound):
		writeError(w, http.StatusNotFound, "QR code not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "image/svg+xml")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Content-Disposition", `attachment; filename="`+code+`.svg"`)
	hd.Set("Cache-Control", "private, max-age=3600")
	hd.Set("Content-Length", strconv.Itoa(len(svg)))
	_, _ = w.Write(svg)
}

// PutUpFlyerLink handles POST (mark) and DELETE (clear) on
// /admin/outreach-flyer-links/{id}/put-up. Marking keeps the first
// timestamp.
func (h *AdminOutreachHandler) PutUpFlyerLink(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := flyerPathID(w, r, "flyer link")
	if !ok {
		return
	}
	actorID := actor.ID
	after, err := h.setFlyerLinkPutUp(r.Context(), clientIP(r), &actorID, id, r.Method == http.MethodPost)
	if err != nil {
		writeOutreachError(w, "put up flyer link", err)
		return
	}
	writeJSON(w, http.StatusOK, flyerLinkViewOf(after))
}

// setFlyerLinkPutUp is PutUpFlyerLink without the transport (#0703). Only a
// real change is audited.
func (h *AdminOutreachHandler) setFlyerLinkPutUp(ctx context.Context, ip string, actorID *int64, id int64, putUp bool) (outreach.FlyerLink, error) {
	before, err := h.flyers.GetFlyerLink(ctx, id)
	if errors.Is(err, outreach.ErrFlyerLinkNotFound) {
		return outreach.FlyerLink{}, errFlyerLinkNotFound
	} else if err != nil {
		return outreach.FlyerLink{}, err
	}
	after, err := h.flyers.SetPutUp(ctx, id, putUp)
	switch {
	case err == nil:
	case errors.Is(err, outreach.ErrFlyerLinkNotFound):
		return outreach.FlyerLink{}, errFlyerLinkNotFound
	default:
		return outreach.FlyerLink{}, err
	}
	switch {
	case putUp && before.PutUpAt == nil:
		h.auditFlyerLinkCtx(ctx, ip, actorID, audit.ActionOutreachFlyerLinkPutUp, id, after.OutreachID, after.LocationID, nil)
	case !putUp && before.PutUpAt != nil:
		h.auditFlyerLinkCtx(ctx, ip, actorID, audit.ActionOutreachFlyerLinkPutUpCleared, id, after.OutreachID, after.LocationID, nil)
	}
	return after, nil
}
