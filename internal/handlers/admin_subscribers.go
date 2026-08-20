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
// it removes any matching suppressions row alongside
// subscribers.Store.AdminClearComplaint. See ActionSubscriberSuppressed's and
// ActionSubscriberComplaintCleared's doc comments (internal/audit/actions.go)
// for how the audit trail records this.
package handlers

import (
	"context"
	"errors"
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
}

// adminInterestByIDReader is the behavior AdminSubscribersHandler needs from
// internal/interests to resolve a subscriber's selected interest ids to
// their slug/name for the detail view. *interests.Store satisfies it via
// GetByIDs.
type adminInterestByIDReader interface {
	GetByIDs(ctx context.Context, ids []int64) ([]interests.Interest, error)
}

// adminSuppressionWriter is the behavior AdminSubscribersHandler needs from
// subscribers.SuppressionStore (#0033): writing a real suppression row on
// Suppress and removing it on ClearComplaint. *subscribers.SuppressionStore
// satisfies it directly.
type adminSuppressionWriter interface {
	Add(ctx context.Context, in subscribers.NewSuppression, now time.Time) (subscribers.Suppression, error)
	Remove(ctx context.Context, email string) error
}

// AdminSubscribersHandler serves the admin-only subscriber routes (PRD
// §5.2; #0032):
//
//	GET  /admin/subscribers                        — paginated, filterable, searchable list + status counts
//	GET  /admin/subscribers/{id}                    — detail: consent evidence, interests, email event history (empty until #0038)
//	POST /admin/subscribers/{id}/suppress            — manual suppress (required note); see the package doc comment on what this does
//	POST /admin/subscribers/{id}/clear-complaint     — the sole sanctioned exit from `complained` (subscribers.Store.AdminClearComplaint)
//	POST /admin/subscribers                          — manual add; still requires double opt-in confirmation
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
// auditor disables audit writes.
func NewAdminSubscribersHandler(store adminSubscriberStore, il adminInterestByIDReader, manualAdd *SubscribeHandler, suppressions adminSuppressionWriter, auditor *audit.Logger) *AdminSubscribersHandler {
	return &AdminSubscribersHandler{store: store, interests: il, manualAdd: manualAdd, suppressions: suppressions, auditor: auditor, now: time.Now}
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
type subscriberView struct {
	ID                int64         `json:"id"`
	Email             string        `json:"email"`
	Status            string        `json:"status"`
	SignupIP          *string       `json:"signup_ip,omitempty"`
	SignupUserAgent   *string       `json:"signup_user_agent,omitempty"`
	UTMSource         *string       `json:"utm_source,omitempty"`
	UTMMedium         *string       `json:"utm_medium,omitempty"`
	UTMCampaign       *string       `json:"utm_campaign,omitempty"`
	CreatedAt         string        `json:"created_at"` // signup timestamp
	ConfirmedAt       *string       `json:"confirmed_at,omitempty"`
	UnsubscribedAt    *string       `json:"unsubscribed_at,omitempty"`
	UnsubscribeSource *string       `json:"unsubscribe_source,omitempty"`
	Interests         []interestRef `json:"interests,omitempty"` // populated by Get only
	EmailEvents       []any         `json:"email_events"`        // always [] until #0038
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
	}
}

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

	writeJSON(w, http.StatusOK, toSubscriberView(sub, refs))
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
		message = "This subscriber has complained, and CLAUDE.md §9 refuses to let unsubscribe touch a complained row — no status change."
		if suppressionAdded {
			message += " The address was still added to the permanent suppression list."
		} else {
			message += " Use \"Clear complaint\" first if the complaint should be cleared."
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

	// Real suppressions-table removal (#0033): clearing a complaint no
	// longer leaves a stale suppressions row blocking the address at
	// #0026's send gate. ErrSuppressionNotFound (no suppression row
	// existed — e.g. this complaint predates #0033, or the address was
	// never suppressed at the table level even though it was complained)
	// is not an error worth failing the request over; any other failure is.
	var suppressionRemoved bool
	if h.suppressions != nil {
		switch err := h.suppressions.Remove(r.Context(), after.Email); {
		case err == nil:
			suppressionRemoved = true
		case errors.Is(err, subscribers.ErrSuppressionNotFound):
			// nothing to remove
		default:
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := id
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSubscriberComplaintCleared,
			TargetType: audit.TargetSubscriber,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"resulting_status":            after.Status,
				"overwrites_note":             "unsubscribed_at/unsubscribe_source were overwritten with admin/now by AdminClearComplaint, discarding the earlier unsubscribe evidence recorded below",
				"previous_unsubscribed_at":    before.UnsubscribedAt,
				"previous_unsubscribe_source": before.UnsubscribeSource,
				"suppression_removed":         suppressionRemoved,
			},
			IP: clientIP(r),
		})
	}

	message := "Complaint cleared. Resulting status is \"unsubscribed\", not \"active\" — this does not re-establish double opt-in consent."
	if suppressionRemoved {
		message += " The matching suppression-list entry was also removed, so a future resignup is no longer blocked at the send gate."
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
		return "This address has previously complained and CLAUDE.md §9 refuses to auto-resubscribe it. Nothing was sent."
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

	writeJSON(w, http.StatusOK, manualAddResponse{
		Subscriber: toSubscriberView(result, nil),
		Message:    manualAddMessage(result.Status),
	})
}
