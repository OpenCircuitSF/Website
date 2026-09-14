// Public campaign archive read path (#0123, PRD §6.8): every SENT campaign
// is also a permanent, public, search-indexable web page.
//
//	GET /api/archive        — List:   published campaigns, reverse chronological
//	GET /api/archive/{slug} — GetBySlug: one campaign's web content
//
// Both routes are unauthenticated and carry no session/admin gate — PRD
// §6.8: "No login, no token, no noindex. These pages are public on
// purpose."
//
// # Rendering: the web renderer, not the email renderer (#0042 reused, not duplicated)
//
// GetBySlug renders campaign.BodyMD through mailing.RenderMarkdownPageHTML
// (#0519) — the same goldmark parse #0042 built and #0043's email pipeline
// (RenderCampaign/wrapCampaignHTML) also calls, via the same campaignMarkdown
// instance, but with every heading demoted one level (capped at <h6>) since
// this body renders under the page's own subject <h1>. It deliberately does
// NOT call RenderCampaign or styleCampaignBodyHTML: those two apply the
// email-specific output — a complete mail-safe HTML document, inline
// mail-client styling, the campaign footer with its manage/unsubscribe
// links. RenderMarkdownPageHTML alone returns a plain, unstyled HTML
// fragment (the exact same one public_workshops.go's renderWorkshopBodyHTML
// wraps for a workshop's body — see that file's own doc comment for the
// identical reuse), which ArchiveEntry.svelte renders inside the site's
// own shell and its own CSS, so it reads as a web page that happens to
// contain a newsletter, not an email screenshotted into a page (PRD §6.8).
// The Markdown PARSE is shared between the email and web paths; the OUTPUT
// TEMPLATE never is.
//
// # Visibility rule (PRD §6.8's table; this issue's acceptance criteria)
//
//   - 404 when no email_campaigns row has the slug at all.
//   - 404 when the row exists but its status is not 'sent' (draft,
//     scheduled, sending, paused_delivery_health, canceled, failed) — the
//     page does not exist yet (or at all), same as an unknown slug;
//     deliberately indistinguishable, so a visitor probing slugs learns
//     nothing about a campaign that hasn't gone out yet.
//   - 410 Gone when archive_status = 'withheld' — an admin's deliberate
//     retraction (PATCH /admin/campaigns/{id}/archive), which is a
//     different fact than "never existed" and told to crawlers as such
//     (see this issue's "withheld must be 410, not 404" note: 410 tells a
//     crawler to drop the page; 404 invites it to keep re-checking).
//   - 200 with the campaign's public web content when archive_status =
//     'published'.
//   - 404 for any other archive_status (#0318) — today only 'pending', a
//     value SetArchiveStatus can never write and CompleteIfDone only ever
//     leaves behind for the instant between flipping status to 'sent' and
//     archive_status to 'published' in the same UPDATE (never separately
//     observable). The switch's default is closed, not open, so a future
//     archive_status value 404s rather than leaking as 200.
//
// # Privacy (PRD §6.8's own "Privacy" paragraph)
//
// Both handlers read ONLY mailing.CampaignStore's own campaignColumns —
// see campaign_archive.go's package doc comment — so a recipient count, a
// per-recipient substitution, an unsubscribe token, or a manage_token can
// never reach either response even by accident: this file has no access
// to email_sends at all, only to a mailing.Campaign value.
package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// publicArchiveStore is the narrow slice of mailing.CampaignStore this
// handler needs. *mailing.CampaignStore satisfies it.
type publicArchiveStore interface {
	ListArchived(ctx context.Context) ([]mailing.Campaign, error)
	GetBySlug(ctx context.Context, slug string) (mailing.Campaign, error)
}

// PublicArchiveHandler serves the public, unauthenticated campaign archive
// read routes.
type PublicArchiveHandler struct {
	store publicArchiveStore
}

// NewPublicArchiveHandler constructs a PublicArchiveHandler over the data
// layer.
func NewPublicArchiveHandler(store publicArchiveStore) *PublicArchiveHandler {
	return &PublicArchiveHandler{store: store}
}

// ── Response shapes ──────────────────────────────────────────────────────────

// archiveEntryView is one campaign's row on the public index — deliberately
// narrow: slug, subject, preheader, archived_at, and nothing else (this
// issue's acceptance criterion for GET /api/archive names exactly these
// four fields).
type archiveEntryView struct {
	Slug       string  `json:"slug"`
	Subject    string  `json:"subject"`
	Preheader  *string `json:"preheader,omitempty"`
	ArchivedAt *string `json:"archived_at,omitempty"`
}

func toArchiveEntryView(c mailing.Campaign) archiveEntryView {
	return archiveEntryView{
		Slug:       c.Slug,
		Subject:    c.Subject,
		Preheader:  c.Preheader,
		ArchivedAt: formatTimePtr(c.ArchivedAt),
	}
}

type archiveListResponse struct {
	Archive []archiveEntryView `json:"archive"`
}

