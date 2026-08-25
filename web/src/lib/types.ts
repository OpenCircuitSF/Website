// TypeScript interfaces mirroring the Go backend's JSON shapes. Field names are
// snake_case to match the API exactly (see internal/handlers/*.go). These are the
// shared contract the views build on.

/** GET /api/me — the current user profile used to gate the admin view. */
export interface User {
  id: number;
  email: string;
  is_admin: boolean;
}

/**
 * A registered passkey (GET /account/credentials item). Matches
 * internal/handlers/credentials.go `credentialView`.
 */
export interface Credential {
  id: number;
  device_name: string;
  aaguid: string;
  device_hint: string;
  sign_count: number;
  created_at: string;
  last_used_at: string | null;
}

/**
 * One audit-log row (GET /admin/audit item). Matches
 * internal/handlers/audit.go `auditRecordView`.
 */
export interface AuditEntry {
  id: number;
  actor_id: number | null;
  user_id: number | null;
  action: string;
  target_type: string | null;
  target_id: number | null;
  metadata: unknown;
  ip_address: string | null;
  created_at: string;
}

/** An admin-visible user account (GET /admin/users item). */
export interface AdminUser {
  id: number;
  email: string;
  is_admin: boolean;
  active: boolean;
  created_at: string;
  last_login_at?: string;
}

/** A runtime server setting (GET /admin/settings item). */
export interface Setting {
  key: string;
  value: string;
  updated_at?: string;
}

/**
 * A workshop interest-taxonomy row (GET /admin/interests item). Matches
 * internal/handlers/interests.go `interestView`. subscriber_count is the
 * number the admin screen exists to show: how many subscribers currently
 * have this interest selected, whether or not the interest is still active
 * (a subscriber's history with a deactivated interest still counts). slug is
 * immutable once created — the PATCH endpoint rejects a body carrying it.
 */
export interface Interest {
  id: number;
  slug: string;
  name: string;
  description?: string;
  sort_order: number;
  active: boolean;
  subscriber_count: number;
  created_at: string;
}

/** A subscriber's selected interest, as embedded in a Subscriber detail (GET /admin/subscribers/{id} item). */
export interface SubscriberInterestRef {
  id: number;
  slug: string;
  name: string;
}

/**
 * A mailing-list subscriber (GET /admin/subscribers[/{id}] item). Matches
 * internal/handlers/admin_subscribers.go `subscriberView` (#0032) — the field
 * set is deliberately limited to what #0075's published privacy policy
 * discloses subscribers their data includes, plus the status/unsubscribe
 * record the same policy already describes. `interests` is only populated by
 * the detail endpoint (GET .../{id}); the list endpoint omits it to avoid an
 * N+1 query per row. `email_events` is always present but empty until #0038
 * (SES bounce/complaint ingestion) lands.
 *
 * `soft_bounce_count`/`soft_bounce_threshold`/`soft_bounce_window_days`
 * (#0039, widened by #0109) are likewise populated by the detail endpoint
 * only: the count of Transient- and Undetermined-bounce `email_events` rows
 * within the currently configured window, excluding the sender-fault
 * Transient subtypes (MessageTooLarge, ContentRejected, AttachmentRejected),
 * plus the threshold/window themselves so the screen can render "N of
 * THRESHOLD in the last WINDOW days" rather than a bare number. All three
 * are present together or absent together.
 */
export interface Subscriber {
  id: number;
  email: string;
  status: string; // pending | active | unsubscribed | bounced | complained
  signup_ip?: string;
  signup_user_agent?: string;
  utm_source?: string;
  utm_medium?: string;
  utm_campaign?: string;
  created_at: string; // signup timestamp
  confirmed_at?: string;
  unsubscribed_at?: string;
  unsubscribe_source?: string;
  interests?: SubscriberInterestRef[];
  email_events: unknown[];
  soft_bounce_count?: number;
  soft_bounce_threshold?: number;
  soft_bounce_window_days?: number;
}

/** The {pending, active, unsubscribed, bounced, complained} header block atop the subscribers list. */
export interface SubscriberStatusCounts {
  pending: number;
  active: number;
  unsubscribed: number;
  bounced: number;
  complained: number;
}

/** Envelope returned by GET /admin/subscribers. */
export interface SubscribersPage {
  subscribers: Subscriber[];
  total: number;
  page: number;
  per_page: number;
  counts: SubscriberStatusCounts;
}

