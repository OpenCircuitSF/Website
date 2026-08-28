// Admin campaign CRUD API (#0041; PRD §5.2, §6.6, §8): create, edit, list,
// and cancel email campaigns, plus the operator-facing "send" transition
// (draft or failed -> scheduled). Backed by internal/mailing.CampaignStore.
//
// # Status transitions this handler enforces
//
// PATCH never changes status — it is content-only (name, subject, preheader,
// body_md, audience_mode, interest_ids), refused with 409 unless the
// campaign is currently draft or scheduled (#0041's acceptance criterion).
// Two dedicated routes own the only two operator-triggered status moves:
//
//	POST /admin/campaigns/{id}/send   draft|failed -> scheduled
//	POST /admin/campaigns/{id}/cancel scheduled|sending|paused_delivery_health -> canceled
//	POST /admin/campaigns/{id}/resume paused_delivery_health -> scheduled (#0124)
//
// scheduled -> sending, sending -> sent/failed, and sending ->
// paused_delivery_health (#0124's circuit breaker, PRD §6.9) belong to
// #0045's send worker and are never written here. Every illegal transition
// attempt is a 409 with a clear message, not a silent no-op — see
// mailing.ErrIllegalStatusTransition's call sites below.
//
// # The advisory Preflight seam (carried in from #0045's plan, 2026-08-21)
//
// #0045's plan requires that the send-transition handler call
// mailing.Preflight and return 409 with {"unmet":[{code,message}]} when it
// fails. mailing.Preflight does not exist yet — it is #0045's own file to
// build (internal/mailing/preflight.go) — so Send depends on the narrow,
// LOCALLY-typed campaignPreflightChecker interface below instead of an
// undefined mailing symbol. See that type's doc comment for exactly what
// #0045 needs to wire in, and CLAUDE.md §9 for why a nil checker here is not
// a compliance gap: the worker's own copy of the gate is the sole
// authoritative enforcement point regardless of whether this advisory one is
// wired up.
//
// # Two distinguishable 409s from Send (carried in from #0047's plan, 2026-08-21)
//
// Send can answer 409 for two structurally different reasons, and the
// compose UI branches on which one it got, so their body shapes must never
// collide: a failed advisory preflight check responds
// {"unmet":[{code,message}]} (unmetPreflightResponse); every other 409 —
// an illegal status transition, a bad audience state, a confirm_count
// mismatch — goes through writeError's {"error":"..."} envelope. Do not
// route a preflight failure through writeError or vice versa.
//
// # The typed send confirmation (wired in by #0044, 2026-08-21)
//
// The compose UI makes the operator type the recipient count before it will
// POST .../send; sendCampaignRequest.ConfirmCount carries that value and
// Send requires it to be present. Since #0044 landed, Send also verifies it:
// campaignAudienceCounter.Preview computes the authoritative count from the
// campaign's own audience_mode/interest_ids (already loaded via
// h.store.GetByID — no second round trip), and a mismatch is a 409 through
// writeError, never the preflight {"unmet":[...]} shape. A nil h.audience
// (test-only) reverts to the pre-#0044 behavior of accepting confirm_count
// unverified.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// campaignStore is the behavior AdminCampaignsHandler needs from the data
// layer. *mailing.CampaignStore (#0041) satisfies it.
type campaignStore interface {
	List(ctx context.Context) ([]mailing.Campaign, error)
	GetByID(ctx context.Context, id int64) (mailing.Campaign, error)
	Create(ctx context.Context, in mailing.CampaignInput) (mailing.Campaign, error)
	Update(ctx context.Context, id int64, in mailing.CampaignUpdate) (mailing.Campaign, error)
	Send(ctx context.Context, id int64, scheduledAt time.Time) (mailing.Campaign, error)
	Cancel(ctx context.Context, id int64) (mailing.Campaign, error)
	// Resume is #0124's circuit-breaker recovery path:
	// paused_delivery_health -> scheduled. See mailing.CampaignStore.Resume's
	// own doc comment for why this is a narrower transition than Send's.
	Resume(ctx context.Context, id int64, scheduledAt time.Time) (mailing.Campaign, error)
}

