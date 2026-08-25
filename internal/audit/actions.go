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

	// SES-driven subscriber status changes (PRD §6.5, §6.7; #0038's
	// internal/sesnotify event ingestion, POST /api/ses/notifications).
	// Actor NULL — the caller is SES/SNS, not a human — same convention as
	// ActionSubscriberSignup. Written only when a subscribers row exists for
	// the recipient (metadata.source is always "ses"); an event for an
	// address with no matching subscriber still records to email_events and
	// still writes a suppressions row, but has no subscriber to attribute an
	// audit row to. ActionSubscriberBounced covers a Permanent bounce only —
	// #0039's repeated-soft-bounce suppression, when it lands, reuses this
	// same action with a distinguishing metadata field rather than minting a
	// second one, since both mean "this subscriber moved to bounced".
	ActionSubscriberBounced    = "subscriber.bounced"
	ActionSubscriberComplained = "subscriber.complained"
	// ActionSubscriberSoftBounceStreakReset is written by POST
	// /admin/deliverability/{email}/reset-streak (#0124, PRD §6.9): an
	// explicit admin override of subscribers.soft_bounce_streak, distinct
	// from the automatic resets a Delivery event or a suppression removal
	// perform (neither of which goes through this handler, so neither
	// writes this action — see internal/subscribers/events.go's
	// ActionDelivered/ActionUnsuppressed for those). Metadata carries the
	// email (target_id is omitted when no subscribers row exists for the
	// address, since GET /admin/deliverability/{email} answers 200 for an
	// arbitrary address with no row).
	ActionSubscriberSoftBounceStreakReset = "subscriber.soft_bounce_streak_reset"

	// SNS subscription-lifecycle events (PRD §6.7; #0038, carrying forward
	// #0037's plan §5). Actor NULL — pre-auth, the caller is SNS.
	// ActionSESSubscriptionConfirmed is written after auto-confirming a
	// SubscriptionConfirmation (only reachable once sesnotify.Verifier's
	// TopicArn pin has already passed — see internal/sesnotify/verify.go).
	// ActionSESUnsubscribeConfirmation is written when SNS reports this
	// endpoint has been unsubscribed from the events topic — an alert, not
	// an action: nothing else in the system notices bounces/complaints
	// silently stopping, so this audit row is the only record.
	ActionSESSubscriptionConfirmed   = "ses.subscription_confirmed"
	ActionSESUnsubscribeConfirmation = "ses.unsubscribe_confirmation"

	// Subscriber admin actions (PRD §5.2, §6.5; #0032's admin screen).
	// Actor is always the acting admin, unlike ActionSubscriberSignup above.
	//
	// ActionSubscriberSuppressed is written by POST
	// /admin/subscribers/{id}/suppress. It calls subscribers.Store.Unsubscribe
	// (source=admin) AND, as of #0033, subscribers.SuppressionStore.Add
	// (reason=manual) — a real `suppressions` table row, not just a status
	// change. The metadata records `no_op` (true when the target was already
	// complained, per CLAUDE.md §9 — Unsubscribe changes nothing in that
	// case) and `suppression_added` (whether the real suppressions-table
	// write happened).
	//
	// Disambiguating historical rows (#0033's carried-in review finding from
	// #0032): rows written BEFORE #0033 landed carry a
	// `suppressions_table_note` metadata key and mean "admin unsubscribe
	// only" — no suppressions-table row exists for them. Rows written AFTER
	// carry `suppression_added` instead and mean what the action name says.
	// The action name itself is deliberately NOT renamed: every such row,
	// old or new, is still "an admin suppressed this address", and renaming
	// would fragment one event type across two action strings in the log for
	// no offsetting clarity — the metadata key is what changed, and it is
	// enough to tell the two eras apart.
	ActionSubscriberSuppressed = "subscriber.suppressed"
	// ActionSubscriberComplaintCleared is written by POST
	// /admin/subscribers/{id}/clear-complaint, the sole sanctioned exit from
	// `complained` (subscribers.Store.AdminClearComplaint). Metadata notes
	// that the resulting status is `unsubscribed`, not `active` — clearing a
	// complaint does not re-establish double opt-in consent — and, as of
	// #0033, records the discarded `previous_unsubscribed_at`/
	// `previous_unsubscribe_source` values (AdminClearComplaint overwrites
	// them) plus, as of #0100, `suppression_removed` (whether the `complaint`
	// suppressions row existed and was removed — scoped to that one reason,
	// never any other), `suppression_removed_reason` ("complaint"),
	// `suppression_removed_note`/`suppression_removed_created_at` (present
	// only when a row was actually removed), and `suppressions_remaining`
	// (the surviving reasons, e.g. ["hard_bounce"], so a later reader can
	// tell the address is still blocked without re-deriving it).
	ActionSubscriberComplaintCleared = "subscriber.complaint_cleared"
	// ActionSuppressionRemoved is written by POST /admin/suppressions/remove
	// (#0100), the admin suppression-list screen's removal action — the
	// general case, as opposed to ActionSubscriberComplaintCleared's
	// specific complaint-clearing flow. TargetID is the subscriber id when a
	// subscribers row exists for the address, nil when the suppression is
	// orphaned (no subscribers row — hard deletion, #0060, or the
	// suppression predates any signup). Metadata: `email`, `reason`, `note`
	// (the admin's justification for this removal), `removed_note`,
	// `removed_created_at` (the destroyed row's own provenance — the only
	// remaining record of it once the row is gone), `suppressions_remaining`
	// (surviving reasons for this address), `subscriber_status` (or null for
	// an orphan), and `subscriber_status_unchanged: true` — reversing a
	// suppression never flips a subscriber's status; the address is still
	// not a subscriber and must return through double opt-in (§6 of
	// issues/0100.md's plan; CLAUDE.md §9's `complained` guard is what makes
	// this true for a complaint-reason removal specifically, since the
	// handler refuses to remove a `complaint` row while a subscribers row
	// still exists — see admin_suppressions.go).
	ActionSuppressionRemoved = "subscriber.suppression_removed"
	// ActionSubscriberManualAdd is written by POST /admin/subscribers, the
	// admin manual-add flow. It never creates an `active` subscriber
	// directly (PRD §5.2's notes) — it drives the same
	// newSignup/existingSignup dispatch #0026's public endpoint uses, so a
	// brand-new address lands `pending` with a confirmation email queued,
	// exactly as if the person had submitted the public form themselves.
	ActionSubscriberManualAdd = "subscriber.manual_add"
	// ActionSubscriberExported is written by GET /admin/subscribers/export
	// (#0059) after the CSV stream finishes writing to the response — success
	// or a mid-stream failure both get a row (metadata.error is present only
	// for the latter), since the fields already streamed to the client by
	// the time a failure occurs cannot be un-sent. TargetID is nil — the
	// action's target is the whole matching set, not one subscriber. Actor is
	// always the exporting admin. This is the highest-value single action an
	// admin account can take against this table (the whole list can leave the
	// system in one request, PRD §8's notes on #0059) and is exactly what an
	// attacker holding a stolen session would do, so it is audited even
	// though it is a read, not a mutation — see this file's other actions,
	// which are otherwise all mutations. Metadata records `row_count`,
	// `filter_status`, `filter_interest_id`, `filter_query` (all empty/zero
	// when unfiltered, mirroring the export's own "respects the current
	// status and interest filters" criterion).
	ActionSubscriberExported = "subscriber.exported"
	// ActionSubscriberErased is written by DELETE /admin/subscribers/{id}
	// (#0060, PRD §11's "erasure is a hard delete plus a permanent
	// suppression entry" GDPR/CCPA posture), after
	// subscribers.Store.Erase's transaction commits. TargetID is the
	// erased subscriber's former id — audit_log.target_id carries no
	// foreign key to subscribers (migrations/000005), so the row is not
	// orphaned by the delete it records; it is the one place that id can
	// still be resolved to an email after this row exists. Actor is
	// always the requesting admin, never nil (unlike
	// ActionSubscriberSignup's pre-auth convention). Metadata records
	// `email` (also independently retained on the `manual` suppressions
	// row this same transaction wrote — see
	// internal/subscribers/erase.go's package doc comment — so this is
	// not a new place the address becomes recoverable), `previous_status`,
	// `interests_removed` (the subscriber_interests row count the
	// subscribers(id) ON DELETE CASCADE just destroyed), `email_sends_
	// anonymized` (how many send-history rows had their `email` snapshot
	// overwritten rather than deleted — PRD §11/this issue's Notes:
	// preserving campaign counts), `suppression_reason` (always "manual"
	// — the reason Erase itself adds) and `suppression_preexisted` (true
	// when a `manual` suppression for this address already existed before
	// this call, e.g. a repeat erasure request). Deliberately omits
	// signup_ip/signup_user_agent/utm_*/confirm_token/manage_token — an
	// erasure audit row exists to prove the action happened and let an
	// operator reconstruct its scope, not to become a second place the
	// data just erased is still sitting in plaintext.
	ActionSubscriberErased = "subscriber.erased"
	// ActionSubscriberResendConfirmation is written by POST
	// /admin/subscribers/{id}/resend-confirmation (#0128, the pending-
	// subscriber admin screen). Actor is always the requesting admin.
	// TargetID is the pending subscriber's id. Metadata deliberately omits
	// the address (see internal/handlers/audit_email_metadata_guard_test.go)
	// — it records only `rate_limited` (true when the cooldown refused the
	// resend and nothing was sent) and `previous_confirm_sent_at`
	// (RFC3339, or omitted/empty when this is the first send), enough for
	// an operator to reconstruct what happened without a second place the
	// address sits in the audit trail.
	ActionSubscriberResendConfirmation = "subscriber.resend_confirmation"

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

	// Email campaign lifecycle (PRD §5.2/§6.6/§8; #0041's campaign CRUD
	// API). Named email_campaign.* per the deleted-constants note just
	// below — this is a distinct concept from ShortLinks' campaigns
	// (grouping short links), which never reused these strings.
	//
	// ActionEmailCampaignCreated is written by POST /admin/campaigns.
	// ActionEmailCampaignUpdated is written by PATCH /admin/campaigns/{id}
	// for a content-only edit (name/subject/preheader/body_md/audience_mode/
	// interest_ids) — PATCH never changes status; see admin_campaigns.go.
	ActionEmailCampaignCreated = "email_campaign.created"
	ActionEmailCampaignUpdated = "email_campaign.updated"
	// ActionEmailCampaignScheduled is written by POST
	// /admin/campaigns/{id}/send — the operator-initiated transition into
	// the send pipeline (draft or failed -> scheduled; PRD §8's "Requires
	// typed confirmation of the count" note belongs to #0044/#0047, once
	// the audience-count endpoint exists — see #0041's ## Fix). Metadata
	// records whether the source status was draft or failed (a retry),
	// since #0045's plan treats "resuming a failed campaign" as a distinct,
	// nameable event from a first send.
	ActionEmailCampaignScheduled = "email_campaign.scheduled"
	// ActionEmailCampaignCanceled is written by POST
	// /admin/campaigns/{id}/cancel. Metadata records the source status
	// (scheduled or sending) since cancelling a 'sending' campaign has a
	// materially different consequence — it stops further batches, it does
	// not recall anything already sent (PRD §8's note).
	ActionEmailCampaignCanceled = "email_campaign.canceled"
	// ActionEmailCampaignTestSent is written by POST
	// /admin/campaigns/{id}/test (#0046) after a real test message actually
	// reached the mailer — never on a render/validation failure that stops
	// the send before it happens. Metadata records the recipient address
	// (always the requesting admin's own — see
	// internal/handlers/admin_campaign_preview.go's doc comment on why an
	// arbitrary address is never accepted) and the provider message id.
	ActionEmailCampaignTestSent = "email_campaign.test_sent"

	// Send-worker actions (#0045, PRD §6.6). Named email_campaign.* per the
	// namespace #0041 established just above — NOT campaign.*, which was
	// retired by #0068 (see the "Deleted" comment below): a
	// campaign.send_refused row would resurrect that prefix and split the
	// campaign audit trail across two namespaces, breaking #0114's planned
	// target_type/target_id filter. All four are written with actor NULL
	// and metadata.source="send_worker" — the worker has no session and is
	// not acting on behalf of an operator at the moment it writes these,
	// matching the convention ActionSubscriberBounced and the ses.* actions
	// already use for system-originated events.
	//
	// ActionEmailCampaignSendStarted is written exactly once per campaign,
	// after materialization completes on the worker's first (start) claim —
	// never on a resume, so a campaign restarted five times still has
	// exactly one row. Metadata carries the materialized recipient count
	// (the required field), audience_mode, interest_ids, inserted, chunks,
	// and created_by.
	ActionEmailCampaignSendStarted = "email_campaign.send_started"
	// ActionEmailCampaignSendRefused is written when the worker's own copy
	// of mailing.Preflight demotes a 'scheduled' campaign back to 'draft'
	// because an unmet requirement reached the worker despite #0041's
	// advisory check (the address was cleared after scheduling, or
	// something bypassed the API). Metadata carries the failing Preflight
	// codes and messages.
	ActionEmailCampaignSendRefused = "email_campaign.send_refused"
	// ActionEmailCampaignSendCompleted is written when a campaign reaches
	// 'sent' — no queued or sending rows remain. Metadata carries the
	// final sent/failed/skipped counts. It does not carry a duration:
	// nothing consumes one (#0049 doesn't reference it), so the field was
	// never written. The duration is still derivable after the fact from
	// email_campaigns.started_at and completed_at, both persisted columns
	// — it just wasn't worth an extra read to store it redundantly here.
	ActionEmailCampaignSendCompleted = "email_campaign.send_completed"
	// ActionEmailCampaignSendFailed is written when the worker stops a
	// drain and moves a campaign to 'failed': an anomalous empty audience
	// post-materialization, physical_address (or reply-to) becoming blank
	// mid-resume, or a terminal-campaign-class SES error (account
	// suspended, sending paused, domain not verified, ...). Metadata
	// carries the reason plus how many rows sent before the stop and how
	// many remain queued.
	ActionEmailCampaignSendFailed = "email_campaign.send_failed"
	// ActionEmailCampaignPausedDeliveryHealth is written by #0124's
	// circuit breaker (PRD §6.9) when the send worker stops a drain
	// because the campaign's running bounce or complaint rate crossed its
	// configured threshold with at least send_health_min_sample messages
	// sent. Metadata carries the reason ("bounce_rate", "complaint_rate",
	// or both, comma-joined), the observed rate(s), their threshold(s),
	// and how many messages had sent so far. actor_id is nil — the worker,
	// not a person, made this call.
	ActionEmailCampaignPausedDeliveryHealth = "email_campaign.paused_delivery_health"
	// ActionEmailCampaignResumed is written by POST
	// /admin/campaigns/{id}/resume (#0124): an admin's deliberate,
	// typed-confirmation decision to let a paused_delivery_health campaign
	// continue sending. Metadata carries from_status (always
	// "paused_delivery_health") for symmetry with
	// ActionEmailCampaignScheduled's own from_status field.
	ActionEmailCampaignResumed = "email_campaign.resumed"

	// Workshop lifecycle (PRD §5.2/§6.2/§8; #0051's workshop CRUD API,
	// migrations 000020/#0050). ActionWorkshopUpdated covers any
	// content-only field change; three status transitions PATCH can drive
	// (draft/canceled -> published, published -> draft, -> canceled from any
	// other status) get their own named actions, mirroring
	// interest.deactivated/reactivated and email_campaign.scheduled/canceled
	// above — "this workshop just went live, came down, or was called off"
	// is exactly the kind of change an operator scanning the log needs to
	// spot without decoding metadata (internal/handlers/admin_workshops.go's
	// PATCH handler). Any OTHER status change (currently just
	// canceled -> draft) is recorded as ActionWorkshopUpdated with
	// old_status/new_status in metadata, same as every other field.
	ActionWorkshopCreated     = "workshop.created"
	ActionWorkshopUpdated     = "workshop.updated"
	ActionWorkshopPublished   = "workshop.published"
	ActionWorkshopUnpublished = "workshop.unpublished"
	ActionWorkshopCanceled    = "workshop.canceled"
	// ActionWorkshopDeleted is written only on an actual DELETE success —
	// never for a delete refused with ErrHasCampaigns (409), which the
	// handler surfaces without an audit row, matching
	// AdminInterestsHandler.Delete's convention for its own refused case.
	ActionWorkshopDeleted = "workshop.deleted"
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
	// TargetEmailCampaign is the target type for the email_campaign.*
	// actions above (#0041).
	TargetEmailCampaign = "email_campaign"
	// TargetSNSTopic is the target type for ActionSESSubscriptionConfirmed
	// and ActionSESUnsubscribeConfirmation (#0038) — the target is the SNS
	// topic/subscription relationship itself, not any one subscriber.
	// TargetID is left nil for both actions; the topic ARN is recorded in
	// metadata instead, since target_id is a BIGINT foreign-key-shaped
	// column and an ARN is a string.
	TargetSNSTopic = "sns_topic"
	// TargetWorkshop is the target type for the workshop.* actions above
	// (#0051).
	TargetWorkshop = "workshop"
)
