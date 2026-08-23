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
		BodyMD:       announceBodyMD(wk, h.baseURL),
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
//
// # Call to action (#0145)
//
// PRD §8's acceptance criterion for this body names only title/summary/
// date/location — it does not ask for a link at all. But a body with no
// link is not something an admin would send unedited (this file's binding
// goal, stated in the package doc comment), so the body always closes with
// a link to the workshop's own public page at baseURL+"/workshops/{slug}"
// (the same "/workshops/"+slug shape internal/seo's sitemap, OG tags, and
// JSON-LD already build canonical URLs from — see internal/seo/seo.go,
// sitemap.go, jsonld.go). When the workshop also has an external
// signup_url, that link is emitted FIRST as "Sign up" — it stays the
// primary action per #0145's acceptance criterion — and the site page is
// offered second as "View this workshop" so the two never look like
// duplicate CTAs. baseURL is config.Config.BaseURL (already trailing-slash
// normalized by internal/config; see NewAdminWorkshopsHandler's doc
// comment) — never a hardcoded host, so a local/staging deploy links to
// itself rather than production.
func announceBodyMD(wk workshops.Workshop, baseURL string) string {
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

	signupURL := ""
	if wk.SignupURL != nil {
		signupURL = strings.TrimSpace(*wk.SignupURL)
	}
	if signupURL != "" {
		fmt.Fprintf(&b, "[Sign up](%s)\n\n", signupURL)
		fmt.Fprintf(&b, "[View this workshop](%s)\n\n", announceWorkshopURL(baseURL, wk.Slug))
	} else {
		fmt.Fprintf(&b, "[View this workshop](%s)\n\n", announceWorkshopURL(baseURL, wk.Slug))
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

// announceWorkshopURL builds the workshop's own public page URL the same
// way internal/seo builds canonical/OG/sitemap URLs for it: baseURL (no
// trailing slash, per config.normalizeBaseURL) + "/workshops/" + slug.
func announceWorkshopURL(baseURL, slug string) string {
	return strings.TrimRight(baseURL, "/") + "/workshops/" + slug
}

// announceLocation is the timezone the announce body renders workshop
// start/end times in.
//
// # Hardcoded, deliberately — not config.Config (#0144's acceptance
// criterion asks this be decided and recorded)
//
// Open Circuit SF is a single, fixed Bay Area organization, not a
// multi-tenant platform: config.Config's other location-shaped values are
// likewise pinned facts about this one org rather than per-deployment
// knobs (WEBAUTHN_RP_ID is literally opencircuitsf.com in every env file
// this repo ships, and the CAN-SPAM physical_address setting names one
// building, not a list). PRD.md names no requirement for a second venue in
// another timezone, and CLAUDE.md §5's standing instruction is not to build
// for a load or a requirement that does not exist. A config knob here would
// be one more environment variable, one more thing every deploy must get
// right, for a value that has never needed to vary.
//
// It is still isolated in this one named var (never inlined at a call
// site) precisely so that IF Open Circuit SF ever runs a workshop outside
// the Bay Area, promoting it to a WORKSHOP_TIMEZONE config field alongside
// BaseURL is a small, local change — not a repo-wide search.
var announceLocation = mustLoadLocation("America/Los_Angeles")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		// Only reachable if the Go toolchain's tzdata is missing/corrupt —
		// every workshop announce would be broken regardless, so fail loud
		// at package init rather than silently rendering wrong times.
		panic(fmt.Sprintf("admin_workshop_announce: load location %q: %v", name, err))
	}
	return loc
}

// announceDateLayout formats a workshop timestamp for the pre-filled body.
// The "MST" reference field is Go's placeholder for "the zone
// abbreviation of whatever *time.Location the time.Time was converted
// into" — formatting a time in announceLocation renders "PST" or "PDT" as
// appropriate, it does not literally print Mountain time.
const announceDateLayout = "Monday, January 2, 2006 at 3:04 PM MST"

// announceWhen renders the workshop's date/time range in America/Los_Angeles
// (#0144 — matches web/src/lib/workshops.ts's viewer-zone rendering on the
// public page for a Bay Area reader), or "" when StartsAt is unset. EndsAt,
// when present, is appended as a time-only range ("6:00 PM to 8:00 PM") when
// it falls on the same Pacific calendar day as StartsAt, or as a full second
// timestamp otherwise — compared in Pacific, not UTC, since a late-evening
// Pacific start can already be the next UTC calendar day.
func announceWhen(wk workshops.Workshop) string {
	if wk.StartsAt == nil {
		return ""
	}
	starts := wk.StartsAt.In(announceLocation)
	when := starts.Format(announceDateLayout)
	if wk.EndsAt == nil {
		return when
	}
	ends := wk.EndsAt.In(announceLocation)
	if sameLocalDate(starts, ends) {
		return fmt.Sprintf("%s to %s", when, ends.Format("3:04 PM MST"))
	}
	return fmt.Sprintf("%s to %s", when, ends.Format(announceDateLayout))
}

// sameLocalDate reports whether a and b fall on the same calendar day in
// whatever zone they are already expressed in — callers pass times already
// converted via announceLocation.In, so this compares Pacific dates.
func sameLocalDate(a, b time.Time) bool {
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