// campaignPreflightFailure mirrors the eventual shape of #0045's
// mailing.PreflightFailure ({"code":…, "message":…}) so #0047 (the compose
// UI) can be built against a stable response contract before #0045 lands,
// and so #0045's own wiring doesn't have to change this handler's JSON
// shape — only construct something that satisfies campaignPreflightChecker
// below.
type campaignPreflightFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// campaignPreflightChecker is the advisory pre-send gate seam #0045's plan
// (issues/0045.md §2, "Wiring, given the queue order") requires: "#0041's
// send-transition handler calls Preflight and, when it fails, returns 409."
//
// It is deliberately a small LOCAL interface, not one built against
// mailing.Preflight/mailing.PreflightInput/mailing.PreflightResult — those
// types don't exist yet (#0045 is not implemented). Depending on an
// undefined symbol here would either block this issue on #0045 or invent a
// throwaway version of a type #0045's plan already specifies in detail
// (issues/0045.md §2's exact PreflightInput field list) — "a second
// implementation of this gate is worse than none" per the orchestrator's
// instruction carrying this requirement in.
//
// When #0045 lands, it should add a small adapter (in internal/mailing or
// internal/handlers) satisfying this interface by calling its own
// SendStore.GatherPreflight + Preflight and mapping PreflightResult.Failures
// to []campaignPreflightFailure — then pass it as NewAdminCampaignsHandler's
// preflight argument at the cmd/opencircuit/main.go call site. Until then,
// nil is passed (see main.go): Send simply skips this advisory check and
// proceeds straight to the state transition. That is NOT a compliance gap —
// CLAUDE.md §9 and #0045's plan are explicit that the worker's own copy of
// the gate, evaluated immediately before a campaign leaves 'scheduled', is
// the sole authoritative enforcement point. A nil checker only means the
// operator loses the courtesy of finding out before scheduling; a campaign
// that reaches 'scheduled' with an unmet requirement is still demoted back
// to 'draft' by the worker on its next poll (#0045's plan §2).
type campaignPreflightChecker interface {
	// Check returns the campaign's unmet pre-send requirements, if any. A
	// nil/empty slice with a nil error means the campaign may proceed.
	Check(ctx context.Context, campaignID int64) ([]campaignPreflightFailure, error)
}

// campaignPreflightStore is the narrow seam campaignPreflightAdapter needs
// from #0045's own data layer: GatherPreflight assembles the
// mailing.PreflightInput this handler hands to mailing.Preflight.
// *mailing.SendStore satisfies it.
type campaignPreflightStore interface {
	GatherPreflight(ctx context.Context, campaignID int64) (mailing.PreflightInput, error)
}

// campaignPreflightAdapter satisfies campaignPreflightChecker by calling
// #0045's own mailing.Preflight over a mailing.SendStore.GatherPreflight
// read, and mapping mailing.PreflightResult.Failures to
// []campaignPreflightFailure — the adapter campaignPreflightChecker's own
// doc comment asked #0045 to build. This is NOT a second implementation of
// the gate: it calls the exact same pure function the worker's authoritative
// check calls, over the exact same GatherPreflight read; the worker's own
// evaluation (immediately before a campaign leaves 'scheduled') remains the
// sole authoritative enforcement point regardless of whether this advisory
// one is wired in (CLAUDE.md §9).
type campaignPreflightAdapter struct {
	store campaignPreflightStore
}

// NewCampaignPreflightChecker adapts *mailing.SendStore (#0045) to
// campaignPreflightChecker for cmd/opencircuit/main.go's call site. Takes
// the CONCRETE *mailing.SendStore, not the local campaignPreflightStore
// interface, specifically so a nil store produces a genuinely nil
// campaignPreflightChecker return value rather than a non-nil interface
// wrapping a nil pointer — the exact typed-nil trap this type's own doc
// comment warns about: `(*campaignPreflightAdapter)(nil)` would be non-nil
// to Send's `h.preflight != nil` guard and panic inside Check the first
// time it's called. A plain `if store == nil` on a concrete pointer
// parameter has no such trap.
func NewCampaignPreflightChecker(store *mailing.SendStore) campaignPreflightChecker {
	if store == nil {
		return nil
	}
	return &campaignPreflightAdapter{store: store}
}