// archiveDetailView is one campaign's full public web content — the
// archiveEntryView fields plus the rendered body. No id, no status, no
// audience_mode, no interest_ids, no created_by: none of those are the
// public's business, and the slug alone is the wire identity, matching
// publicWorkshopView's own convention of dropping the numeric id.
type archiveDetailView struct {
	Slug       string  `json:"slug"`
	Subject    string  `json:"subject"`
	Preheader  *string `json:"preheader,omitempty"`
	BodyHTML   string  `json:"body_html"`
	ArchivedAt *string `json:"archived_at,omitempty"`
}

// ── GET /api/archive ─────────────────────────────────────────────────────────

// List handles GET /api/archive: published campaigns, reverse chronological
// (mailing.CampaignStore.ListArchived's own ordering). Public, no auth.
func (h *PublicArchiveHandler) List(w http.ResponseWriter, r *http.Request) {
	campaigns, err := h.store.ListArchived(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	views := make([]archiveEntryView, 0, len(campaigns))
	for _, c := range campaigns {
		views = append(views, toArchiveEntryView(c))
	}
	writeJSON(w, http.StatusOK, archiveListResponse{Archive: views})
}

// ── GET /api/archive/{slug} ──────────────────────────────────────────────────

// archivePageStatus maps one campaign to the HTTP status its public archive
// carries (#0522): the exact visibility rule this file's package doc comment
// states, shared by GetBySlug's JSON route and PageStatus's HTML route so
// the two can never disagree about what "public", "gone", and "not yet
// existing" mean. Deliberately a closed mapping: a future archive_status
// value 404s rather than leaking as 200, the same default GetBySlug has
// always applied.
func archivePageStatus(c mailing.Campaign) int {
	if c.Status != mailing.CampaignStatusSent {
		// Not sent yet (or never will be — canceled/failed): the page
		// doesn't exist, same as an unknown slug. Deliberately
		// indistinguishable from ErrCampaignNotFound — see the package doc
		// comment. Independent of the ArchiveStatus switch below (#0318
		// criterion 2): a non-sent campaign is unreachable regardless of
		// what its archive_status happens to be.
		return http.StatusNotFound
	}
	switch c.ArchiveStatus {
	case mailing.ArchiveStatusPublished:
		return http.StatusOK
	case mailing.ArchiveStatusWithheld:
		// A deliberate retraction, not "never existed" — 410 Gone, per
		// #0123's "withheld must be 410, not 404" note.
		return http.StatusGone
	default:
		return http.StatusNotFound
	}
}

// GetBySlug handles GET /api/archive/{slug}. See this file's package doc
// comment for the full visibility rule (404/410/200) and why rendering
// goes through mailing.RenderMarkdownPageHTML rather than the email
// pipeline. #0522 lifted the status mapping itself into archivePageStatus,
// which PageStatus shares, so the JSON API and the HTML page can never
// disagree about a slug's visibility.
func (h *PublicArchiveHandler) GetBySlug(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	c, err := h.store.GetBySlug(r.Context(), slug)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	switch archivePageStatus(c) {
	case http.StatusOK:
		// fall through to render below.
	case http.StatusGone:
		// A deliberate retraction, not "never existed" — 410 Gone, per
		// #0123's "withheld must be 410, not 404" note.
		writeError(w, http.StatusGone, "this campaign is no longer available")
		return
	default:
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	bodyHTML, err := mailing.RenderMarkdownPageHTML(c.BodyMD)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, archiveDetailView{
		Slug:       c.Slug,
		Subject:    c.Subject,
		Preheader:  c.Preheader,
		BodyHTML:   bodyHTML,
		ArchivedAt: formatTimePtr(c.ArchivedAt),
	})
}

// PageStatus is the HTML route's twin of GetBySlug's status decision
// (#0522): GET /archive/{slug} serves the SPA shell at the SAME status the
// JSON API answers with, so a search engine sees 410 Gone for a withheld
// campaign and 404 for an unknown, draft, or unsent slug instead of a
// soft-404 200 with the SPA shell. The body is still the shell either way
// — the SPA's own archive-detail view fetches the JSON API, shows its
// withheld/not-found state, and keeps working.
//
// 500 for a store error mirrors GetBySlug: the caller can't know whether
// the slug exists, and guessing "not found" would tell a crawler to drop a
// URL that may simply be having a bad minute. The shell body rides the
// same status.
func (h *PublicArchiveHandler) PageStatus(ctx context.Context, slug string) int {
	c, err := h.store.GetBySlug(ctx, slug)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
	return archivePageStatus(c)
}

// SPAStatus satisfies the SPAHandler deep-link resolver seam (static.go,
// #0522): /archive/{slug} paths get the status archivePageStatus maps the
// campaign to, everything else reports not-mine. The trailing-slash
// normalization is ArchiveDetailSlug's own, the same one isKnownRoute and
// SPAHandler apply, so the two never disagree about which paths this
// resolver claims.
func (h *PublicArchiveHandler) SPAStatus(r *http.Request) (int, bool) {
	slug, ok := ArchiveDetailSlug(r.URL.Path)
	if !ok {
		return 0, false
	}
	return h.PageStatus(r.Context(), slug), true
}
