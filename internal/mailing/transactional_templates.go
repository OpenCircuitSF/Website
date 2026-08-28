package mailing

import (
	"fmt"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// The non-campaign messages the system sends: #0028's original four
// (confirmation, already-subscribed, registration, recovery) plus
// SendSessionsRevoked, themed consistently with the other three
// internal/auth mails by #0076, plus #0126's admin_alert and #0127's
// welcome. None of confirmation/already-subscribed/registration/recovery/
// sessions_revoked/admin_alert carry List-Unsubscribe / List-Unsubscribe-Post
// / List-Id headers — those are RFC 8058 one-click headers, historically
// CAMPAIGN mail only (#0035, #0043). Adding them there would be wrong even
// though two of those five are mailing-list-related: the published privacy
// policy (#0075) commits to "every campaign email" carrying one-click
// unsubscribe, precisely because a double opt-in confirmation is not a
// campaign, and Path 2 (the in-body footer link) is what covers every
// email, campaign or not — see ShowListFooter on emailContent and PRD §6.5.
//
// BuildWelcomeEmail (#0127) is a deliberate, disclosed exception to "campaign
// mail only": it carries the RFC 8058 header set too, because #0127's
// acceptance criteria name it explicitly and the privacy policy's sentence
// is a floor ("every campaign email..."), not an exclusivity claim. See
// that function's own doc comment.

// BuildConfirmationEmail builds the double opt-in confirmation email sent
// immediately after a new (or previously-unsubscribed) signup. The button
// carries confirmToken; the footer's manage/unsubscribe links carry
// manageToken. ttl is the confirm token's actual expiry
// (subscribers.NewSignup.ConfirmTTL) — the "expires in N days" line is
// derived from it, not a separately hand-typed number, so the two can't
// drift apart. physicalAddress is settings.physical_address; passing "" (its
// current seed value — CLAUDE.md §10 item 3, not yet resolved) simply omits
// the address line rather than fabricating one.
func BuildConfirmationEmail(to, baseURL, confirmToken, manageToken string, ttl time.Duration, physicalAddress string) Message {
	confirmURL := baseURL + "/confirm?token=" + confirmToken
	manageURL := baseURL + "/preferences?token=" + manageToken
	unsubscribeURL := baseURL + "/unsubscribe?token=" + manageToken

	c := emailContent{
		Subject:   "Confirm your Open Circuit SF subscription",
		Preheader: "One click and you're on the list.",
		Eyebrow:   "$ opencircuit/confirm",
		Heading:   "Confirm your subscription",
		IntroParagraphs: []string{
			"Thanks for signing up for Open Circuit SF updates. Confirm your email address below to start receiving them.",
		},
		ButtonText: "Confirm subscription",
		ButtonURL:  confirmURL,
		NoteParagraphs: []string{
			"This link expires in " + formatDuration(ttl) + ".",
			"If you didn't request this, you can safely ignore this email — no subscription will be created.",
		},
		ShowListFooter:  true,
		ManageURL:       manageURL,
		UnsubscribeURL:  unsubscribeURL,
		PhysicalAddress: physicalAddress,
	}
	return Message{To: to, Subject: c.Subject, HTMLBody: c.renderHTML(), TextBody: c.renderText()}
}

// BuildAlreadySubscribedEmail builds the email sent when someone submits the
// signup form for an address that is already status=active. It carries the
// preference-center link (manageToken), never a confirm link — there is
// nothing to confirm.
func BuildAlreadySubscribedEmail(to, baseURL, manageToken, physicalAddress string) Message {
	manageURL := baseURL + "/preferences?token=" + manageToken
	unsubscribeURL := baseURL + "/unsubscribe?token=" + manageToken

	c := emailContent{
		Subject:   "You're already subscribed to Open Circuit SF",
		Preheader: "This address is already on the list.",
		Eyebrow:   "$ opencircuit/subscribe",
		Heading:   "You're already on the list",
		IntroParagraphs: []string{
			"This email address is already subscribed to Open Circuit SF updates — no action is needed.",
			"Want to change what you get, or leave the list? Use the link below.",
		},
		ButtonText: "Manage your preferences",
		ButtonURL:  manageURL,
		NoteParagraphs: []string{
			"If you didn't just try to sign up, you can safely ignore this email.",
		},
		ShowListFooter:  true,
		ManageURL:       manageURL,
		UnsubscribeURL:  unsubscribeURL,
		PhysicalAddress: physicalAddress,
	}
	return Message{To: to, Subject: c.Subject, HTMLBody: c.renderHTML(), TextBody: c.renderText()}
}

// BuildRegistrationEmail builds the passkey-registration magic-link email,
// re-themed from the ShortLinks plain-text original (internal/auth's earlier
// SendVerification) into the terminal HTML/text pair. ttl is
// auth.registrationTTL, passed in rather than imported so this package has
// no dependency on internal/auth.
func BuildRegistrationEmail(to, baseURL, token string, ttl time.Duration) Message {
	link := baseURL + "/register/verify?token=" + token

	c := emailContent{
		Subject:   "Verify your Open Circuit SF account",
		Preheader: "Verify your email to finish setting up your passkey.",
		Eyebrow:   "$ opencircuit/register",
		Heading:   "Verify your account",
		IntroParagraphs: []string{
			"Welcome to Open Circuit SF.",
			"Click the {{cta}} below to verify your email and add a passkey.",
		},
		ButtonText: "Verify email",
		ButtonURL:  link,
		NoteParagraphs: []string{
			"This link expires in " + formatDuration(ttl) + ".",
			"If you did not request this, you can ignore this email.",
		},
		ShowListFooter: false,
	}
	return Message{To: to, Subject: c.Subject, HTMLBody: c.renderHTML(), TextBody: c.renderText()}
}

// BuildRecoveryEmail builds the single-use account-recovery magic-link
// email, re-themed the same way as BuildRegistrationEmail. ttl is
// auth.recoveryTTL.
func BuildRecoveryEmail(to, baseURL, token string, ttl time.Duration) Message {
	link := baseURL + "/recover/verify?token=" + token

	c := emailContent{
		Subject:   "Recover your Open Circuit SF account",
		Preheader: "Use this link to register a new passkey.",
		Eyebrow:   "$ opencircuit/recover",
		Heading:   "Recover your account",
		IntroParagraphs: []string{
			"A recovery link was requested for your Open Circuit SF account.",
			"Click the {{cta}} below to register a new passkey.",
		},
		ButtonText: "Recover account",
		ButtonURL:  link,
		NoteParagraphs: []string{
			"This link expires in " + formatDuration(ttl) + ".",
			"If you did not request this, you can ignore this email; your existing passkeys remain valid.",
		},
		ShowListFooter: false,
	}
	return Message{To: to, Subject: c.Subject, HTMLBody: c.renderHTML(), TextBody: c.renderText()}
}

// BuildSessionsRevokedEmail builds the "sign out everywhere" notification,
// themed as an HTML+text pair consistent with the other three internal/auth
// mails (#0076). #0028 deliberately left this one text-only and un-themed —
// a defensible scope line for that issue — but leaving it that way
// indefinitely meant the same account holder could receive two
// differently-branded messages from one system: three terminal-styled HTML
// mails and one bare string. See issues/0076.md's Gotchas for the reasoning.
//
// Unlike BuildRegistrationEmail/BuildRecoveryEmail this carries no token and
// no single-use link — signing out everywhere is not itself an action with
// something to confirm — so there is no "expires in" note. The primary CTA
// points at baseURL+"/login", the SPA's passkey sign-in route (PRD §5.1;
// web/src/lib/router.ts maps it to web/src/views/Login.svelte, and it is
// already the canonical post-passkey destination — both
// RegisterVerify.svelte and RecoverVerify.svelte navigate there after
// enrollment). It is not a magic-link path — there's no token to carry — but
// it is a real, existing sign-in destination, not the marketing homepage:
// this is the one message where the recipient is already asking "was this
// me?", and a button labelled "Sign in" that lands on a hero and a subscribe
// form is itself the shape of a phishing lure. ShowListFooter is false for
// the same reason it is on the other two account mails: this is account
// security email, not mailing-list email, so it carries neither a
// manage/unsubscribe footer nor a physical address.
func BuildSessionsRevokedEmail(to, baseURL string, at time.Time) Message {
	c := emailContent{
		Subject:   "All sessions signed out",
		Preheader: "Every session on your account was just signed out.",
		Eyebrow:   "$ opencircuit/security",
		Heading:   "All sessions signed out",
		IntroParagraphs: []string{
			"All sessions for " + to + " were signed out on " + at.UTC().Format(time.RFC1123Z) + ".",
			"Click the {{cta}} below to sign in with your existing passkey — it still works and nothing about your account has changed.",
		},
		ButtonText: "Sign in",
		ButtonURL:  baseURL + "/login",
		NoteParagraphs: []string{
			"If you did this because a device was lost or is no longer yours, also open Account settings after signing in and revoke that device's passkey. You can enroll a replacement from the same screen.",
			"If you did not do this, sign in and revoke your passkeys immediately.",
		},
		ShowListFooter: false,
	}
	return Message{To: to, Subject: c.Subject, HTMLBody: c.renderHTML(), TextBody: c.renderText()}
}

// BuildAdminAlertEmail builds the notification #0124's delivery-health
// circuit breaker (and any future operational alert) sends to the
// configured admin address via outbox.KindAdminAlert. Deliberately small
// and generic: this issue (#0126) supplies only the template, the enqueue
// path, and a test that drains one — the trigger, the copy, and what
// counts as alert-worthy are #0124's job (see #0126's plan §3 and §7).
//
// subject becomes both the outer Message.Subject and the email's Heading;
// lines are rendered as separate paragraphs, in order — the caller passes
// already-composed operator-facing sentences, not raw data, since this
// function does no formatting of its own beyond one paragraph per line.
// The button links to the admin console root (baseURL+"/admin") — a real,
// useful destination for an operator reading this on a phone, and what
// keeps this template honest against TestAllTemplates_LinksAreAbsolute
// (every message this package builds must carry at least one absolute
// link). No manage/unsubscribe footer, no physical address: this is
// operational mail to staff, not mailing-list mail to a subscriber (same
// ShowListFooter=false reasoning as BuildSessionsRevokedEmail).
func BuildAdminAlertEmail(to, baseURL, subject string, lines []string) Message {
	c := emailContent{
		Subject:         subject,
		Preheader:       subject,
		Eyebrow:         "$ opencircuit/admin-alert",
		Heading:         subject,
		IntroParagraphs: lines,
		ButtonText:      "View admin dashboard",
		ButtonURL:       baseURL + "/admin",
		ShowListFooter:  false,
	}
	return Message{To: to, Subject: c.Subject, HTMLBody: c.renderHTML(), TextBody: c.renderText()}
}

// BuildWelcomeEmail builds the message sent once a subscriber confirms via
// double opt-in (#0127, PRD §6.3) — internal/subscribers.Store.Confirm
// enqueues it (kind='welcome') inside the same transaction that activates
// the row, so a committed confirmation can never leave the welcome unsent
// (mirroring #0126's "committed signup can never have an unsent
// confirmation" property one step later in the flow).
//
// Unlike every template above, this one carries the RFC 8058 one-click
// List-Unsubscribe / List-Unsubscribe-Post / List-Id header set (#0035) via
// the same CampaignHeaders helper #0043's campaign sends use — a
// deliberate, disclosed departure from this file's package doc comment
// ("None of these carry List-Unsubscribe... for CAMPAIGN mail only"):
// #0127's acceptance criteria name the headers explicitly for this one
// message. It does not contradict the privacy policy's "every campaign
// email carries a one-click unsubscribe link" (PrivacyPolicy.svelte) —
// that sentence is a floor commitment, not a claim that no other message
// ever carries the header — and TestNoTransactionalMessageCarriesCampaignHeaders
// (templates_test.go) is updated to assert this positively for "welcome"
// going forward, the same way it already does for "campaign".
//
// interestNames is the subscriber's selected interest names AT CONFIRM TIME
// (internal/subscribers.Store.Confirm reads them inside its own
// transaction, not re-read at send time) — a later preference change does
// not retroactively rewrite what this one email already said. An empty
// slice renders a general-announcements sentence instead of a list.
//
// No /archive link: #0127 depends on #0123 (the public campaign archive),
// which had not landed when this was implemented — issues/0127.md's own
// Notes say to ship the welcome without the link rather than point it at a
// page that does not exist yet, rather than block on #0123. Add the link
// once #0123 lands.
func BuildWelcomeEmail(to, baseURL, listDomain, manageToken string, interestNames []string, physicalAddress string) Message {
	manageURL := baseURL + "/preferences?token=" + manageToken
	unsubscribeURL := baseURL + "/unsubscribe?token=" + manageToken
	workshopsURL := baseURL + "/workshops"

	interestLine := "You didn't pick any specific topics, so you'll get general announcements about upcoming workshops."
	if len(interestNames) > 0 {
		interestLine = "You picked: " + joinWithAnd(interestNames) + "."
	}

	c := emailContent{
		Subject:   "Welcome to Open Circuit SF",
		Preheader: "You're confirmed — here's what to expect.",
		Eyebrow:   "$ opencircuit/welcome",
		Heading:   "You're on the list",
		IntroParagraphs: []string{
			"Open Circuit SF is a San Francisco group running hands-on electronics workshops — soldering, microcontrollers, homelab, home automation, and whatever the room wants to build next. You're confirmed, and we're glad you're here.",
			interestLine,
			// "Roughly once a month" is a deliberately soft estimate, not a
			// contractual cadence: PRD §4.1's own reference layout example
			// ("[ OK ] monthly · beginners") is the only cadence figure
			// anywhere in this project's source (see web/src/views/About.svelte's
			// identical flag on the same number) — replace this sentence if
			// the group ever settles on a firmer answer.
			"Expect an email roughly once a month when a new workshop goes up — we don't send more than that.",
		},
		ButtonText: "See upcoming workshops",
		ButtonURL:  workshopsURL,
		NoteParagraphs: []string{
			"Change what you get, or leave the list entirely, any time from the preference center below.",
		},
		ShowListFooter:  true,
		ManageURL:       manageURL,
		UnsubscribeURL:  unsubscribeURL,
		PhysicalAddress: physicalAddress,
	}
	msg := Message{To: to, Subject: c.Subject, HTMLBody: c.renderHTML(), TextBody: c.renderText()}
	msg.Headers = CampaignHeaders(baseURL, listDomain, manageToken)
	return msg
}

// importSourceLabels renders subscribers.ImportSource* constants into the
// natural-language phrase importInviteProvenanceSentence needs. The values
// match subscriber_imports_source_check (migrations/000023) via
// subscribers.ImportSource* exactly — Commit validates against that same
// closed set before this template is ever reached, so the map's absent-key
// fallback below is defensive, not a path this package expects to exercise.
var importSourceLabels = map[string]string{
	subscribers.ImportSourceLuma:       "our Luma event list",
	subscribers.ImportSourceEventbrite: "our Eventbrite export",
	subscribers.ImportSourceMeetup:     "our Meetup export",
	subscribers.ImportSourceManualCSV:  "our records",
	subscribers.ImportSourceOther:      "our records",
}

// importSourceLabel returns source's natural-language phrase, falling back
// to "our records" for an unrecognized value rather than panicking or
// leaving a raw enum value in outbound copy.
func importSourceLabel(source string) string {
	if label, ok := importSourceLabels[source]; ok {
		return label
	}
	return "our records"
}

// importInviteProvenanceSentence builds the sentence PRD §6.10.1 requires
// and calls "not optional copy": "You gave us this address when you signed
// up for [source_detail] through our Google Form on 12 May 2026." — built
// from source, sourceDetail, and collectedAt, the same three fields #0125's
// review named as "mandatory... for the second reason" this template
// exists. collectedAt renders as "2 January 2006" (no leading zero on the
// day), matching the PRD's own example figure.
func importInviteProvenanceSentence(source, sourceDetail string, collectedAt time.Time) string {
	return fmt.Sprintf("You gave us this address when you signed up for %s through %s on %s.",
		sourceDetail, importSourceLabel(source), collectedAt.Format("2 January 2006"))
}

// BuildImportInviteEmail builds the invitation an admin-only CSV import
// sends under consent_mode=invite (#0129, PRD §6.10.1): the address was
// never asked directly, so this message asks, naming exactly where it came
// from, and its confirm button carries the SAME confirm_token shape and
// lands on the SAME /confirm route the public double opt-in flow uses —
// internal/subscribers.Store.Confirm, not a second confirmation path
// (#0129's acceptance criteria: "same endpoint, same token shape, no second
// confirmation route").
//
// importSource/sourceDetail/collectedAt are captured from the
// subscriber_imports row AT ENQUEUE TIME
// (internal/subscribers.ImportStore.Commit's invite branch), not re-read at
// send time — the same "captured, not re-derived" convention
// BuildWelcomeEmail's interestNames follows (see that function's own doc
// comment), so nothing can retroactively rewrite what an already-queued
// invitation says.
//
// Unlike every OTHER template in this file except BuildWelcomeEmail, this
// message carries the RFC 8058 one-click header set (via CampaignHeaders,
// same as BuildWelcomeEmail) and — mirroring #0264's welcome gate exactly —
// treats a blank physicalAddress as commercial mail requiring one: this
// message solicits consent from someone who never asked for it and carries
// a one-click decline, which is what makes CAN-SPAM §7704 apply the same
// way it does to a campaign. This function itself does not refuse on a
// blank physicalAddress (it simply omits the address line, matching every
// non-Welcome template's convention) — the refusal lives in
// outbox_worker.go's render, which is what decides whether to call this
// function at all; see errImportInviteMissingPhysicalAddress's doc comment.
//
// The footer's ManageURL/UnsubscribeURL point at the SAME /preferences and
// /unsubscribe routes (and therefore the SAME POST /api/unsubscribe
// handler) every other list email uses — internal/subscribers.Store.
// Unsubscribe itself is what detects a still-pending, unconfirmed
// invitation being declined through that shared path and additionally
// suppresses the address (see that method's own doc comment for why reusing
// the existing endpoint, rather than building a second one, is the safer
// choice here).
func BuildImportInviteEmail(to, baseURL, listDomain, confirmToken, manageToken, importSource, sourceDetail string, collectedAt time.Time, ttl time.Duration, physicalAddress string) Message {
	confirmURL := baseURL + "/confirm?token=" + confirmToken
	manageURL := baseURL + "/preferences?token=" + manageToken
	unsubscribeURL := baseURL + "/unsubscribe?token=" + manageToken

	c := emailContent{
		Subject:   "You're invited to the Open Circuit SF mailing list",
		Preheader: "Confirm below, or do nothing and you won't hear from us again.",
		Eyebrow:   "$ opencircuit/invite",
		Heading:   "Confirm to join the list",
		IntroParagraphs: []string{
			importInviteProvenanceSentence(importSource, sourceDetail, collectedAt),
			"We're starting an email list for workshop announcements — click the {{cta}} below if you'd like to be on it.",
		},
		ButtonText: "Confirm subscription",
		ButtonURL:  confirmURL,
		NoteParagraphs: []string{
			"This link expires in " + formatDuration(ttl) + ".",
			"If you do nothing, you won't hear from us again — we won't send a reminder.",
		},
		ShowListFooter:  true,
		ManageURL:       manageURL,
		UnsubscribeURL:  unsubscribeURL,
		PhysicalAddress: physicalAddress,
	}
	msg := Message{To: to, Subject: c.Subject, HTMLBody: c.renderHTML(), TextBody: c.renderText()}
	msg.Headers = CampaignHeaders(baseURL, listDomain, manageToken)
	return msg
}

// joinWithAnd renders a list of names the way a person would say them:
// "a", "a and b", "a, b, and c". No dependency pulled in for this — the
// project has no other prose-list joiner, and the golden tests below pin
// exact output for one- two- and three-item cases.
func joinWithAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", and " + items[len(items)-1]
	}
}