func (a *campaignPreflightAdapter) Check(ctx context.Context, campaignID int64) ([]campaignPreflightFailure, error) {
	in, err := a.store.GatherPreflight(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	result := mailing.Preflight(in)
	if result.OK() {
		return nil, nil
	}
	failures := make([]campaignPreflightFailure, len(result.Failures))
	for i, f := range result.Failures {
		failures[i] = campaignPreflightFailure{Code: f.Code, Message: f.Message}
	}
	return failures, nil
}

// campaignAudienceCounter is the narrow seam Send uses to compute the
// authoritative recipient count #0044 wires in (carried in from #0041's
// implementation, 2026-08-21 — see this issue's "Carried in from #0041's
// implementation" section). *mailing.AudienceStore satisfies it, sharing
// the exact method AdminCampaignAudienceHandler's campaignAudiencePreviewer
// (admin_campaign_audience.go) uses for the same purpose. Send builds the
// mailing.Audience it passes in directly from the campaign row it already
// loaded via h.store.GetByID (AudienceMode/InterestIDs), so it needs only
// Preview here — not LoadAudience, which would be a second, redundant
// round trip for data Send already has.
type campaignAudienceCounter interface {
	Preview(ctx context.Context, aud mailing.Audience, sampleLimit int) (mailing.AudiencePreview, error)
}

// AdminCampaignsHandler serves the admin-only campaign CRUD routes (PRD
// §5.2/§6.6/§8; #0041):
//
//	GET    /admin/campaigns             — list every campaign, newest first
//	POST   /admin/campaigns             — create a new draft campaign
//	GET    /admin/campaigns/{id}        — one campaign, with its interest_ids
//	PATCH  /admin/campaigns/{id}        — content-only edit (draft/scheduled only)
//	POST   /admin/campaigns/{id}/send   — draft|failed -> scheduled
//	POST   /admin/campaigns/{id}/cancel — scheduled|sending|paused_delivery_health -> canceled
//	POST   /admin/campaigns/{id}/resume — paused_delivery_health -> scheduled (#0124)
//
// All routes MUST be mounted behind middleware.RequireSession then
// middleware.RequireAdmin, exactly like every other admin handler — see
// cmd/opencircuit/main.go's adminRoutes.
type AdminCampaignsHandler struct {
	store     campaignStore
	preflight campaignPreflightChecker // nil until #0045 wires one in — see that type's doc comment
	audience  campaignAudienceCounter  // nil disables confirm_count enforcement — see Send
	auditor   *audit.Logger
}

// NewAdminCampaignsHandler constructs an AdminCampaignsHandler. A nil
// auditor disables audit writes (matching every other admin handler's
// nil-tolerance); a nil preflight disables the advisory pre-send check (see
// campaignPreflightChecker's doc comment). A nil audience disables the
// confirm_count comparison (see Send) — confirm_count is still required and
// shape-validated, but a nil audience means it is accepted unverified, the
// pre-#0044 behavior. Every production call site (cmd/opencircuit/main.go)
// passes a real *mailing.AudienceStore; nil exists only for tests that don't
// exercise Send's confirm_count path.
func NewAdminCampaignsHandler(store campaignStore, preflight campaignPreflightChecker, audience campaignAudienceCounter, auditor *audit.Logger) *AdminCampaignsHandler {
	return &AdminCampaignsHandler{store: store, preflight: preflight, audience: audience, auditor: auditor}
}

// ── Response shapes ──────────────────────────────────────────────────────────

type campaignView struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Subject      string  `json:"subject"`
	Preheader    *string `json:"preheader,omitempty"`
	BodyMD       string  `json:"body_md"`
	Status       string  `json:"status"`
	AudienceMode string  `json:"audience_mode"`
	WorkshopID   *int64  `json:"workshop_id,omitempty"`
	InterestIDs  []int64 `json:"interest_ids"`
	ScheduledAt  *string `json:"scheduled_at,omitempty"`
	StartedAt    *string `json:"started_at,omitempty"`
	CompletedAt  *string `json:"completed_at,omitempty"`
	// TestSentAt is email_campaigns.test_sent_at (migration 000018),
	// surfaced starting with #0046 (the first issue to write it — #0041
	// predates the column and left it out for that reason; see this
	// field's absence noted in #0041's own carried-in review). nil means
	// no test send has ever been delivered, or a subsequent subject/body_md
	// edit cleared it (CampaignStore.Update's own CASE clause).
	TestSentAt *string `json:"test_sent_at,omitempty"`
	CreatedBy  *int64  `json:"created_by,omitempty"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	// Slug, ArchiveStatus, and ArchivedAt back #0123's compose-time
	// "copyable future archive URL" field (PRD §6.8) and the archive
	// toggle's own status display. Slug is never empty — Create mints one
	// at draft time — so it is NOT omitempty, unlike every other field
	// added here; the compose UI depends on it always being present to
	// build the URL immediately after a campaign is created.
	Slug          string  `json:"slug"`
	ArchiveStatus string  `json:"archive_status"`
	ArchivedAt    *string `json:"archived_at,omitempty"`
}

// formatTimePtr is defined in admin_subscribers.go and reused here.

func toCampaignView(c mailing.Campaign) campaignView {
	ids := c.InterestIDs
	if ids == nil {
		ids = []int64{}
	}
	return campaignView{
		ID:            c.ID,
		Name:          c.Name,
		Subject:       c.Subject,
		Preheader:     c.Preheader,
		BodyMD:        c.BodyMD,
		Status:        c.Status,
		AudienceMode:  c.AudienceMode,
		WorkshopID:    c.WorkshopID,
		InterestIDs:   ids,
		ScheduledAt:   formatTimePtr(c.ScheduledAt),
		StartedAt:     formatTimePtr(c.StartedAt),
		CompletedAt:   formatTimePtr(c.CompletedAt),
		TestSentAt:    formatTimePtr(c.TestSentAt),
		CreatedBy:     c.CreatedBy,
		CreatedAt:     c.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:     c.UpdatedAt.UTC().Format(time.RFC3339),
		Slug:          c.Slug,
		ArchiveStatus: c.ArchiveStatus,
		ArchivedAt:    formatTimePtr(c.ArchivedAt),
	}
}

type campaignsListResponse struct {
	Campaigns []campaignView `json:"campaigns"`
}

// campaignAudienceErrorMessage maps the two write-time audience guard errors
// to an operator-facing message, stripping the "mailing: " package prefix
// that mailing.Err* carries internally. Shared by Create and Patch.
func campaignAudienceErrorMessage(err error) string {
	switch {
	case errors.Is(err, mailing.ErrCampaignInterestsRequired):
		return "this audience mode requires at least one interest to be selected"
	case errors.Is(err, mailing.ErrUnknownAudienceMode):
		return "unknown audience mode"
	default:
		return "invalid audience targeting"
	}
}

// ── GET /admin/campaigns ─────────────────────────────────────────────────────

func (h *AdminCampaignsHandler) List(w http.ResponseWriter, r *http.Request) {
	campaigns, err := h.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views := make([]campaignView, 0, len(campaigns))
	for _, c := range campaigns {
		views = append(views, toCampaignView(c))
	}
	writeJSON(w, http.StatusOK, campaignsListResponse{Campaigns: views})
}

// ── GET /admin/campaigns/{id} ────────────────────────────────────────────────

func (h *AdminCampaignsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseCampaignID(w, r)
	if !ok {
		return
	}
	c, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, toCampaignView(c))
}

// ── POST /admin/campaigns ────────────────────────────────────────────────────

type createCampaignRequest struct {
	Name         string  `json:"name"`
	Subject      string  `json:"subject"`
	Preheader    *string `json:"preheader,omitempty"`
	BodyMD       string  `json:"body_md"`
	AudienceMode string  `json:"audience_mode,omitempty"`
	InterestIDs  []int64 `json:"interest_ids,omitempty"`
}

// Create handles POST /admin/campaigns. Always creates in status='draft'
// (mailing.CampaignStore.Create's own contract). audience_mode defaults to
// "any_of" when omitted, matching migration 000017's column default, so a
// client that only ever sets interest_ids doesn't also have to spell out the
// mode. Rejects an empty interest set for any_of/all_of at write time — the
// carried-in guard from #0044's plan.
func (h *AdminCampaignsHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	var req createCampaignRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	subject := strings.TrimSpace(req.Subject)
	if subject == "" {
		writeError(w, http.StatusBadRequest, "subject is required")
		return
	}
	if strings.TrimSpace(req.BodyMD) == "" {
		writeError(w, http.StatusBadRequest, "body_md is required")
		return
	}
	mode := req.AudienceMode
	if mode == "" {
		mode = mailing.AudienceAnyOf
	}
	preheader := normalizeOptionalCampaignField(req.Preheader)

	actorID := actor.ID
	created, err := h.store.Create(r.Context(), mailing.CampaignInput{
		Name:         name,
		Subject:      subject,
		Preheader:    preheader,
		BodyMD:       req.BodyMD,
		AudienceMode: mode,
		InterestIDs:  req.InterestIDs,
		CreatedBy:    &actorID,
	})
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrUnknownAudienceMode), errors.Is(err, mailing.ErrCampaignInterestsRequired):
		writeError(w, http.StatusBadRequest, campaignAudienceErrorMessage(err))
		return
	case errors.Is(err, mailing.ErrCampaignInterestNotFound):
		writeError(w, http.StatusBadRequest, "one or more interest ids do not exist")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := created.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionEmailCampaignCreated,
			TargetType: audit.TargetEmailCampaign,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"name":          created.Name,
				"subject":       created.Subject,
				"audience_mode": created.AudienceMode,
				"interest_ids":  created.InterestIDs,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusCreated, toCampaignView(created))
}

// normalizeOptionalCampaignField applies the same "empty string means unset"
// convention AdminInterestsHandler.Patch uses for description: nil stays
// nil, an explicit empty (or whitespace-only) string is treated as absent
// (stored NULL) — matching parseOptionalTime's blank check. A non-blank
// value is returned unchanged, leading/trailing whitespace and all: this
// only decides whether the field counts as "cleared," it does not
// otherwise normalize real content.
func normalizeOptionalCampaignField(v *string) *string {
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil
	}
	return v
}

// ── PATCH /admin/campaigns/{id} ──────────────────────────────────────────────

// patchCampaignRequest is the PATCH /admin/campaigns/{id} body. Every field
// is optional; a nil pointer leaves that field unchanged. There is
// deliberately NO status field — PATCH never transitions status; see this
// file's package doc comment for why Send/Cancel are separate routes.
type patchCampaignRequest struct {
	Name         *string  `json:"name,omitempty"`
	Subject      *string  `json:"subject,omitempty"`
	Preheader    *string  `json:"preheader,omitempty"`
	BodyMD       *string  `json:"body_md,omitempty"`
	AudienceMode *string  `json:"audience_mode,omitempty"`
	InterestIDs  *[]int64 `json:"interest_ids,omitempty"`
}

// Patch handles PATCH /admin/campaigns/{id}: loads the current row, merges
// the provided fields onto it, and calls Update with the full merged value —
// mirrors AdminInterestsHandler.Patch's own merge-then-call-Update shape.
// Refused with 409 (mailing.ErrCampaignNotEditable) unless the campaign is
// currently draft or scheduled.
func (h *AdminCampaignsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseCampaignID(w, r)
	if !ok {
		return
	}

	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	var req patchCampaignRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := current.Name
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "name cannot be empty")
			return
		}
		name = trimmed
	}
	subject := current.Subject
	if req.Subject != nil {
		trimmed := strings.TrimSpace(*req.Subject)
		if trimmed == "" {
			writeError(w, http.StatusBadRequest, "subject cannot be empty")
			return
		}
		subject = trimmed
	}
	preheader := current.Preheader
	if req.Preheader != nil {
		preheader = normalizeOptionalCampaignField(req.Preheader)
	}
	bodyMD := current.BodyMD
	if req.BodyMD != nil {
		if strings.TrimSpace(*req.BodyMD) == "" {
			writeError(w, http.StatusBadRequest, "body_md cannot be empty")
			return
		}
		bodyMD = *req.BodyMD
	}
	audienceMode := current.AudienceMode
	if req.AudienceMode != nil {
		audienceMode = *req.AudienceMode
	}
	interestIDs := current.InterestIDs
	if req.InterestIDs != nil {
		interestIDs = *req.InterestIDs
	}

	updated, err := h.store.Update(r.Context(), id, mailing.CampaignUpdate{
		Name:         name,
		Subject:      subject,
		Preheader:    preheader,
		BodyMD:       bodyMD,
		AudienceMode: audienceMode,
		InterestIDs:  interestIDs,
	})
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	case errors.Is(err, mailing.ErrCampaignNotEditable):
		writeError(w, http.StatusConflict, "a campaign can only be edited while draft or scheduled")
		return
	case errors.Is(err, mailing.ErrUnknownAudienceMode), errors.Is(err, mailing.ErrCampaignInterestsRequired):
		writeError(w, http.StatusBadRequest, campaignAudienceErrorMessage(err))
		return
	case errors.Is(err, mailing.ErrCampaignInterestNotFound):
		writeError(w, http.StatusBadRequest, "one or more interest ids do not exist")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionEmailCampaignUpdated,
			TargetType: audit.TargetEmailCampaign,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"old_subject":       current.Subject,
				"new_subject":       updated.Subject,
				"old_audience_mode": current.AudienceMode,
				"new_audience_mode": updated.AudienceMode,
				"old_interest_ids":  current.InterestIDs,
				"new_interest_ids":  updated.InterestIDs,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, toCampaignView(updated))
}

// ── POST /admin/campaigns/{id}/send ──────────────────────────────────────────

type sendCampaignRequest struct {
	// ConfirmCount is the recipient count the operator typed to confirm the
	// send — carried in from #0047's plan (2026-08-21, "typed confirmation
	// enforced only in the browser is theatre"). Required: a request with no
	// confirm_count (or a negative one) is a 400. #0041 lands before #0044
	// (the audience count), so there is nothing yet to compare it AGAINST —
	// see the TODO in Send below for exactly where that comparison must be
	// wired in once #0044 ships.
	ConfirmCount *int64 `json:"confirm_count"`
	// ScheduledAt is RFC 3339. Omitted means "send as soon as the worker's
	// next poll sees it", implemented as time.Now(): #0045's worker claims
	// campaigns WHERE scheduled_at <= now(), so "now" is the send-immediately
	// value, not a sentinel the worker has to special-case.
	ScheduledAt *string `json:"scheduled_at,omitempty"`
}

type unmetPreflightResponse struct {
	Unmet []campaignPreflightFailure `json:"unmet"`
}

// Send handles POST /admin/campaigns/{id}/send: the operator-initiated
// transition into the send pipeline. Accepts a campaign currently draft OR
// failed (a retry — #0045's plan confirms the worker's drain resumes a
// re-queued campaign safely and idempotently); anything else is a 409.
//
// Runs the advisory preflight check (if one is wired in — see
// campaignPreflightChecker's doc comment) before attempting the transition,
// so an operator sees the unmet-requirements list without the campaign ever
// touching 'scheduled'.
func (h *AdminCampaignsHandler) Send(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseCampaignID(w, r)
	if !ok {
		return
	}

	var req sendCampaignRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ConfirmCount == nil {
		writeError(w, http.StatusBadRequest, "confirm_count is required")
		return
	}
	if *req.ConfirmCount < 0 {
		writeError(w, http.StatusBadRequest, "confirm_count must not be negative")
		return
	}

	scheduledAt := time.Now()
	if req.ScheduledAt != nil && strings.TrimSpace(*req.ScheduledAt) != "" {
		parsed, perr := time.Parse(time.RFC3339, strings.TrimSpace(*req.ScheduledAt))
		if perr != nil {
			writeError(w, http.StatusBadRequest, "scheduled_at must be RFC 3339")
			return
		}
		scheduledAt = parsed
	}

	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// The typed send confirmation (#0047's plan, wired in by #0044): compare
	// the operator's confirm_count against the authoritative audience count,
	// computed from the campaign row already loaded above rather than a
	// second LoadAudience round trip. A mismatch is a plain {"error":"..."}
	// 409 — deliberately NOT the {"unmet":[...]} preflight shape below,
	// which #0047 branches on to distinguish "the count you typed is stale"
	// from "this campaign isn't ready to send". h.audience == nil (test-only
	// — see NewAdminCampaignsHandler's doc comment) skips this check
	// entirely, leaving confirm_count accepted-but-unverified, the
	// pre-#0044 behavior.
	if h.audience != nil {
		aud := mailing.Audience{Mode: current.AudienceMode, InterestIDs: current.InterestIDs}
		preview, aerr := h.audience.Preview(r.Context(), aud, 0)
		switch {
		case aerr == nil:
			if preview.Count != *req.ConfirmCount {
				writeError(w, http.StatusConflict, fmt.Sprintf(
					"confirm_count does not match the current audience count (%d); reload and try again", preview.Count))
				return
			}
		case errors.Is(aerr, mailing.ErrUnknownAudienceMode), errors.Is(aerr, mailing.ErrAudienceInterestsRequired):
			// The campaign's own audience targeting is invalid. Don't
			// duplicate that error mapping here — h.store.Send below runs
			// the identical defensive normalizeAudience check and reports
			// it the usual way (ErrUnknownAudienceMode/
			// ErrCampaignInterestsRequired -> 409 via
			// campaignAudienceErrorMessage).
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}

	// Only run the advisory gate when the campaign is actually a legal
	// source for Send — otherwise report the transition error, not a
	// preflight failure list computed against a campaign that can't send
	// from here regardless.
	fromStatus := current.Status
	if h.preflight != nil && (fromStatus == mailing.CampaignStatusDraft || fromStatus == mailing.CampaignStatusFailed) {
		failures, perr := h.preflight.Check(r.Context(), id)
		if perr != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		if len(failures) > 0 {
			writeJSON(w, http.StatusConflict, unmetPreflightResponse{Unmet: failures})
			return
		}
	}

	updated, err := h.store.Send(r.Context(), id, scheduledAt)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	case errors.Is(err, mailing.ErrIllegalStatusTransition):
		writeError(w, http.StatusConflict, "this campaign cannot be sent from its current status; only a draft or a failed campaign can be scheduled to send")
		return
	case errors.Is(err, mailing.ErrCampaignScheduleTimeRequired):
		writeError(w, http.StatusBadRequest, "scheduled_at is required")
		return
	case errors.Is(err, mailing.ErrUnknownAudienceMode), errors.Is(err, mailing.ErrCampaignInterestsRequired):
		writeError(w, http.StatusConflict, campaignAudienceErrorMessage(err))
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := updated.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionEmailCampaignScheduled,
			TargetType: audit.TargetEmailCampaign,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"from_status":  fromStatus,
				"scheduled_at": scheduledAt.UTC().Format(time.RFC3339),
				"retry":        fromStatus == mailing.CampaignStatusFailed,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, toCampaignView(updated))
}

// ── POST /admin/campaigns/{id}/cancel ────────────────────────────────────────

// Cancel handles POST /admin/campaigns/{id}/cancel: stops a scheduled or
// sending campaign. It never recalls an already-sent message — cancelling a
// 'sending' campaign only stops #0045's worker from claiming further queued
// rows on its next poll (PRD §8's note, restated in #0041's Notes).
func (h *AdminCampaignsHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseCampaignID(w, r)
	if !ok {
		return
	}

	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	canceled, err := h.store.Cancel(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	case errors.Is(err, mailing.ErrIllegalStatusTransition):
		writeError(w, http.StatusConflict, "this campaign cannot be cancelled from its current status; only a scheduled or sending campaign can be cancelled")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := canceled.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionEmailCampaignCanceled,
			TargetType: audit.TargetEmailCampaign,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"from_status": current.Status,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, toCampaignView(canceled))
}

// ── POST /admin/campaigns/{id}/resume ────────────────────────────────────────

// resumeCampaignRequest is POST /admin/campaigns/{id}/resume's body.
// ConfirmSubject is #0124's typed confirmation, "requiring typed
// confirmation like #0045's send confirmation" (this issue's acceptance
// criterion) — but Resume has no audience count to re-verify (the audience
// was already materialized before the breaker tripped), so it borrows
// #0060's Erase pattern instead: the operator types the campaign's own
// subject back, the same "type the resource's identifying text to confirm"
// shape GitHub-like consoles use for a consequential action, rather than a
// bare boolean a client could send with no evidence an operator saw which
// campaign — and which bounce/complaint rate — they were about to restart.
type resumeCampaignRequest struct {
	ConfirmSubject string `json:"confirm_subject"`
}

// Resume handles POST /admin/campaigns/{id}/resume (#0124, PRD §6.9): the
// only way to un-trip the delivery-health circuit breaker. Accepts a
// campaign currently paused_delivery_health only — deliberately narrower
// than Send's draft-or-failed set, since this route exists to answer one
// specific question (see mailing.CampaignStore.Resume's own doc comment).
//
// The breaker is NOT bypassed by resuming: CampaignStore.Resume moves the
// campaign back to 'scheduled', and the worker's ordinary claim picks it up
// from there — checkDeliveryHealth evaluates the same cumulative rate again
// on the very next batch, so a campaign resumed into a still-bad rate trips
// again rather than draining unchecked. This is the acceptance criterion
// "the breaker is not bypassable from the UI" made concrete: there is no
// request shape this handler accepts that skips the worker's own check.
func (h *AdminCampaignsHandler) Resume(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseCampaignID(w, r)
	if !ok {
		return
	}

	var req resumeCampaignRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	confirmSubject := strings.TrimSpace(req.ConfirmSubject)
	if confirmSubject == "" {
		writeError(w, http.StatusBadRequest, "confirm_subject is required")
		return
	}

	// Loaded (and its subject compared against the typed confirmation)
	// BEFORE calling Resume — same ordering Erase uses, and for the same
	// reason: a 400 for a mistyped confirmation must not have mutated
	// anything.
	current, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if confirmSubject != strings.TrimSpace(current.Subject) {
		writeError(w, http.StatusBadRequest, "confirm_subject does not match this campaign's subject")
		return
	}
	if current.Status != mailing.CampaignStatusPausedDeliveryHealth {
		writeError(w, http.StatusConflict, "this campaign cannot be resumed from its current status; only a campaign paused by the delivery-health circuit breaker can be resumed")
		return
	}

	resumed, err := h.store.Resume(r.Context(), id, time.Now())
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	case errors.Is(err, mailing.ErrIllegalStatusTransition):
		writeError(w, http.StatusConflict, "this campaign cannot be resumed from its current status; only a campaign paused by the delivery-health circuit breaker can be resumed")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := resumed.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionEmailCampaignResumed,
			TargetType: audit.TargetEmailCampaign,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"from_status": current.Status,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, toCampaignView(resumed))
}

// ── shared helpers ────────────────────────────────────────────────────────────

// parseCampaignID reads and validates the {id} path value, writing a 400 and
// returning ok=false on a missing/invalid id.
func parseCampaignID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid campaign id")
		return 0, false
	}
	return id, true
}
