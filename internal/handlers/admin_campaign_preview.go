// Admin campaign preview and test send (#0046; PRD §5.2/§6.6): POST
// /admin/campaigns/{id}/preview renders a campaign's stored content without
// sending anything, and POST /admin/campaigns/{id}/test delivers one real
// message so an operator can review it — and click its unsubscribe link —
// before a real send is even possible (#0045's Preflight requires
// test_sent_at to be non-nil; see PreflightCodeNoTestSend).
//
// A separate handler and file, following AdminCampaignAudienceHandler's
// precedent (#0044) rather than growing AdminCampaignsHandler further.
//
// # Neither route accepts a request body — by construction, not convention
//
// #0047's plan (carried in here) forbids letting the caller preview or test
// unsaved content: #0047's editor autosaves before calling either route, so
// both must render the STORED row, never a client-supplied override. Rather
// than accept a body and simply ignore an override field, Preview and Test
// never call decodeJSON at all — there is no field in either request this
// handler ever reads, so "an unsaved-body override" is not a validation
// rule that could be forgotten later, it is a code path that does not
// exist. See TestAdminCampaignPreview_Preview_IgnoresRequestBody and
// TestAdminCampaignPreview_Test_IgnoresRequestBody, and this file's own
// mutation-tested proof in admin_campaign_preview_test.go.
//
// # Test send recipient: always the requesting admin's own address
//
// Test never accepts a destination address. An admin-suppliable "send to
// any address" parameter would turn this endpoint into an open relay for
// attacker-chosen HTML — a session-authenticated one, but still a relay,
// since the body content itself is fully attacker-controlled the moment
// they can also edit the campaign. The recipient is always
// middleware.AuthUser.Email, read from the session the same RequireAdmin
// guard already authenticated — never a request field. See
// TestAdminCampaignPreview_Test_SendsToRequestingAdminOnly.
//
// # Why the message is addressed to a DEDICATED subscriber row, not the admin's real one
//
// The unsubscribe link in a test message must be a real, clickable,
// working manage_token (#0046's acceptance criteria: "so the unsubscribe
// links can be clicked and verified") — the only place FindByManageToken
// resolves one is the subscribers table. Naively reusing (or creating) a
// subscribers row AT the admin's own email address is unsafe two ways: (1)
// if that admin is ALSO a genuine list member (plausible — nothing stops
// staff from subscribing to their own newsletter), clicking the test's
// unsubscribe link would unsubscribe their REAL subscription, and (2) even
// ignoring that, a row with status='active' at that address would appear
// in a real "all" campaign's audience count, silently adding the tester as
// a recipient of campaigns they never opted into as a subscriber.
//
// Instead, ensureTestRecipient finds-or-creates a SEPARATE, synthetic
// subscribers row per requesting admin
// (campaign-test+admin-<id>@internal.opencircuitsf.test — the .test TLD is
// RFC 2606-reserved and never resolves) and rotates a fresh manage_token on
// it before every test send. That row stays status='pending' for its
// entire life (Store.Create's own default; nothing here ever confirms it),
// which keeps it out of EVERY audience predicate — internal/mailing's
// eligibility SQL filters status = 'active' everywhere, unconditionally
// (audience.go, worker_store.go) — regardless of audience_mode, without
// this file needing to special-case "all" against any other mode. The
// actual message is still addressed (Message.To) to the admin's REAL
// inbox; only the manage_token — the thing an unsubscribe click acts on —
// is anchored to this disposable row. Clicking it can only ever unsubscribe
// the synthetic row, which is not, and was never, a real list member. See
// TestAdminCampaignPreview_Test_UsesDedicatedRecipientRow_NotARealSubscriber.
//
// # The synthetic row must be invisible to the admin subscribers screen — #0046's review, finding A
//
// status='pending' keeps the row out of every AUDIENCE (above), but it does
// NOT keep it off #0032's admin subscribers screen: that screen's
// StatusCounts and List (internal/subscribers/store.go) had no exclusion of
// any kind, so every test send permanently inflated the "pending" bucket by
// one and listed the fixture in the subscriber table — proven empirically
// on a private database by the review that bounced this issue's first
// pass. Fixed at the schema layer: migration 000019 adds
// subscribers.synthetic (NOT NULL DEFAULT false); ensureTestRecipient's
// Create call below sets NewSignup.Synthetic: true; StatusCounts and List
// both exclude synthetic=true unconditionally. audienceWhere is untouched —
// it already excluded this row correctly and does not need the flag.
//
// # The address must also be unmailable by the public subscribe endpoint — #0046's review, finding B
//
// testRecipientEmail's format (campaign-test+admin-<id>@...) is
// deterministic and public in this file's own source; admin ids are small,
// enumerable integers. Before this fix, POSTing that address to
// /api/subscribe reached subscribe.go's pending branch and attempted a real
// SES send to a domain that can never resolve — a guaranteed hard bounce,
// on demand, from an unauthenticated caller. subscribers.
// ReservedTestEmailDomain / IsReservedTestEmail (internal/subscribers/
// store.go) is the single source of truth for that domain string;
// SubscribeHandler.processMutateJob (subscribe.go) checks it and silently
// no-ops, exactly like its existing isBot/suppressed checks, for ANY
// address at that domain — not only ones a synthetic row already exists
// for, since the enumeration risk exists whether or not this admin has
// ever run a test send. That check runs strictly after Subscribe has
// already written the uniform 202 to the (long-since-returned) response, so
// it cannot introduce an observable branch in that endpoint's behavior
// (CLAUDE.md §9).
//
// # What Preflight subset a test send requires, and why
//
// Test does NOT call mailing.Preflight and does not require ANY of its ten
// checks — deliberately, not by oversight:
//
//   - physical_address_missing / reply_to_missing / list_domain_unset are
//     deploy-configuration prerequisites for a REAL send to subscribers
//     (CAN-SPAM §7704 for the address; PRD §6.6 for reply-to). Requiring
//     them here would make it impossible to review a campaign's rendering
//     before that configuration lands — exactly backwards, since testing
//     early is how an operator notices the address is still blank. CLAUDE.md
//     §9's physical_address restriction is about the SEND WORKER's gate
//     (the one that actually delivers to subscribers) staying
//     unbypassable from the UI; it says nothing about an internal QA copy
//     mailed only to the operator's own inbox, which is not the
//     "commercial email message" CAN-SPAM regulates delivery of. The send
//     worker's own copy of Preflight (worker.go, authoritative,
//     unconditional) is completely untouched by this file — a campaign
//     that has been test-sent a hundred times still cannot leave
//     'scheduled' without physical_address set.
//   - no_test_send is definitionally circular here — this IS the test
//     send.
//   - audience_invalid / empty_audience are properties of the AUDIENCE this
//     campaign would mail for real; Test never touches the audience, so
//     they don't apply.
//   - subject_empty / body_empty / body_render_failed are not enforced as
//     a separate gate: rendering an empty subject or body is not a crash,
//     and letting an operator test-send exactly that is the useful
//     feedback loop ("that looks empty, let me fix it") the whole feature
//     exists for. A genuine render failure (a panic RenderCampaign's own
//     recover() converts to an error) still surfaces as a 422 — not
//     because this file re-implements PreflightCodeBodyRenderFailed, but
//     because BuildCampaignMessage cannot succeed without a rendered body.
//
// The one thing Test DOES require, unconditionally, is that outbound email
// is actually configured — h.mailer == nil (MAILER_NOOP=true, or a
// deploy that never wired one in) answers 503 before touching the database
// at all. That is not a Preflight code; it is the same operational
// precondition #0027's MAILER_NOOP guard already enforces at process
// startup for every other outbound path in this project. See
// TestAdminCampaignPreview_Test_NilMailer_Returns503.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// settingPhysicalAddress mirrors internal/mailing's own unexported constant
// of the same name (worker_store.go) — the settings.key this file reads to
// populate CampaignRenderInput.PhysicalAddress. Kept as a literal here
// rather than exported from internal/mailing solely for this one read: the
// key is also asserted directly (as a string literal) by
// internal/handlers/settings_test.go and admin_campaigns_test.go, so this
// is already how the handlers package refers to it.
const settingPhysicalAddress = "physical_address"

