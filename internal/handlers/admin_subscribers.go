// Admin subscribers screen (PRD §5.2, §6.2; #0032): search, filter by status
// and interest, inspect one subscriber's consent evidence and event history,
// manually suppress, and manually add (still gated by double opt-in).
//
// # Reusing SubscribeHandler's unexported dispatch, not reimplementing it
//
// The manual-add flow (Create, below) must end up in exactly the same states
// a public signup can: a brand-new address goes to `pending` with a
// confirmation email queued, an existing `active` address gets the
// "already subscribed" notice, `unsubscribed`/`bounced` restarts double
// opt-in, and `complained` never auto-resubscribes (CLAUDE.md §9). That
// dispatch — newSignup/existingSignup/restartSignup — already lives in
// subscribe.go (#0026), reviewed repeatedly for exactly the failure modes a
// second implementation would risk reintroducing (the once-per-hour resend
// claim, the async-send timing-oracle fix, the complained guard). Rather
// than duplicate it, Create holds a reference to the same *SubscribeHandler
// instance main.go constructs and calls its unexported methods directly —
// legal and safe because this file lives in the same package (handlers) and
// touches none of subscribe.go's own source. See #0032's Carried-in
// criteria (issues/0032.md) for why AdminClearComplaint and Unsubscribe's
// no-op-on-complained behavior specifically shape Suppress and ClearComplaint
// below.
//
// # What "suppress" means, since #0033
//
// PRD §6.2 describes a dedicated `suppressions` table — keyed by email,
// surviving resubscribe, checked before every send. #0033 built it
// (subscribers.SuppressionStore). POST /admin/subscribers/{id}/suppress now
// does both sanctioned things: calls subscribers.Store.Unsubscribe with
// source=admin (idempotent, already hardened against complained-laundering
// per #0025's review) AND writes a real subscribers.NewSuppression row
// (reason=manual) via the suppressions field below — so a future resignup by
// this address is blocked at #0026's suppressed send gate, not just marked
// unsubscribed. POST /admin/subscribers/{id}/clear-complaint is the inverse:
// it removes the `complaint` suppression row only; any other reason (for
// example a hard bounce) survives (#0100) — a single address can carry more
// than one independent suppression row since #0100's reason-scoped model,
// and clearing one must not silently discard another. See
// ActionSubscriberSuppressed's and
// ActionSubscriberComplaintCleared's doc comments (internal/audit/actions.go)
// for how the audit trail records this.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// adminSubscriberStore is the behavior AdminSubscribersHandler needs from
// internal/subscribers. Depending on an interface (rather than the concrete
// *subscribers.Store) mirrors interestStore/adminUserStore's pattern
// elsewhere in this package.
type adminSubscriberStore interface {
	List(ctx context.Context, filter subscribers.ListFilter) ([]subscribers.Subscriber, int64, error)
	StatusCounts(ctx context.Context) (map[string]int64, error)
	GetByID(ctx context.Context, id int64) (subscribers.Subscriber, error)
	FindByEmail(ctx context.Context, email string) (subscribers.Subscriber, error)
	InterestIDs(ctx context.Context, subscriberID int64) ([]int64, error)
	Unsubscribe(ctx context.Context, id int64, source string, now time.Time) (subscribers.Subscriber, error)
	AdminClearComplaint(ctx context.Context, id int64, now time.Time) (subscribers.Subscriber, error)
	// StreamExport backs GET /admin/subscribers/export (#0059) — see
	// admin_subscribers_export.go and internal/subscribers/export.go's
	// package doc comment for why this is a separate streaming query rather
	// than a reuse of List.
	StreamExport(ctx context.Context, filter subscribers.ListFilter, fn func(subscribers.ExportRow) error) error
	// Erase backs DELETE /admin/subscribers/{id} (#0060) — see
	// internal/subscribers/erase.go's package doc comment for what it does
	// and why.
	Erase(ctx context.Context, id int64, now time.Time) (subscribers.ErasureResult, error)
	// RecordEvent writes a subscriber_events row (#0126) — this handler's
	// two admin_edited writes (ClearComplaint and Create's manual add; see
	// #0126's plan §9 item 9).
	RecordEvent(ctx context.Context, e subscribers.Event) error
	// EventHistory backs the subscriber detail drawer's event history
	// (#0126, #0032's own criterion): newest first, bounded to 100 rows.
	EventHistory(ctx context.Context, subscriberID int64) ([]subscribers.SubscriberEventRow, error)
}

// adminInterestByIDReader is the behavior AdminSubscribersHandler needs from
// internal/interests to resolve a subscriber's selected interest ids to
// their slug/name for the detail view. *interests.Store satisfies it via
// GetByIDs.
type adminInterestByIDReader interface {
	GetByIDs(ctx context.Context, ids []int64) ([]interests.Interest, error)
}