/**
 * One row of the suppression list (GET /admin/suppressions item). Matches
 * internal/handlers/admin_suppressions.go `suppressionView` (#0100).
 * `subscriber_status` is null when no subscribers row exists for this
 * address (an orphan — hard deletion, #0060, or a suppression added before
 * any signup) — the field the two-layer picture ("blocked at the
 * suppression list, the subscriber row, or both?") is built on, and what
 * lets the client pre-disable a `complaint` removal before the server
 * 409s.
 */
export interface Suppression {
  email: string;
  reason: string; // hard_bounce | complaint | manual | repeated_soft_bounce
  note?: string;
  created_at: string;
  subscriber_status: string | null;
}

// ── Public mailing-list journey (#0029-#0031) ────────────────────────────────

/**
 * One interest as shown to an anonymous visitor (GET /api/interests item, and
 * the active_interests list embedded in the confirm/preferences responses
 * below). Matches internal/handlers/public_interests.go's publicInterestView
 * -- deliberately narrower than Interest above: no id, active, or
 * subscriber_count, which are admin-only fields from GET /admin/interests.
 */
export interface PublicInterest {
  slug: string;
  name: string;
  description?: string;
  sort_order: number;
}

/** POST /api/subscribe/confirm success body (#0030). manage_token lets the SPA
 * show the preference center inline, already authenticated, without a second
 * confirmation email; email is unmasked here (unlike GET /api/preferences)
 * because reaching this response already proved fresh control of the
 * mailbox. Matches internal/handlers/confirm.go's confirmResponse. */
export interface ConfirmResponse {
  message: string;
  manage_token: string;
  email: string;
  interests: string[];
  active_interests: PublicInterest[];
}

// ── Admin: campaign compose (#0041/#0044/#0045/#0046/#0047) ─────────────────

/**
 * One email campaign (GET/POST/PATCH /admin/campaigns[/{id}] item). Matches
 * internal/handlers/admin_campaigns.go `campaignView`. `scheduled_at`,
 * `started_at`, `completed_at`, and `test_sent_at` are all `json:",omitempty"`
 * on the Go side, so a NULL column is an ABSENT key in the response, never a
 * present key holding `null` — see lib/campaigns.ts's
 * `wasDemotedAfterScheduling` for why this distinction is load-bearing
 * (#0041's carried-in review finding, amended by #0046's third review once
 * `test_sent_at` itself shipped).
 */
export interface Campaign {
  id: number;
  name: string;
  subject: string;
  preheader?: string;
  body_md: string;
  status: string; // draft | scheduled | sending | sent | canceled | failed
  audience_mode: string; // all | any_of | all_of | none_selected
  workshop_id?: number;
  interest_ids: number[];
  scheduled_at?: string;
  started_at?: string;
  completed_at?: string;
  test_sent_at?: string;
  created_by?: number;
  created_at: string;
  updated_at: string;
}

/**
 * One unmet pre-send requirement, as returned both by GET
 * .../campaigns/{id}/preflight's `unmet` array and by the `{"unmet":[...]}`
 * 409 body POST .../send answers when its advisory check fails. Matches
 * internal/handlers/admin_campaigns.go `campaignPreflightFailure`. `message`
 * is rendered verbatim by lib/preflight.ts's renderer — see that module's
 * doc comment for why there is no client-side code→message table.
 */
export interface UnmetRequirement {
  code: string;
  message: string;
}

/**
 * GET /admin/campaigns/{id}/preflight response. Matches
 * internal/handlers/admin_campaign_preflight.go `campaignPreflightResponse`.
 * `summary` is the server's read of the STORED row — what
 * CampaignSendDialog.svelte confirms against, never the editor's unsaved
 * buffer.
 */
export interface PreflightResponse {
  ok: boolean;
  unmet: UnmetRequirement[];
  summary: {
    subject: string;
    from: string;
    recipients: number;
  };
}

/**
 * GET /admin/campaigns/{id}/audience response. Matches
 * internal/handlers/admin_campaign_audience.go `campaignAudienceView`.
 * `sample` is capped at 20 server-side (campaignAudienceSampleLimit) — never
 * client-controlled; lib/audience.ts's `sampleCaption` derives its wording
 * from `sample.length`, not a repeated literal 20.
 */
export interface AudiencePreviewResponse {
  mode: string;
  interest_ids: number[];
  count: number;
  sample: string[];
  warnings: string[];
}

/**
 * POST /admin/campaigns/{id}/preview and POST /admin/campaigns/{id}/test both
 * respond with this shape once a send/render succeeds (Test returns the
 * updated Campaign instead — see api.ts's testSendCampaign). Matches
 * internal/handlers/admin_campaign_preview.go `campaignPreviewResponse`.
 */
export interface CampaignPreviewResponse {
  subject: string;
  html: string;
  text: string;
}