// previewManageToken is the sentinel substituted into a Preview response's
// footer links. It is NOT a real, resolvable manage_token — no subscribers
// row is ever created for it — so a preview's "Manage your interests" /
// "Unsubscribe from everything" links render with the correct shape but are
// never clickable. That is intentional: unlike Test (whose whole point is a
// working, verifiable unsubscribe link), Preview's contract is "render
// without sending anything" — provisioning a real subscribers row on every
// preview request (which #0047's editor calls on every save) would leak a
// database row per keystroke-triggered autosave.
const previewManageToken = "PREVIEW-DRY-RUN"

// testRecipientEmail returns the deterministic, per-admin synthetic address
// ensureTestRecipient finds-or-creates a subscribers row for, at
// subscribers.ReservedTestEmailDomain — the single source of truth for that
// domain string, also consulted by subscribe.go's reserved-domain guard (see
// this file's package doc comment's "dedicated subscriber row" section and
// #0046's review finding B). Deterministic (not random) so a second test
// send by the same admin reuses the same row — see the package doc comment
// for why a SEPARATE row per admin (not one global row) avoids a
// token-rotation race between two admins testing concurrently.
func testRecipientEmail(adminID int64) string {
	return fmt.Sprintf("campaign-test+admin-%d@%s", adminID, subscribers.ReservedTestEmailDomain)
}