// adminSuppressionWriter is the behavior AdminSubscribersHandler needs from
// subscribers.SuppressionStore (#0033, widened by #0100): writing a real
// suppression row on Suppress, removing the `complaint` row (and only that
// row) on ClearComplaint, and listing the survivors so ClearComplaint's
// response/audit can say which reasons still block the address.
// *subscribers.SuppressionStore satisfies it directly.
type adminSuppressionWriter interface {
	Add(ctx context.Context, in subscribers.NewSuppression, now time.Time) (subscribers.Suppression, error)
	Remove(ctx context.Context, email, reason string) (subscribers.Suppression, error)
	ListByEmail(ctx context.Context, email string) ([]subscribers.Suppression, error)
}

// AdminSubscribersHandler serves the admin-only subscriber routes (PRD
// §5.2; #0032):
//
//	GET  /admin/subscribers                        — paginated, filterable, searchable list + status counts
//	GET  /admin/subscribers/{id}                    — detail: consent evidence, interests, email event history (empty until #0038)
//	POST /admin/subscribers/{id}/suppress            — manual suppress (required note); see the package doc comment on what this does
//	POST /admin/subscribers/{id}/clear-complaint     — the sole sanctioned exit from `complained` (subscribers.Store.AdminClearComplaint)
//	POST /admin/subscribers                          — manual add; still requires double opt-in confirmation
//	GET  /admin/subscribers/export                   — streaming CSV export, audited (#0059; admin_subscribers_export.go)
//	DELETE /admin/subscribers/{id}                   — GDPR/CCPA erasure: hard delete + permanent suppression (#0060; see Erase below)
//
// All routes MUST be mounted behind middleware.RequireSession then
// middleware.RequireAdmin, exactly like the other admin handlers in this
// package — see cmd/opencircuit/main.go's adminRoutes.
type AdminSubscribersHandler struct {
	store     adminSubscriberStore
	interests adminInterestByIDReader
	// manualAdd is the *SubscribeHandler whose unexported newSignup /
	// existingSignup / resolveInterestIDs Create calls directly — see the
	// package doc comment. May be nil (STORAGE=json dev mode has no
	// subscriber-table backing, matching subscribeH's own nil-guard in
	// mountAndServe); Create returns 503 rather than nil-dereferencing.
	manualAdd *SubscribeHandler
	// suppressions is subscribers.SuppressionStore (#0033), used by Suppress
	// (Add) and ClearComplaint (Remove) — see the package doc comment. May be
	// nil (same STORAGE=json gap as manualAdd); both callers skip the
	// suppressions-table write/removal when nil rather than dereferencing it,
	// matching the rest of this handler's nil-tolerance.
	suppressions adminSuppressionWriter
	// settings resolves #0039's soft_bounce_threshold_count for the
	// streak/threshold pair Get renders (#0124: SoftBounceStreak itself is
	// always populated straight off the subscribers row — see
	// toSubscriberView — so this is only needed for the threshold half). May
	// be nil (STORAGE=json dev mode); Get omits SoftBounceThreshold when nil
	// rather than dereferencing it.
	settings softBounceSettingsReader
	// auditor records subscriber.suppressed / .complaint_cleared /
	// .manual_add. May be nil in tests that don't assert audit rows.
	auditor *audit.Logger
	// now is injectable so timestamps are deterministic in tests; defaults
	// to time.Now.
	now func() time.Time
}

// NewAdminSubscribersHandler constructs an AdminSubscribersHandler. A nil
// manualAdd disables POST /admin/subscribers (Create returns 503); a nil
// suppressions skips the suppressions-table write/removal in Suppress/
// ClearComplaint (they still perform their subscribers-table effect); a nil
// settings disables #0124's SoftBounceThreshold field on the detail view
// (SoftBounceStreak itself is always populated regardless — see
// toSubscriberView); a nil auditor disables audit writes.
func NewAdminSubscribersHandler(store adminSubscriberStore, il adminInterestByIDReader, manualAdd *SubscribeHandler, suppressions adminSuppressionWriter, settings softBounceSettingsReader, auditor *audit.Logger) *AdminSubscribersHandler {
	return &AdminSubscribersHandler{
		store: store, interests: il, manualAdd: manualAdd, suppressions: suppressions,
		settings: settings, auditor: auditor, now: time.Now,
	}
}

// ── Response shapes ──────────────────────────────────────────────────────────

