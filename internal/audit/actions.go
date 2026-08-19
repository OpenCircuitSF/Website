package audit

// Action constants are the canonical event-type strings written to
// audit_log.action. They reproduce the PRD's Action catalogue verbatim; every
// call site references one of these rather than a string literal so a typo
// cannot silently fragment the log.
const (
	// Account lifecycle.
	ActionAccountRegistrationStarted = "account.registration_started" // actor NULL (pre-auth)
	ActionAccountRegistered          = "account.registered"
	ActionAccountLogin               = "account.login"
	ActionAccountLogout              = "account.logout"
	ActionAccountRecoveryStarted     = "account.recovery_started" // actor NULL (pre-auth)
	ActionAccountRecovered           = "account.recovered"
	// account.deactivated / account.reactivated are admin-on-other-user actions
	// that belong to admin user management. They have no call site yet;
	// the constants are defined here so that feature can use the same API
	// trivially when it lands.
	ActionAccountDeactivated = "account.deactivated"
	ActionAccountReactivated = "account.reactivated"

	// Credential lifecycle.
	ActionCredentialAdded   = "credential.added"
	ActionCredentialRevoked = "credential.revoked"

	// Session lifecycle. account.logout (above) revokes exactly one session by
	// its cookie token; session.revoked_all is the bulk "sign out everywhere"
	// action that revokes every session for the account in one call. It
	// is deliberately not named account.* — it never touches the users row or
	// any passkey_credentials row, only sessions.
	ActionSessionsRevokedAll = "session.revoked_all"

	// Link lifecycle.
	ActionLinkCreated     = "link.created"
	ActionLinkDeactivated = "link.deactivated"
	ActionLinkReactivated = "link.reactivated"
	ActionLinkDenied      = "link.denied"

	// URL filter rule lifecycle.
	ActionURLFilterCreated = "url_filter.created"
	ActionURLFilterUpdated = "url_filter.updated"
	ActionURLFilterDeleted = "url_filter.deleted"

	// Settings.
	ActionSettingsUpdated = "settings.updated"

	// Subscriber lifecycle (PRD §6.3). ActionSubscriberSignup covers both a
	// brand-new signup and the "unsubscribed → treat as new signup" restart
	// path (#0026) — actor NULL (pre-auth, the visitor is anonymous), same
	// convention as ActionAccountRegistrationStarted above.
	ActionSubscriberSignup = "subscriber.signup"
	// ActionSubscriberConfirmed is written by POST /api/subscribe/confirm
	// (#0030) when a pending subscriber's token is successfully consumed and
	// the row transitions to active. Actor NULL (pre-auth, the visitor is
	// anonymous) — same convention as ActionSubscriberSignup.
	ActionSubscriberConfirmed = "subscriber.confirmed"
	// ActionSubscriberPreferencesUpdated is written by PATCH
	// /api/preferences (#0031) whenever a subscriber's interest selection is
	// replaced. Actor NULL — the caller is authenticated only by possessing
	// manage_token, not a real account.
	ActionSubscriberPreferencesUpdated = "subscriber.preferences_updated"
	// ActionSubscriberUnsubscribed is the general "a subscriber left the
	// list" action, shared across every path that can produce it: the
	// preference center's explicit "Unsubscribe from everything" action
	// (#0031, source=preferences) and #0034's one-click email-footer link
	// (source=one_click, once it lands) both write this SAME action —
	// distinguished by metadata.source, exactly like
	// subscribers.Store.Unsubscribe's own unsubscribe_source column
	// (SourceOneClick/SourcePreferences/SourceMailto/SourceAdmin). This is
	// the constant this file's own doc comment previously earmarked for
	// #0034; #0031 defines it first since it needed a real "unsubscribe from
	// everything" action before #0034 existed, and #0034 should reuse it
	// rather than mint a second name for the same event.
	ActionSubscriberUnsubscribed = "subscriber.unsubscribed"

	// Subscriber admin actions (PRD §5.2, §6.5; #0032's admin screen).
	// Actor is always the acting admin, unlike ActionSubscriberSignup above.
	//
	// ActionSubscriberSuppressed is written by POST
	// /admin/subscribers/{id}/suppress. It calls subscribers.Store.Unsubscribe
	// (source=admin) — the only sanctioned status-mutating action available
	// today — NOT a write to the `suppressions` table PRD §6.2 describes,
	// because that table and its store (#0033) do not exist yet. The metadata
	// records which one happened via `no_op` (true when the target was
	// already complained, per CLAUDE.md §9 — Unsubscribe changes nothing in
	// that case) so the log itself carries the caveat, not just this comment.
	ActionSubscriberSuppressed = "subscriber.suppressed"
	// ActionSubscriberComplaintCleared is written by POST
	// /admin/subscribers/{id}/clear-complaint, the sole sanctioned exit from
	// `complained` (subscribers.Store.AdminClearComplaint). Metadata notes
	// that the resulting status is `unsubscribed`, not `active` — clearing a
	// complaint does not re-establish double opt-in consent — and that
	// #0033's suppressions table, once it exists, still needs a matching
	// removal for this action to actually unblock the address at #0026's
	// suppressed send gate.
	ActionSubscriberComplaintCleared = "subscriber.complaint_cleared"
	// ActionSubscriberManualAdd is written by POST /admin/subscribers, the
	// admin manual-add flow. It never creates an `active` subscriber
	// directly (PRD §5.2's notes) — it drives the same
	// newSignup/existingSignup dispatch #0026's public endpoint uses, so a
	// brand-new address lands `pending` with a confirmation email queued,
	// exactly as if the person had submitted the public form themselves.
	ActionSubscriberManualAdd = "subscriber.manual_add"

	// Interest taxonomy lifecycle (PRD §6.1, §5.2 — the admin CRUD, #0024).
	// ActionInterestUpdated covers any field change other than the active
	// flag's transition; that transition gets its own two actions (mirroring
	// account.deactivated/reactivated above) because "an interest just
	// disappeared from the signup form" is exactly the kind of change an
	// operator needs to spot in the log without decoding metadata.
	ActionInterestCreated     = "interest.created"
	ActionInterestUpdated     = "interest.updated"
	ActionInterestDeactivated = "interest.deactivated"
	ActionInterestReactivated = "interest.reactivated"
	// ActionInterestDeleted is only ever written for the narrow hard-delete
	// path (interests.Store.Delete) that succeeds solely when zero
	// subscribers ever selected the interest — never for the common case of
	// retiring one with history, which is ActionInterestDeactivated.
	ActionInterestDeleted = "interest.deleted"
)

// Deleted (#0068): ActionCampaignCreated/Updated/Deleted and
// ActionCampaignLinkAssigned/Unassigned, plus the TargetCampaign target-type
// constant below. These were ShortLinks' campaign.* actions for its
// campaigns.Store (grouping short links) — that package was deleted in #0002
// and nothing in this codebase referenced these constants. They are NOT this
// project's email_campaigns concept (a message sent to a mailing-list
// segment, PRD §6.6) and must not be reused for it — CLAUDE.md §6 and PRD
// §3.2 both warn the two share only a word. If Phase 5 (#0044-#0048) needs
// campaign-lifecycle audit actions for email_campaigns, define new
// email_campaign.* constants; do not resurrect these.

// Target-type constants are the canonical values written to
// audit_log.target_type. They mirror the PRD's enumerated entity kinds.
const (
	TargetLink       = "link"
	TargetUser       = "user"
	TargetCredential = "credential"
	TargetSettings   = "settings"
	TargetURLFilter  = "url_filter"
	TargetSubscriber = "subscriber"
	TargetInterest   = "interest"
)