// campaignPreviewStore is the campaign-data behavior this file needs.
// *mailing.CampaignStore (#0041/#0046) satisfies it.
type campaignPreviewStore interface {
	GetByID(ctx context.Context, id int64) (mailing.Campaign, error)
	MarkTestSent(ctx context.Context, id int64, at time.Time) (mailing.Campaign, error)
}

// testRecipientStore is the narrow subscribers-data behavior
// ensureTestRecipient needs. *subscribers.Store satisfies it.
type testRecipientStore interface {
	FindByEmail(ctx context.Context, email string) (subscribers.Subscriber, error)
	Create(ctx context.Context, in subscribers.NewSignup, now time.Time) (subscribers.Subscriber, error)
	RotateManageToken(ctx context.Context, id int64, now time.Time) (subscribers.Subscriber, error)
}

// AdminCampaignPreviewHandler serves POST /admin/campaigns/{id}/preview and
// POST /admin/campaigns/{id}/test. Must be mounted behind
// middleware.RequireSession then middleware.RequireAdmin, exactly like
// every other admin handler — see cmd/opencircuit/main.go's adminRoutes.
//
// mailer may be nil (MAILER_NOOP=true, or a deploy/test that never wired
// one in): Test answers 503 rather than attempting a send; Preview is
// unaffected, since it never sends.
type AdminCampaignPreviewHandler struct {
	campaigns  campaignPreviewStore
	subs       testRecipientStore
	settings   mailing.SettingsReader
	render     mailing.CampaignRenderer
	mailer     mailing.Mailer
	auditor    *audit.Logger
	baseURL    string
	listDomain string
	fromAddr   string
	now        func() time.Time
}

// NewAdminCampaignPreviewHandler constructs an AdminCampaignPreviewHandler.
// baseURL/listDomain/fromAddr are config.Config.BaseURL/EmailListDomain/
// EmailFrom — the same three values #0045's SendStore and Worker are
// constructed with, so a test send builds byte-identical URLs and headers
// to a real one. A nil auditor disables audit writes (matching every other
// admin handler's nil-tolerance); a nil mailer disables Test (503) without
// affecting Preview.
func NewAdminCampaignPreviewHandler(
	campaigns campaignPreviewStore,
	subs testRecipientStore,
	settings mailing.SettingsReader,
	render mailing.CampaignRenderer,
	mailer mailing.Mailer,
	auditor *audit.Logger,
	baseURL, listDomain, fromAddr string,
) *AdminCampaignPreviewHandler {
	return &AdminCampaignPreviewHandler{
		campaigns:  campaigns,
		subs:       subs,
		settings:   settings,
		render:     render,
		mailer:     mailer,
		auditor:    auditor,
		baseURL:    baseURL,
		listDomain: listDomain,
		fromAddr:   fromAddr,
		now:        time.Now,
	}
}