/**
 * GET /admin/campaigns/{id}/stats response (#0049, PRD §11). Matches
 * internal/handlers/admin_campaign_stats.go `campaignStatsResponse`.
 *
 * `counts` is the raw email_sends.status breakdown, all seven response
 * fields present including `bounced`/`complained` — but migration 000017
 * (as amended by #0131, widened by 000018 to add `sending`) no longer
 * admits either value into the column, so those two always read zero.
 * `reconciled` is the SEPARATE, actually-meaningful bounce/complaint count,
 * joined server-side from email_events by ses_message_id — see
 * internal/mailing/campaign_stats.go's package doc comment for why
 * `counts.bounced`/`counts.complained` read zero (nothing in this codebase
 * ever writes those two email_sends.status values, and the CHECK now
 * forbids it outright) and lib/campaignStats.ts's `buildStatBuckets` for
 * why the presentation layer substitutes `reconciled` in their place.
 */
export interface CampaignStatsResponse {
  campaign_id: number;
  status: string;
  counts: {
    queued: number;
    sending: number;
    sent: number;
    failed: number;
    bounced: number;
    complained: number;
    skipped: number;
  };
  reconciled: {
    bounced: number;
    complained: number;
  };
  failed_sends: CampaignFailedSend[];
}

/** One failed email_sends row, as listed by CampaignStatsResponse.failed_sends. */
export interface CampaignFailedSend {
  id: number;
  email: string;
  error?: string;
  attempts: number;
}

// ── Public workshops (#0051/#0053/#0054) ─────────────────────────────────────

/**
 * One workshop as shown to an anonymous visitor (GET /api/workshops and GET
 * /api/workshops/{slug} item). Matches
 * internal/handlers/public_workshops.go's publicWorkshopView -- deliberately
 * narrower than an admin workshop view: no numeric id, the public wire
 * contract identifies a workshop by slug (same convention PublicInterest
 * follows for interests). `status` is included even though the index
 * (GET /api/workshops) only ever contains 'published' rows -- the detail
 * route also serves 'canceled', and this type is shared by both callers.
 */
export interface PublicWorkshop {
  slug: string;
  title: string;
  summary?: string;
  body_md?: string;
  /**
   * body_md rendered server-side through goldmark (#0136 --
   * internal/handlers/admin_workshop_preview.go's renderWorkshopBodyHTML,
   * the same function POST /admin/workshops/{id}/preview renders through).
   * WorkshopDetail.svelte renders this directly with `{@html}` -- it never
   * runs body_md through a client-side Markdown renderer. Absent when
   * body_md is unset or renders to nothing.
   */
  body_html?: string;
  starts_at?: string;
  ends_at?: string;
  location_name?: string;
  location_address?: string;
  location_note?: string;
  capacity?: number;
  signup_url?: string;
  cover_image?: string;
  status: string; // published | canceled (draft never reaches a public route)
  interests: PublicInterest[];
}

/**
 * GET /api/workshops response (#0051, #0053). Matches
 * internal/handlers/public_workshops.go's workshopsListResponsePublic:
 * published workshops split into upcoming (chronological) and past (reverse
 * chronological) relative to the server's clock.
 */
export interface WorkshopsListResponse {
  upcoming: PublicWorkshop[];
  past: PublicWorkshop[];
}

// ── Admin workshops (#0051 API, #0052 admin UI) ──────────────────────────────

/**
 * One workshop as the admin CRUD API sees it (GET/POST/PATCH
 * /admin/workshops[/{id}]) -- wider than PublicWorkshop above: carries the
 * numeric id, every status (draft/published/canceled, not just
 * published/canceled), and interest_ids as raw ids rather than embedded
 * PublicInterest objects (the admin screen resolves those against the
 * interests list it already loads for the taxonomy section -- see
 * lib/admin.ts). Matches
 * internal/handlers/admin_workshops.go's workshopView.
 */
export interface AdminWorkshop {
  id: number;
  slug: string;
  title: string;
  summary?: string;
  body_md?: string;
  starts_at?: string;
  ends_at?: string;
  location_name?: string;
  location_address?: string;
  location_note?: string;
  capacity?: number;
  signup_url?: string;
  cover_image?: string;
  status: string; // draft | published | canceled
  published_at?: string;
  interest_ids: number[];
  created_at: string;
  updated_at: string;
}

/** GET /admin/workshops response. Matches admin_workshops.go's workshopsListResponse. */
export interface AdminWorkshopsListResponse {
  workshops: AdminWorkshop[];
}