// interestRef is a subscriber's selected interest, reduced to what the
// admin screen needs to render it (id for the filter dropdown, slug+name for
// display).
type interestRef struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// subscriberView is the JSON shape for one subscribers row. Deliberately
// limited to what #0075's published privacy policy discloses subscribers
// their data includes (PrivacyPolicy.svelte "What we collect": email,
// interests, signup IP + timestamp, user agent, the UTM triple, and the
// confirmation timestamp) plus status and the unsubscribe record the same
// policy's "How long we keep it" section already describes ("we keep your
// record marked unsubscribed"). confirm_token and manage_token are NEVER
// included — they are live credentials (an unsubscribe/preference-center
// bearer token), not display data, and #0032's acceptance criteria does not
// ask for them. confirm_sent_at/confirm_expires_at/already_subscribed_sent_at
// (send-cooldown bookkeeping) are also omitted: they are neither disclosed
// to the subscriber nor needed for #0032's stated admin tasks (search,
// filter, inspect consent evidence, suppress, clear a complaint) — see
// #0032's Verification notes if a later reviewer wants them added.
//
// EmailEvents is always present but empty until #0038 (SES bounce/complaint
// ingestion into email_events) lands — see this issue's Notes: "build the
// section now and let it fill in."
//
// SoftBounceStreak/SoftBounceThreshold are #0039's "admin can see the
// current soft-bounce count" criterion, corrected by #0124 to describe the
// STREAK, not a windowed count: #0039/#0109's rolling-window rule
// (soft_bounce_count/soft_bounce_window_days) is retired — see
// issues/0124.md's Notes and PRD §6.9. SoftBounceStreak is
// subscribers.soft_bounce_streak read directly off the row (no query, no
// dependency — it's always populated, on List as well as Get, unlike the
// fields it replaces). SoftBounceThreshold is still populated by Get only,
// since resolving it needs a settings read (softBounceSettingsReader) that
// List has no reason to pay for every row.
type subscriberView struct {
	ID                  int64                 `json:"id"`
	Email               string                `json:"email"`
	Status              string                `json:"status"`
	SignupIP            *string               `json:"signup_ip,omitempty"`
	SignupUserAgent     *string               `json:"signup_user_agent,omitempty"`
	UTMSource           *string               `json:"utm_source,omitempty"`
	UTMMedium           *string               `json:"utm_medium,omitempty"`
	UTMCampaign         *string               `json:"utm_campaign,omitempty"`
	CreatedAt           string                `json:"created_at"` // signup timestamp
	ConfirmedAt         *string               `json:"confirmed_at,omitempty"`
	UnsubscribedAt      *string               `json:"unsubscribed_at,omitempty"`
	UnsubscribeSource   *string               `json:"unsubscribe_source,omitempty"`
	Interests           []interestRef         `json:"interests,omitempty"`             // populated by Get only
	EmailEvents         []any                 `json:"email_events"`                    // always [] until #0038
	SoftBounceStreak    int                   `json:"soft_bounce_streak"`              // always populated (#0124)
	LastBounceAt        *string               `json:"last_bounce_at,omitempty"`        // always populated (#0124)
	LastDeliveryAt      *string               `json:"last_delivery_at,omitempty"`      // always populated (#0124)
	SoftBounceThreshold *int                  `json:"soft_bounce_threshold,omitempty"` // populated by Get only
	Events              []subscriberEventView `json:"events,omitempty"`                // populated by Get only (#0126)
	// Source, SourceDetail, ConsentBasis, ImportID are #0125's provenance
	// fields (PRD §6.10) — always populated (List as well as Get, like
	// SoftBounceStreak above; no extra query needed, they're columns on the
	// row itself). ImportID is the "originating import" GET
	// /admin/subscribers/{id}'s acceptance criterion asks for: the id an
	// admin can act on via POST /admin/imports/{id}/revoke. InvitedAt is
	// #0129's marker (always null until that issue lands a producer).
	Source       string  `json:"source"`
	SourceDetail *string `json:"source_detail,omitempty"`
	ConsentBasis *string `json:"consent_basis,omitempty"`
	ImportID     *int64  `json:"import_id,omitempty"`
	InvitedAt    *string `json:"invited_at,omitempty"`
}