// physicalAddress reads the physical_address setting for both Preview and
// Test. A read error (including "no such setting", e.g. a deploy that
// skipped migration 000008) is treated as "unset" — never a hard failure —
// matching PreflightInput.SettingsErr's own "unreadable is missing"
// treatment (preflight.go).
func (h *AdminCampaignPreviewHandler) physicalAddress(ctx context.Context) string {
	addr, err := h.settings.GetSetting(ctx, settingPhysicalAddress)
	if err != nil {
		return ""
	}
	return addr
}

func campaignPreheader(c mailing.Campaign) string {
	if c.Preheader == nil {
		return ""
	}
	return *c.Preheader
}

// ── POST /admin/campaigns/{id}/preview ───────────────────────────────────

type campaignPreviewResponse struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

// Preview handles POST /admin/campaigns/{id}/preview: renders the STORED
// campaign row's content through the exact same mailing.CampaignRenderer
// (and therefore the exact same goldmark-based, no-remote-asset,
// no-raw-HTML rendering #0042/#0043 built) a real send or test send would
// use, with a non-functional sentinel manage token substituted into the
// footer links. Never sends anything, never touches email_sends,
// email_campaigns, or subscribers.
func (h *AdminCampaignPreviewHandler) Preview(w http.ResponseWriter, r *http.Request) {
	id, ok := parseCampaignID(w, r)
	if !ok {
		return
	}

	campaign, err := h.campaigns.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	htmlBody, textBody, err := h.render.Campaign(mailing.CampaignRenderInput{
		Subject:      campaign.Subject,
		Preheader:    campaignPreheader(campaign),
		BodyMarkdown: campaign.BodyMD,
		BaseURL:      h.baseURL,
		// Slug (#0123): the preview/test-send must show the same "View
		// this email in your browser" link a real send would render, so
		// an operator previewing sees exactly what recipients will get.
		Slug:            campaign.Slug,
		ManageToken:     previewManageToken,
		PhysicalAddress: h.physicalAddress(r.Context()),
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "campaign body does not render: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, campaignPreviewResponse{
		Subject: campaign.Subject,
		HTML:    htmlBody,
		Text:    textBody,
	})
}

// ── POST /admin/campaigns/{id}/test ──────────────────────────────────────

