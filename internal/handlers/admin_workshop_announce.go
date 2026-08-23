// Announce-to-list shortcut (#0056; PRD §8, §5.2): POST
// /admin/workshops/{id}/announce creates a draft email campaign pre-filled
// from one workshop — subject, body, and interest targeting — so an
// operator can go from "this workshop exists" straight to "I have a draft
// to review and send" without re-typing the workshop's own details or
// hand-picking its audience.
//
// # This is a compose shortcut, never a send (binding, issues/0056.md)
//
// Announce does exactly one thing: mailing.CampaignStore.Create, the same
// call AdminCampaignsHandler.Create makes for a hand-composed campaign.
// The row it creates lands in status='draft' by that method's own
// contract — nothing here, or anywhere in this file, ever calls Send. The
// operator still has to open the draft, review it, and run it through
// #0047's full send flow (test send, preflight, typed recipient-count
// confirmation) like any other campaign. Repeated calls create additional
// drafts rather than overwriting one — intentional (issues/0056.md's
// Notes: "a follow-up reminder is a normal thing to send, and silently
// overwriting a partly-edited draft would lose work"), and falls out for
// free from always calling Create rather than checking for an existing
// linked draft first.
//
// # Why a method on AdminWorkshopsHandler, not its own handler type
//
// An earlier version of this file gave Announce its own
// AdminWorkshopAnnounceHandler, specifically to avoid touching
// AdminWorkshopsHandler's constructor signature (and, transitively,
// adminRoutes/mountAndServe's own signatures and every one of their ~10
// wiring-test call sites across cmd/opencircuit/ — a large blast radius
// for one route). That tradeoff turned out to be backwards: adminRoutes
// and mountAndServe both already thread *handlers.AdminWorkshopsHandler
// through as a parameter, so a route reachable from THAT handler needs
// zero signature changes anywhere in cmd/opencircuit/ — only a new
// registration line inside adminRoutes' existing `if adminWorkshopsH !=
// nil` block. Announce is therefore a method here, and
// NewAdminWorkshopsHandler (admin_workshops.go) takes one additional
// constructor argument, campaigns announceCampaignStore — its own doc
// comment covers the nil-tolerance contract.
//
// # Audience targeting when the workshop has no interests
//
// PRD's acceptance criterion says "pre-set to the workshop's interests in
// any_of mode" — but a workshop is not required to carry any interests
// (workshops.CreateInput.InterestIDs is optional), and
// mailing.normalizeAudience refuses any_of/all_of with zero interest ids
// (mailing.ErrCampaignInterestsRequired) precisely to stop a silent
// "targets nobody in practice" campaign. Rather than surface that
// validation error to the operator for what is supposed to be a one-click
// shortcut, a workshop with no interests falls back to
// mailing.AudienceAll (targets the full active list) — the draft is fully
// editable, so the operator narrows it before sending if that is not what
// they want.
package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// Announce handles POST /admin/workshops/{id}/announce: loads the
// workshop, builds a pre-filled draft campaign from it, and creates that
// campaign via h.campaigns.Create. Returns 201 with the created campaign
// (the same campaignView shape GET/POST /admin/campaigns use) so the
// compose UI can jump straight into editing it. Responds 500 if h.campaigns
// is nil — test-only (see NewAdminWorkshopsHandler's doc comment); every
// production call site wires a real *mailing.CampaignStore.
func (h *AdminWorkshopsHandler) Announce(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseWorkshopID(w, r)
	if !ok {
		return
	}
	if h.campaigns == nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	wk, err := h.store.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, workshops.ErrNotFound):
		writeError(w, http.StatusNotFound, "workshop not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	mode := mailing.AudienceAnyOf
	interestIDs := wk.InterestIDs
	if len(interestIDs) == 0 {
		// See the package doc comment above: an interest-less workshop
		// falls back to "all" rather than tripping
		// mailing.ErrCampaignInterestsRequired on an empty any_of set.
		mode = mailing.AudienceAll
		interestIDs = nil
	}

	actorID := actor.ID
	workshopID := wk.ID
	created, err := h.campaigns.Create(r.Context(), mailing.CampaignInput{
		Name:         announceCampaignName(wk),
		Subject:      announceSubject(wk),
		BodyMD:       announceBodyMD(wk),
		AudienceMode: mode,
		InterestIDs:  interestIDs,
		WorkshopID:   &workshopID,
		CreatedBy:    &actorID,
	})
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrUnknownAudienceMode), errors.Is(err, mailing.ErrCampaignInterestsRequired):
		// Defence in depth: normalizeAudience above should make this
		// unreachable (mode is always a known constant, and interestIDs is
		// nil'd out for any_of's empty case), but map it the same way
		// AdminCampaignsHandler.Create does rather than let it fall through
		// as a bare 500.
		writeError(w, http.StatusBadRequest, campaignAudienceErrorMessage(err))
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
				"workshop_id":   wk.ID,
				"workshop_slug": wk.Slug,
				"via":           "announce",
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusCreated, toCampaignView(created))
}

