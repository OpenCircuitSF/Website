package mailing

import "time"

// The non-campaign messages the system sends: #0028's original four
// (confirmation, already-subscribed, registration, recovery) plus
// SendSessionsRevoked, themed consistently with the other three
// internal/auth mails by #0076. None of these carry List-Unsubscribe /
// List-Unsubscribe-Post / List-Id headers — those are RFC 8058 one-click
// headers for CAMPAIGN mail only (#0035, #0043). Adding them here would be
// wrong even though two of these templates are mailing-list-related: the
// published privacy policy (#0075) commits to "every campaign email"
// carrying one-click unsubscribe, precisely because a double opt-in
// confirmation is not a campaign, and Path 2 (the in-body footer link) is
// what covers every email, campaign or not — see ShowListFooter on
// emailContent and PRD §6.5.

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
// something to confirm — so there is no "expires in" note, and the primary
// CTA points at baseURL itself rather than a magic-link path. ShowListFooter
// is false for the same reason it is on the other two account mails: this is
// account security email, not mailing-list email, so it carries neither a
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
		ButtonURL:  baseURL,
		NoteParagraphs: []string{
			"If you did this because a device was lost or is no longer yours, also open Account settings after signing in and revoke that device's passkey. You can enroll a replacement from the same screen.",
			"If you did not do this, sign in and revoke your passkeys immediately.",
		},
		ShowListFooter: false,
	}
	return Message{To: to, Subject: c.Subject, HTMLBody: c.renderHTML(), TextBody: c.renderText()}
}