/**
 * POST /admin/workshops/{id}/preview response (#0136). Matches
 * internal/handlers/admin_workshop_preview.go's workshopPreviewResponse --
 * deliberately narrower than CampaignPreviewResponse above: a workshop page
 * has no subject line and no plain-text alternative to render, only the one
 * HTML fragment WorkshopEditor.svelte's preview pane shows.
 */
export interface WorkshopPreviewResponse {
  html: string;
}

/** GET/PATCH /api/preferences body (#0031). email is masked
 * ("b•••••n@gmail.com") except when the SPA already holds the unmasked
 * address from a fresh ConfirmResponse (see PreferenceCenter.svelte).
 * unsubscribed is true only on a successful "Unsubscribe from everything"
 * PATCH; no_op is true only when that same PATCH found the row already
 * complained (Store.Unsubscribe's documented no-op case) — unsubscribed is
 * false in that case, and PreferenceCenter.svelte must not treat it as a
 * success (#0031 review finding 2). Matches
 * internal/handlers/preferences.go's preferencesResponse. no_op is always
 * present (#0093: the server used to tag it `omitempty`, the one field this
 * type had that AdminSubscribers' SubscriberActionResult-and-no_op shape and
 * UnsubscribeResult's no_op did not — both of those were always required).
 */
export interface PreferencesResponse {
  message?: string;
  email: string;
  status: string;
  interests: string[];
  active_interests: PublicInterest[];
  unsubscribed: boolean;
  no_op: boolean;
}

/**
 * GET /admin/overview (#0061): the /admin landing screen's single data call.
 * Matches internal/handlers/admin_dashboard.go's dashboardOverviewResponse
 * exactly — see that file's package doc comment for why every figure here
 * is one aggregate query, not a loop.
 */
export interface DashboardOverview {
  subscribers: DashboardSubscribers;
  interests: DashboardInterestRow[];
  recent_campaigns: DashboardCampaignRow[];
  /** Absent (undefined) when no campaign is currently mid-send. */
  sending_campaign?: DashboardCampaignRow;
  /** Absent (undefined) when the server has no outbound_queue backing (STORAGE=json). */
  outbound_queue?: DashboardOutboundQueue;
  warnings: DashboardWarnings;
}

/**
 * The transactional-mail queue depth (#0126, PRD §6.11) — confirmation,
 * registration, recovery, and every other outbound_queue kind combined.
 * oldest_queued_age_seconds is 0 when queued is 0.
 */
export interface DashboardOutboundQueue {
  queued: number;
  sending: number;
  sent: number;
  abandoned: number;
  oldest_queued_age_seconds: number;
}

export interface DashboardSubscriberCounts {
  pending: number;
  active: number;
  unsubscribed: number;
  bounced: number;
  complained: number;
}

export interface DashboardGrowth {
  confirmed_30d: number;
  unsubscribed_30d: number;
  net_30d: number;
}

export interface DashboardSubscribers {
  counts: DashboardSubscriberCounts;
  growth_30d: DashboardGrowth;
}

export interface DashboardInterestRow {
  id: number;
  slug: string;
  name: string;
  subscriber_count: number;
}

export interface DashboardCampaignRow {
  id: number;
  name: string;
  status: string; // draft | scheduled | sending | sent | canceled | failed
  scheduled_at?: string;
  started_at?: string;
  completed_at?: string;
}

/**
 * The "what needs attention" row. complaint_rate_pct is absent when the
 * account-wide sample is below the server's minimum-sample floor — never
 * fabricated as 0 (CLAUDE.md §5: "say so in the UI rather than presenting
 * it as exact"). inbound_mail_unavailable is always true today: PRD §6.5
 * path 3 (#0058) is not built, so there is no count this figure could ever
 * report — see admin_dashboard.go's package doc comment.
 *
 * complaint_rate_review (amber, #0227) and complaint_rate_high (red) are
 * INDEPENDENT booleans, not tiers of one escalating scale: a rate above the
 * red threshold sets both true, because both consequences — AWS may
 * re-sandbox the account (review) and Gmail/Yahoo will filter mail (high)
 * — apply at once. lib/dashboard.ts's buildWarnings renders each as its
 * own distinct message.
 */
export interface DashboardWarnings {
  complaint_rate_review: boolean;
  complaint_rate_high: boolean;
  complaint_rate_pct?: number;
  complaint_sample_size: number;
  physical_address_unset: boolean;
  ses_sandbox_active: boolean;
  inbound_mail_unavailable: boolean;
  /** At least one outbound_queue row has reached the terminal 'abandoned' state (#0126). */
  outbound_queue_abandoned: boolean;
}