// ── Pre-fill content ─────────────────────────────────────────────────────────

// announceCampaignName builds the internal (admin-facing only, never sent)
// campaign name for an announce-created draft, so it reads clearly in the
// campaigns list rather than bare-reusing the workshop title.
func announceCampaignName(wk workshops.Workshop) string {
	return fmt.Sprintf("Announce: %s", wk.Title)
}

// announceSubject builds the pre-filled email subject line from the
// workshop's title alone — date and location go in the body, where there is
// room to present them clearly; a subject line packed with every field
// would be unreadable in an inbox.
func announceSubject(wk workshops.Workshop) string {
	return fmt.Sprintf("New workshop: %s", wk.Title)
}

// announceBodyMD builds the pre-filled Markdown body from the workshop's
// title, summary, date, and location (PRD §8's acceptance criterion),
// omitting any section whose source field is unset rather than emitting an
// empty "**When:**" line. Fully editable by the operator before send — see
// the package doc comment.
func announceBodyMD(wk workshops.Workshop) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", wk.Title)

	if wk.Summary != nil && strings.TrimSpace(*wk.Summary) != "" {
		fmt.Fprintf(&b, "%s\n\n", strings.TrimSpace(*wk.Summary))
	}

	if when := announceWhen(wk); when != "" {
		fmt.Fprintf(&b, "**When:** %s\n\n", when)
	}

	if where := announceWhere(wk); where != "" {
		fmt.Fprintf(&b, "**Where:** %s\n\n", where)
	}

	if wk.SignupURL != nil && strings.TrimSpace(*wk.SignupURL) != "" {
		fmt.Fprintf(&b, "[Sign up](%s)\n\n", strings.TrimSpace(*wk.SignupURL))
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// announceDateLayout formats a workshop timestamp for the pre-filled body.
// Displayed in UTC (workshops.starts_at/ends_at are TIMESTAMPTZ; the store
// never records a display timezone) — acceptable for a draft the operator
// reviews and edits before sending, not a claim about local event time.
const announceDateLayout = "Monday, January 2, 2006 at 3:04 PM MST"

// announceWhen renders the workshop's date/time range, or "" when
// StartsAt is unset. EndsAt, when present, is appended as a time-only
// range ("6:00 PM to 8:00 PM") when it falls on the same UTC calendar day
// as StartsAt, or as a full second timestamp otherwise.
func announceWhen(wk workshops.Workshop) string {
	if wk.StartsAt == nil {
		return ""
	}
	starts := wk.StartsAt.UTC()
	when := starts.Format(announceDateLayout)
	if wk.EndsAt == nil {
		return when
	}
	ends := wk.EndsAt.UTC()
	if sameUTCDate(starts, ends) {
		return fmt.Sprintf("%s to %s", when, ends.Format("3:04 PM MST"))
	}
	return fmt.Sprintf("%s to %s", when, ends.Format(announceDateLayout))
}

func sameUTCDate(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

// announceWhere renders the workshop's location as "Name, Address (Note)",
// omitting any of the three fields that are unset, or "" when none are
// set.
func announceWhere(wk workshops.Workshop) string {
	var parts []string
	if wk.LocationName != nil && strings.TrimSpace(*wk.LocationName) != "" {
		parts = append(parts, strings.TrimSpace(*wk.LocationName))
	}
	if wk.LocationAddress != nil && strings.TrimSpace(*wk.LocationAddress) != "" {
		parts = append(parts, strings.TrimSpace(*wk.LocationAddress))
	}
	where := strings.Join(parts, ", ")
	if wk.LocationNote != nil && strings.TrimSpace(*wk.LocationNote) != "" {
		note := strings.TrimSpace(*wk.LocationNote)
		if where == "" {
			return note
		}
		return fmt.Sprintf("%s (%s)", where, note)
	}
	return where
}
