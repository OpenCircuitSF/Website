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
	// convention as ActionAccountRegistrationStarted above. Later phases
	// (#0030's subscriber.confirmed, #0034's subscriber.unsubscribed, per
	// PRD §6.3/§6.5) add their own constants here when they land.
	ActionSubscriberSignup = "subscriber.signup"

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