// subscriberEventView is one subscriber_events row (#0126, PRD §6.11),
// newest first, bounded to subscribers.Store.EventHistory's own 100-row
// limit (no pagination — matching that method's own doc comment).
type subscriberEventView struct {
	Action     string          `json:"action"`
	CreatedAt  string          `json:"created_at"`
	CampaignID *int64          `json:"campaign_id,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func toSubscriberView(sub subscribers.Subscriber, refs []interestRef) subscriberView {
	return subscriberView{
		ID:                sub.ID,
		Email:             sub.Email,
		Status:            sub.Status,
		SignupIP:          sub.SignupIP,
		SignupUserAgent:   sub.SignupUserAgent,
		UTMSource:         sub.UTMSource,
		UTMMedium:         sub.UTMMedium,
		UTMCampaign:       sub.UTMCampaign,
		CreatedAt:         sub.CreatedAt.UTC().Format(time.RFC3339),
		ConfirmedAt:       formatTimePtr(sub.ConfirmedAt),
		UnsubscribedAt:    formatTimePtr(sub.UnsubscribedAt),
		UnsubscribeSource: sub.UnsubscribeSource,
		Interests:         refs,
		EmailEvents:       []any{},
		SoftBounceStreak:  sub.SoftBounceStreak,
		LastBounceAt:      formatTimePtr(sub.LastBounceAt),
		LastDeliveryAt:    formatTimePtr(sub.LastDeliveryAt),
		Source:            sub.Source,
		SourceDetail:      sub.SourceDetail,
		ConsentBasis:      sub.ConsentBasis,
		ImportID:          sub.ImportID,
		InvitedAt:         formatTimePtr(sub.InvitedAt),
	}
}

// intPtr is a small helper so Get can set SoftBounceThreshold from a local
// int without a named variable.
func intPtr(n int) *int { return &n }

// statusCountsView is the {pending, active, unsubscribed, bounced,
// complained} header block #0032's acceptance criteria requires at the top
// of the list screen.
type statusCountsView struct {
	Pending      int64 `json:"pending"`
	Active       int64 `json:"active"`
	Unsubscribed int64 `json:"unsubscribed"`
	Bounced      int64 `json:"bounced"`
	Complained   int64 `json:"complained"`
}

func toStatusCountsView(counts map[string]int64) statusCountsView {
	return statusCountsView{
		Pending:      counts[subscribers.StatusPending],
		Active:       counts[subscribers.StatusActive],
		Unsubscribed: counts[subscribers.StatusUnsubscribed],
		Bounced:      counts[subscribers.StatusBounced],
		Complained:   counts[subscribers.StatusComplained],
	}
}

type subscribersListResponse struct {
	Subscribers []subscriberView `json:"subscribers"`
	Total       int64            `json:"total"`
	Page        int              `json:"page"`
	PerPage     int              `json:"per_page"`
	Counts      statusCountsView `json:"counts"`
}

// ── GET /admin/subscribers ───────────────────────────────────────────────────

func validSubscriberStatus(s string) bool {
	switch s {
	case subscribers.StatusPending, subscribers.StatusActive, subscribers.StatusUnsubscribed,
		subscribers.StatusBounced, subscribers.StatusComplained:
		return true
	default:
		return false
	}
}

// List handles GET /admin/subscribers?status=&interest_id=&q=&page=&per_page=.
// status and interest_id are optional filters (400 on an unrecognized status
// or a non-positive interest_id); q is a case-insensitive email substring
// search. Always returns status counts across the WHOLE table, not just the
// current filter/page, so the header block stays stable while an admin
// filters.
func (h *AdminSubscribersHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	status := q.Get("status")
	if status != "" && !validSubscriberStatus(status) {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}

	var interestID int64
	if raw := q.Get("interest_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid interest_id")
			return
		}
		interestID = id
	}

	page := 1
	if raw := q.Get("page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid page")
			return
		}
		page = n
	}

	perPage := 25
	if raw := q.Get("per_page"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid per_page")
			return
		}
		perPage = n
	}

	filter := subscribers.ListFilter{
		Status:     status,
		InterestID: interestID,
		Query:      q.Get("q"),
		Page:       page,
		PerPage:    perPage,
	}
	subs, total, err := h.store.List(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	counts, err := h.store.StatusCounts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	views := make([]subscriberView, 0, len(subs))
	for _, sub := range subs {
		views = append(views, toSubscriberView(sub, nil))
	}

	writeJSON(w, http.StatusOK, subscribersListResponse{
		Subscribers: views,
		Total:       total,
		Page:        page,
		PerPage:     perPage,
		Counts:      toStatusCountsView(counts),
	})
}

// ── GET /admin/subscribers/{id} ──────────────────────────────────────────────

func parseSubscriberID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid subscriber id")
		return 0, false
	}
	return id, true
}

// Get handles GET /admin/subscribers/{id}: the detail view with consent
// evidence, the subscriber's selected interests (resolved to slug/name), and
// an (always-empty-for-now) email event history section — see this issue's
// Notes on #0038.
func (h *AdminSubscribersHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseSubscriberID(w, r)
	if !ok {
		return
	}
	sub, err := h.store.GetByID(r.Context(), id)
	switch {
	case errors.Is(err, subscribers.ErrNotFound):
		writeError(w, http.StatusNotFound, "subscriber not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	interestIDs, err := h.store.InterestIDs(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	var refs []interestRef
	if len(interestIDs) > 0 {
		its, err := h.interests.GetByIDs(r.Context(), interestIDs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		refs = make([]interestRef, 0, len(its))
		for _, it := range its {
			refs = append(refs, interestRef{ID: it.ID, Slug: it.Slug, Name: it.Name})
		}
	}

	view := toSubscriberView(sub, refs)

	// #0124: the threshold SoftBounceStreak (always populated — see
	// toSubscriberView) is being measured against. Unlike the retired
	// windowed count, no query is needed for the streak itself; only the
	// threshold needs a settings read, so this is the sole remaining
	// dependency-gated piece of the detail view. Matches this handler's
	// existing nil-tolerance for STORAGE=json dev mode.
	if h.settings != nil {
		threshold := softBounceThreshold(r.Context(), h.settings, nil)
		view.SoftBounceThreshold = intPtr(threshold)
	}

	// #0126: the address's activity log, newest first — #0032's own
	// "event history" criterion for this drawer.
	history, err := h.store.EventHistory(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if len(history) > 0 {
		view.Events = make([]subscriberEventView, 0, len(history))
		for _, e := range history {
			view.Events = append(view.Events, subscriberEventView{
				Action:     string(e.Action),
				CreatedAt:  e.CreatedAt.UTC().Format(time.RFC3339),
				CampaignID: e.CampaignID,
				Detail:     json.RawMessage(e.Detail),
			})
		}
	}

	writeJSON(w, http.StatusOK, view)
}

// ── POST /admin/subscribers/{id}/suppress ───────────────────────────────────

type suppressRequest struct {
	Note string `json:"note"`
}

type suppressResponse struct {
	Subscriber subscriberView `json:"subscriber"`
	// NoOp is true when the target was already `complained`: Unsubscribe is
	// a guaranteed no-op on a complained row (CLAUDE.md §9 —
	// subscribers.Store's package doc comment), so the request "succeeded"
	// (200, no error) without changing the subscribers-table status. #0032's
	// carried-in review finding requires the screen to surface this rather
	// than let an admin believe the status changed. See suppressResponse's
	// Message. Independent of SuppressionAdded — the noOp status case and
	// the suppressions-table write are two different effects.
	NoOp bool `json:"no_op"`
	// SuppressionAdded is true when a real subscribers.suppressions row
	// (#0033) was written for this address. False only when this handler
	// was constructed with a nil suppressions store (STORAGE=json dev
	// mode) — never because the write failed, since a failed write is a
	// 500, not a 200 with this field false.
	SuppressionAdded bool   `json:"suppression_added"`
	Message          string `json:"message"`
}

// Suppress handles POST /admin/subscribers/{id}/suppress. note is required
// (400 if empty after trimming). See the package doc comment for what this
// endpoint does.
func (h *AdminSubscribersHandler) Suppress(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseSubscriberID(w, r)
	if !ok {
		return
	}

	var req suppressRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		writeError(w, http.StatusBadRequest, "note is required")
		return
	}

	before, err := h.store.GetByID(r.Context(), id)
	switch {
	case errors.Is(err, subscribers.ErrNotFound):
		writeError(w, http.StatusNotFound, "subscriber not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	now := h.now()
	after, err := h.store.Unsubscribe(r.Context(), id, subscribers.SourceAdmin, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	// Unsubscribe's only no-op case is a complained row (see its doc
	// comment); comparing before/after status is how the carried-in review
	// finding is honored — inspect the returned Status, don't assume success.
	noOp := before.Status == subscribers.StatusComplained

	// Real suppressions-table write (#0033): Add is idempotent, so a repeat
	// Suppress on an already-suppressed address is safe — it returns the
	// original row rather than erroring or overwriting an earlier
	// reason/note. A write failure here is surfaced as a 500 rather than
	// swallowed: this endpoint's whole point, since #0033 landed, is that
	// suppression is a real, durable block, so silently proceeding without
	// it would recreate the exact gap #0032's review flagged.
	var suppressionAdded bool
	if h.suppressions != nil {
		if _, err := h.suppressions.Add(r.Context(), subscribers.NewSuppression{
			Email:  after.Email,
			Reason: subscribers.SuppressionReasonManual,
			Note:   note,
		}, now); err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		suppressionAdded = true
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := id
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSubscriberSuppressed,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"note":              note,
				"previous_status":   before.Status,
				"resulting_status":  after.Status,
				"no_op":             noOp,
				"suppression_added": suppressionAdded,
			},
			IP: clientIP(r),
		})
	}

	message := "Subscriber unsubscribed (source: admin) and added to the permanent suppression list."
	if noOp {
		// A complained subscriber never auto-resubscribes, and unsubscribe
		// is not an exit from that state — only an admin explicitly
		// clearing the complaint (below) is. The pointer to "Clear
		// complaint" used to be appended only when h.suppressions was nil
		// — dev/test wiring that production never takes, so the message an
		// admin actually saw never named the exit. It is unconditional now:
		// this modal sits over the same row Admin.svelte renders a "Clear
		// complaint" button on, so the button is not the only place the
		// route forward is named, but this message stands on its own too
		// (#0182).
		message = "This subscriber has complained. By design, unsubscribing a complained row makes no status change — that is enforced, not a glitch. Use \"Clear complaint\" to change that."
		if suppressionAdded {
			message += " The address was still added to the permanent suppression list."
		}
	}
	writeJSON(w, http.StatusOK, suppressResponse{
		Subscriber:       toSubscriberView(after, nil),
		NoOp:             noOp,
		SuppressionAdded: suppressionAdded,
		Message:          message,
	})
}

// ── POST /admin/subscribers/{id}/clear-complaint ────────────────────────────

type clearComplaintResponse struct {
	Subscriber subscriberView `json:"subscriber"`
	Message    string         `json:"message"`
}

// ClearComplaint handles POST /admin/subscribers/{id}/clear-complaint, the
// only sanctioned exit from `complained` (subscribers.Store.
// AdminClearComplaint; CLAUDE.md §9). Returns 409 when the target isn't
// currently complained (ErrNotComplained) — #0032's own screen is expected
// to offer this action only on complained rows, so reaching this state is a
// caller bug worth surfacing, not a case to paper over.
func (h *AdminSubscribersHandler) ClearComplaint(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseSubscriberID(w, r)
	if !ok {
		return
	}

	// #0033's carried-in review finding (from #0032): capture the
	// evidence AdminClearComplaint is about to overwrite BEFORE calling it,
	// so the audit row records what was discarded rather than losing that
	// provenance — mirroring how Suppress already records previous_status.
	before, err := h.store.GetByID(r.Context(), id)
	switch {
	case errors.Is(err, subscribers.ErrNotFound):
		writeError(w, http.StatusNotFound, "subscriber not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	now := h.now()
	after, err := h.store.AdminClearComplaint(r.Context(), id, now)
	switch {
	case errors.Is(err, subscribers.ErrNotFound):
		writeError(w, http.StatusNotFound, "subscriber not found")
		return
	case errors.Is(err, subscribers.ErrNotComplained):
		writeError(w, http.StatusConflict, "subscriber has not complained; nothing to clear")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// Real suppressions-table removal, SCOPED TO reason=complaint (#0100):
	// clearing a complaint must remove only the complaint suppression, never
	// a hard_bounce or manual row that happens to share this address — a
	// reason-blind DELETE (the pre-#0100 behavior) could silently discard a
	// second, independent reason to block the address. ErrSuppressionNotFound
	// (no complaint suppression row existed — e.g. this complaint predates
	// #0033, or the address was never suppressed at the table level even
	// though it was complained) is not an error worth failing the request
	// over; any other failure is.
	var suppressionRemoved bool
	var removed subscribers.Suppression
	var remaining []subscribers.Suppression
	if h.suppressions != nil {
		switch rem, err := h.suppressions.Remove(r.Context(), after.Email, subscribers.SuppressionReasonComplaint); {
		case err == nil:
			suppressionRemoved = true
			removed = rem
		case errors.Is(err, subscribers.ErrSuppressionNotFound):
			// nothing to remove
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		// ListByEmail regardless of whether a row was removed: the caller
		// needs to know what (if anything) still blocks this address either
		// way, and building the survivor list from the store — never
		// hardcoding a reason — is what keeps this correct as new reasons
		// are added.
		remaining, err = h.suppressions.ListByEmail(r.Context(), after.Email)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}
	remainingReasons := make([]string, 0, len(remaining))
	for _, sup := range remaining {
		remainingReasons = append(remainingReasons, sup.Reason)
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := id
		metadata := map[string]any{
			"resulting_status":            after.Status,
			"overwrites_note":             "unsubscribed_at/unsubscribe_source were overwritten with admin/now by AdminClearComplaint, discarding the earlier unsubscribe evidence recorded below",
			"previous_unsubscribed_at":    before.UnsubscribedAt,
			"previous_unsubscribe_source": before.UnsubscribeSource,
			"suppression_removed":         suppressionRemoved,
			"suppression_removed_reason":  subscribers.SuppressionReasonComplaint,
			"suppressions_remaining":      remainingReasons,
		}
		if suppressionRemoved {
			metadata["suppression_removed_note"] = removed.Note
			metadata["suppression_removed_created_at"] = removed.CreatedAt
		}
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSubscriberComplaintCleared,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata:   metadata,
			IP:         clientIP(r),
		})
	}

	// #0126: admin_edited — "A staff member changed the record directly"
	// (PRD §6.11). Clear-complaint is a direct, admin-only edit of the
	// subscriber's status; the audit_log entry above is the detailed
	// staff-facing record, this is the address's own history entry.
	if err := h.store.RecordEvent(r.Context(), subscribers.Event{
		SubscriberID: &id,
		Email:        after.Email,
		Action:       subscribers.ActionAdminEdited,
		ActorUserID:  &actor.ID,
		Detail:       map[string]any{"operation": "clear_complaint"},
	}); err != nil {
		slog.Default().Error("admin_subscribers: recording admin_edited event failed", "subscriber_id", id, "err", err)
	}

	message := "Complaint cleared. Resulting status is \"unsubscribed\", not \"active\" — this does not re-establish double opt-in consent."
	switch {
	case suppressionRemoved && len(remainingReasons) == 0:
		message += " The \"complaint\" suppression-list entry was removed, and no other suppression remains for this address."
	case suppressionRemoved:
		message += " The \"complaint\" suppression-list entry was removed, but this address is still suppressed for: " +
			strings.Join(remainingReasons, ", ") + ". It remains blocked at the send gate."
	case !suppressionRemoved && len(remainingReasons) == 0:
		message += " No \"complaint\" suppression-list entry existed for this address."
	default:
		message += " No \"complaint\" suppression-list entry existed. This address is still suppressed for: " +
			strings.Join(remainingReasons, ", ") + "."
	}
	writeJSON(w, http.StatusOK, clearComplaintResponse{
		Subscriber: toSubscriberView(after, nil),
		Message:    message,
	})
}

// ── POST /admin/subscribers (manual add) ─────────────────────────────────────

type manualAddRequest struct {
	Email     string   `json:"email"`
	Interests []string `json:"interests"`
	Note      string   `json:"note"`
}

type manualAddResponse struct {
	Subscriber subscriberView `json:"subscriber"`
	Message    string         `json:"message"`
}

// manualAddMessage renders a human-readable outcome for the admin screen so
// "I clicked add" has a visible, honest answer rather than requiring the
// admin to infer it from status alone — this endpoint's whole point is that
// nothing here can create an active subscriber directly (PRD §5.2's notes).
func manualAddMessage(status string) string {
	switch status {
	case subscribers.StatusPending:
		return "Added. A confirmation email has been queued — the address is not on the list until they confirm."
	case subscribers.StatusActive:
		return "This address was already confirmed and active; an \"already subscribed\" notice has been queued."
	case subscribers.StatusComplained:
		// A complained subscriber never auto-resubscribes; only an admin
		// clearing the complaint moves it out of that state. This message
		// lands in createSubNotice on the manual-add form, where no "Clear
		// complaint" button is adjacent (that button lives on the
		// subscriber row, not the add form), so the route forward has to be
		// named in the copy itself — nothing on screen names it otherwise
		// (#0182).
		return "This address has previously complained. By design, adding it here does not resubscribe it — only clearing the complaint on that subscriber's row changes that. Nothing was sent."
	default:
		return "Request processed; resulting status: " + status
	}
}

// Create handles POST /admin/subscribers: the manual-add flow. It never
// creates an `active` subscriber directly — see the package doc comment for
// why it dispatches through *SubscribeHandler's own newSignup/existingSignup
// rather than writing subscribers rows itself. Unlike the public endpoint,
// this is an authenticated admin action, so there is no uniform-response
// requirement (CLAUDE.md §9's enumeration-oracle concern is specific to the
// unauthenticated public signup form) — the response reports exactly what
// happened.
//
// signup_ip/signup_user_agent are deliberately left empty (NULL) for a
// manually-added subscriber rather than filled with the ADMIN's IP/user
// agent: those columns are #0075's disclosed "evidence of consent" from the
// SUBSCRIBER's own browser, and fabricating a value there would misrepresent
// who actually submitted the form. The operator's identity and IP are
// instead captured in this action's own audit row (ActorID, IP) — a
// complete, honest trail without conflating the two provenances.
func (h *AdminSubscribersHandler) Create(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	if h.manualAdd == nil {
		writeError(w, http.StatusServiceUnavailable, "manual add is unavailable")
		return
	}

	var req manualAddRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	email := strings.TrimSpace(req.Email)
	if !validEmailSyntax(email) {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if len(req.Interests) > maxSubscribeInterests {
		writeError(w, http.StatusBadRequest, "too many interests")
		return
	}

	ctx := r.Context()
	interestIDs, err := h.manualAdd.resolveInterestIDs(ctx, req.Interests)
	switch {
	case errors.Is(err, errUnknownInterest):
		writeError(w, http.StatusBadRequest, "unknown interest")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	note := strings.TrimSpace(req.Note)
	now := h.now()
	evidence := subscribers.RestartSignupInput{ConfirmTTL: subscribeConfirmTTL}

	existing, err := h.store.FindByEmail(ctx, email)
	switch {
	case errors.Is(err, subscribers.ErrNotFound):
		h.manualAdd.newSignup(ctx, email, interestIDs, evidence, now)
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	default:
		h.manualAdd.existingSignup(ctx, existing, interestIDs, evidence, now)
	}

	result, err := h.store.FindByEmail(ctx, email)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := result.ID
		h.auditor.Record(ctx, audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSubscriberManualAdd,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"email":            email,
				"interests":        req.Interests,
				"note":             note,
				"resulting_status": result.Status,
			},
			IP: clientIP(r),
		})
	}

	// #0126: admin_edited, alongside (not instead of) the signup_requested/
	// resubscribed event newSignup/existingSignup's own Create/RestartSignup
	// call already recorded above — that event captures WHAT happened (a
	// signup was requested); this one captures WHO caused it (a staff
	// member, not the subscriber). #0126's plan §9 item 9 flags this
	// pairing as the one genuinely ambiguous action mapping in this issue;
	// this is the suggested reading, not a certainty — see that plan
	// section for the reviewer.
	if err := h.store.RecordEvent(ctx, subscribers.Event{
		SubscriberID: &result.ID,
		Email:        result.Email,
		Action:       subscribers.ActionAdminEdited,
		ActorUserID:  &actor.ID,
		Detail:       map[string]any{"operation": "manual_add"},
	}); err != nil {
		slog.Default().Error("admin_subscribers: recording admin_edited event failed", "subscriber_id", result.ID, "err", err)
	}

	writeJSON(w, http.StatusOK, manualAddResponse{
		Subscriber: toSubscriberView(result, nil),
		Message:    manualAddMessage(result.Status),
	})
}

// ── DELETE /admin/subscribers/{id} ───────────────────────────────────────────

// eraseSubscriberRequest is DELETE /admin/subscribers/{id}'s body.
// ConfirmEmail is the typed confirmation #0060's acceptance criteria
// requires: the caller must type the subscriber's own email address back,
// the "type the resource's name to confirm" pattern GitHub/similar consoles
// use for irreversible actions, rather than a bare boolean flag a client
// could send without any evidence an operator saw what they were about to
// destroy.
type eraseSubscriberRequest struct {
	ConfirmEmail string `json:"confirm_email"`
}

// eraseSubscriberResponse is DELETE /admin/subscribers/{id}'s 200 body. The
// subscriber row itself is gone by the time this is built — every field
// below is a copy Erase captured before deleting it.
type eraseSubscriberResponse struct {
	Erased               bool   `json:"erased"`
	Email                string `json:"email"`
	PreviousStatus       string `json:"previous_status"`
	InterestsRemoved     int    `json:"interests_removed"`
	EmailSendsAnonymized int64  `json:"email_sends_anonymized"`
	Message              string `json:"message"`
}

// Erase handles DELETE /admin/subscribers/{id} (#0060, PRD §11): hard-deletes
// the subscriber, cascading its subscriber_interests rows, after
// anonymizing its email_sends rows and adding a permanent `manual`
// suppression for its address — see internal/subscribers/erase.go's package
// doc comment for the full design, and CLAUDE.md §9 for why a hard delete
// here does not defeat the "complained never auto-resubscribes" rule (the
// suppression added below outlives the row and blocks return either way).
//
// Requires a typed confirmation: the request body's confirm_email must
// case-insensitively match the target subscriber's own address (400
// otherwise), so this destructive, irreversible action cannot be triggered
// by a bare click with no evidence the operator saw what they were about to
// erase — the same "typed confirmation enforced only in the browser is
// theatre" reasoning #0047's campaign-send confirm_count carries in
// (admin_campaigns.go's sendCampaignRequest).
//
// 409s with ErrHasPendingSends if the subscriber has a queued or
// currently-sending campaign delivery — see erase.go's package doc comment
// for exactly why erasing through that would leave a stuck email_sends row
// rather than protecting anyone, and what an operator should do instead
// (wait for the send to finish, or cancel the campaign, then retry).
//
// For a copy of this subscriber's data before erasing it (this issue's
// "data-export-on-request path" criterion), reuse #0059's export filtered
// to this one address: GET /admin/subscribers/export?q=<email> —
// StreamExport's Query filter is already a case-insensitive substring match
// against email, so the exact address this handler just verified against
// before (below) returns exactly one row. A dedicated single-subscriber
// export endpoint would duplicate that query for no new behavior.
func (h *AdminSubscribersHandler) Erase(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseSubscriberID(w, r)
	if !ok {
		return
	}

	var req eraseSubscriberRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	confirmEmail := strings.ToLower(strings.TrimSpace(req.ConfirmEmail))
	if confirmEmail == "" {
		writeError(w, http.StatusBadRequest, "confirm_email is required")
		return
	}

	// Loaded (and its email compared against the typed confirmation) BEFORE
	// calling Erase: the store method's own before-state doesn't reach this
	// handler, and a 400 for a mistyped confirmation must not have mutated
	// anything.
	before, err := h.store.GetByID(r.Context(), id)
	switch {
	case errors.Is(err, subscribers.ErrNotFound):
		writeError(w, http.StatusNotFound, "subscriber not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if confirmEmail != strings.ToLower(strings.TrimSpace(before.Email)) {
		writeError(w, http.StatusBadRequest, "confirm_email does not match this subscriber's address")
		return
	}

	now := h.now()
	result, err := h.store.Erase(r.Context(), id, now)
	switch {
	case errors.Is(err, subscribers.ErrNotFound):
		// Raced with a concurrent delete between GetByID above and here.
		writeError(w, http.StatusNotFound, "subscriber not found")
		return
	case errors.Is(err, subscribers.ErrHasPendingSends):
		writeError(w, http.StatusConflict, "cannot erase: a campaign delivery to this subscriber is queued or in progress")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := id
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSubscriberErased,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"email":                  result.Email,
				"previous_status":        result.PreviousStatus,
				"interests_removed":      result.InterestsRemoved,
				"email_sends_anonymized": result.EmailSendsAnonymized,
				"suppression_reason":     subscribers.SuppressionReasonManual,
				"suppression_preexisted": result.SuppressionPreexisted,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, eraseSubscriberResponse{
		Erased:               true,
		Email:                result.Email,
		PreviousStatus:       result.PreviousStatus,
		InterestsRemoved:     result.InterestsRemoved,
		EmailSendsAnonymized: result.EmailSendsAnonymized,
		Message:              "Subscriber erased. The address remains on the permanent suppression list and cannot be silently re-added.",
	})
}