// Test handles POST /admin/campaigns/{id}/test: renders the stored campaign
// exactly as Preview does, assembles the SAME Message a real send would
// (subject, HTML/text bodies, and the RFC 8058 List-Unsubscribe /
// List-Unsubscribe-Post / List-Id headers via mailing.CampaignHeaders — see
// this file's package doc comment for why fidelity to the real send path
// matters more here than the "not a commercial mailing" distinction), and
// delivers it through the real mailer to the requesting admin's own
// address. On success, stamps email_campaigns.test_sent_at.
func (h *AdminCampaignPreviewHandler) Test(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, ok := parseCampaignID(w, r)
	if !ok {
		return
	}

	// Checked first, before any database write: an unconfigured mailer
	// means this request can go no further regardless of what the campaign
	// or the recipient row look like. See the package doc comment's
	// "Preflight subset" section for why this is the one precondition Test
	// enforces.
	if h.mailer == nil {
		writeError(w, http.StatusServiceUnavailable, "outbound email is not configured")
		return
	}

	campaign, err := h.campaigns.GetByID(r.Context(), id)
	switch {
	case err == nil:
	case errors.Is(err, mailing.ErrCampaignNotFound):
		writeError(w, http.StatusNotFound, "campaign not found")
		return
	default:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	now := h.now()
	sub, err := h.ensureTestRecipient(r.Context(), actor.ID, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	htmlBody, textBody, err := h.render.Campaign(mailing.CampaignRenderInput{
		Subject:      campaign.Subject,
		Preheader:    campaignPreheader(campaign),
		BodyMarkdown: campaign.BodyMD,
		BaseURL:      h.baseURL,
		// Slug (#0123): the preview/test-send must show the same "View
		// this email in your browser" link a real send would render, so
		// an operator previewing sees exactly what recipients will get.
		Slug:            campaign.Slug,
		ManageToken:     sub.ManageToken,
		PhysicalAddress: h.physicalAddress(r.Context()),
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "campaign body does not render: "+err.Error())
		return
	}

	// Mirrors mailing.BuildCampaignMessage exactly (RenderCampaign +
	// CampaignHeaders) but goes through h.render — the injected
	// CampaignRenderer seam — rather than calling BuildCampaignMessage
	// directly, so tests can exercise this path with a fake renderer the
	// same way Preview's test does, instead of only ever hitting the real
	// goldmark pipeline.
	msg := mailing.Message{
		To:       actor.Email,
		From:     mailing.ResolveFromHeader(r.Context(), h.settings, h.fromAddr),
		Subject:  campaign.Subject,
		HTMLBody: htmlBody,
		TextBody: textBody,
		Headers:  mailing.CampaignHeaders(h.baseURL, h.listDomain, sub.ManageToken),
	}

	sendCtx, cancel := context.WithTimeout(r.Context(), testSendTimeout)
	defer cancel()
	messageID, sendErr := h.mailer.Send(sendCtx, msg)
	if sendErr != nil {
		writeError(w, http.StatusBadGateway, "sending test message failed: "+sendErr.Error())
		return
	}

	updated, err := h.campaigns.MarkTestSent(r.Context(), id, now)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		targetID := id
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionEmailCampaignTestSent,
			TargetType: audit.TargetEmailCampaign,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"to":              actor.Email,
				"ses_message_id":  messageID,
				"recipient_email": sub.Email,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, toCampaignView(updated))
}

// testSendTimeout bounds the one SES round trip a test send makes. Matches
// the send worker's own sendMessageTimeout (worker.go) in spirit —
// generous enough for real network + AWS API latency under CLAUDE.md §5's
// "check the machine before you blame the code" guidance, not sized against
// any measured load.
const testSendTimeout = 30 * time.Second

// ensureTestRecipient finds or creates the dedicated, synthetic subscribers
// row this admin's test sends use as their manage_token anchor (see the
// package doc comment's "dedicated subscriber row" section), then rotates a
// fresh manage_token on it unconditionally — so every test send carries a
// token nobody has clicked yet, even if an earlier test's link was already
// used to unsubscribe this row (RotateManageToken is a no-op only when the
// row is currently 'complained', which this synthetic row can never
// become — nothing ever routes a real bounce/complaint event to a
// .test-domain address).
func (h *AdminCampaignPreviewHandler) ensureTestRecipient(ctx context.Context, adminID int64, now time.Time) (subscribers.Subscriber, error) {
	email := testRecipientEmail(adminID)

	sub, err := h.subs.FindByEmail(ctx, email)
	switch {
	case err == nil:
		// fall through to the unconditional rotation below
	case errors.Is(err, subscribers.ErrNotFound):
		created, cerr := h.subs.Create(ctx, subscribers.NewSignup{Email: email, ConfirmTTL: time.Hour, Synthetic: true}, now)
		switch {
		case cerr == nil:
			sub = created
		case errors.Is(cerr, subscribers.ErrEmailExists):
			// Lost a create race against a concurrent test send by the
			// same admin (two requests in flight at once) — the row now
			// exists; re-read it.
			sub, err = h.subs.FindByEmail(ctx, email)
			if err != nil {
				return subscribers.Subscriber{}, err
			}
		default:
			return subscribers.Subscriber{}, cerr
		}
	default:
		return subscribers.Subscriber{}, err
	}

	return h.subs.RotateManageToken(ctx, sub.ID, now)
}
