// Pure, framework-free helpers backing the Admin view: the four admin
// sub-sections (settings, users, audit log, interests). Keeping the
// deactivation `other`-requires-note validation, the audit metadata/actor
// rendering, the pagination math, and the interest-form/reorder logic here
// (rather than inline in the .svelte file) makes them unit-testable without
// a DOM — see admin.test.ts.

import type { AdminUser, AuditEntry, Interest, Subscriber } from './types';

// ── Deactivation reasons (user management) ───────────────────────────────────
// The six account.deactivated reason values from the PRD "Deactivation reasons"
// table. `note` is REQUIRED when the reason is `other`.

/** One option in the deactivation reason dropdown: stored value + label. */
export interface DeactivationReason {
  value: string;
  label: string;
}

/** The deactivation reason values with their labels, in dropdown order. */
export const DEACTIVATION_REASONS: readonly DeactivationReason[] = [
  { value: 'malware_distribution', label: 'Malware or ransomware distribution' },
  { value: 'phishing', label: 'Phishing' },
  { value: 'spam', label: 'Spam' },
  { value: 'harassment', label: 'Harassment' },
  { value: 'terms_violation', label: 'Terms of service violation' },
  { value: 'other', label: 'Other' },
] as const;

/**
 * Human-readable label for a stored deactivation reason value. An unknown value
 * is returned as-is so a server-stored value the UI doesn't know is still shown
 * rather than swallowed.
 */
export function deactivationReasonLabel(value: string): string {
  const r = DEACTIVATION_REASONS.find((d) => d.value === value);
  return r ? r.label : value;
}

/** Whether a string is one of the six known deactivation reason values. */
export function isValidDeactivationReason(value: string): boolean {
  return DEACTIVATION_REASONS.some((d) => d.value === value);
}

/**
 * Validate a deactivation form (reason + note) before submitting, mirroring the
 * server (internal/handlers/users.go): the reason must be one of the six values,
 * and the note is REQUIRED (non-empty after trimming) when reason is `other`.
 * Returns the trimmed note on success or a message to show. This is a
 * convenience gate so an invalid submit never round-trips; the server remains
 * the source of truth.
 */
export function validateDeactivation(
  reason: string,
  note: string,
): { note: string } | { error: string } {
  if (!isValidDeactivationReason(reason)) {
    return { error: 'Select a deactivation reason.' };
  }
  const trimmed = note.trim();
  if (reason === 'other' && trimmed === '') {
    return { error: 'A note is required when the reason is "Other".' };
  }
  return { note: trimmed };
}

/**
 * Whether the acting admin should even be offered the "Deactivate" control for a
 * given user: never for an admin account, and never for oneself (the backend
 * refuses both). Already-inactive users are handled separately (Reactivate).
 */
export function canDeactivate(user: AdminUser, currentUserId: number): boolean {
  return user.active && !user.is_admin && user.id !== currentUserId;
}

// ── Audit log rendering ──────────────────────────────────────────────────────

/**
 * The `target_type` value written for every email_campaign.* audit action —
 * mirrors internal/audit/actions.go's `TargetEmailCampaign` constant. #0114:
 * used to build the campaign-scoped audit filter the campaign screens
 * deep-link into (see `campaignAuditFilter` below), so a demoted campaign's
 * `email_campaign.send_refused` row — written with a NULL actor by #0045's
 * send worker, and therefore unreachable through the user_id filter alone —
 * is reachable from the campaign it belongs to.
 */
export const AUDIT_TARGET_EMAIL_CAMPAIGN = 'email_campaign';

/**
 * The audit filter to deep-link into for one campaign's history — every
 * `email_campaign.*` row for `campaignId`, most importantly a
 * `send_refused` row that has no user_id to filter by. The sole builder of
 * this shape so the campaign screens (CampaignEditor's demoted banner,
 * CampaignStats) and Admin.svelte's `listAudit` call agree on it.
 */
export function campaignAuditFilter(campaignId: number): { targetType: string; targetId: number } {
  return { targetType: AUDIT_TARGET_EMAIL_CAMPAIGN, targetId: campaignId };
}

/**
 * Render an audit entry's actor for display. A NULL actor_id means a
 * system/anonymous action (e.g. account.registration_started); otherwise show
 * the numeric user id.
 */
export function actorLabel(entry: AuditEntry): string {
  return entry.actor_id === null ? 'system' : `user #${entry.actor_id}`;
}

/**
 * Render an audit entry's target for display, e.g. "user #5", "settings", or
 * "—" when there is no target. target_id is appended only when present.
 */
export function targetLabel(entry: AuditEntry): string {
  if (entry.target_type === null) return '—';
  if (entry.target_id === null) return entry.target_type;
  return `${entry.target_type} #${entry.target_id}`;
}

/**
 * Render an audit entry's `metadata` (arbitrary JSON) as a compact, stable,
 * one-line string for the table cell, e.g. `key=registrations_enabled,
 * new_value=true`. A null/empty metadata yields an empty string; a non-object
 * (string/number) is stringified directly. Keys are sorted so the output is
 * deterministic (and test-stable).
 */
export function formatMetadata(metadata: unknown): string {
  if (metadata === null || metadata === undefined) return '';
  if (typeof metadata !== 'object') return String(metadata);
  const obj = metadata as Record<string, unknown>;
  const keys = Object.keys(obj).sort();
  if (keys.length === 0) return '';
  return keys
    .map((k) => {
      const v = obj[k];
      const rendered =
        v === null || v === undefined
          ? ''
          : typeof v === 'object'
            ? JSON.stringify(v)
            : String(v);
      return `${k}=${rendered}`;
    })
    .join(', ');
}

/**
 * Format an RFC 3339 timestamp as a human date-time for the audit table (e.g.
 * "May 25, 2026, 12:00 PM"). Falls back to the raw string when it does not parse
 * so a malformed server value is shown rather than swallowed.
 */
export function formatDateTime(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  });
}

// ── Pagination math ──────────────────────────────────────────────────────────

/** Computed pagination state for a list page, derived from total + page + per_page. */
export interface PageInfo {
  page: number;
  perPage: number;
  total: number;
  totalPages: number;
  hasPrev: boolean;
  hasNext: boolean;
  /** 1-based index of the first item on this page (0 when empty). */
  firstItem: number;
  /** 1-based index of the last item on this page (0 when empty). */
  lastItem: number;
}

/**
 * Compute pagination state from the server envelope. totalPages is at least 1
 * (an empty list is "page 1 of 1"). hasNext/hasPrev gate the controls; firstItem
 * /lastItem give the "showing N–M of T" range. page/perPage are clamped to sane
 * minimums defensively.
 */
export function pageInfo(total: number, page: number, perPage: number): PageInfo {
  const safePerPage = Math.max(1, perPage);
  const safeTotal = Math.max(0, total);
  const totalPages = Math.max(1, Math.ceil(safeTotal / safePerPage));
  const safePage = Math.min(Math.max(1, page), totalPages);
  const firstItem = safeTotal === 0 ? 0 : (safePage - 1) * safePerPage + 1;
  const lastItem = safeTotal === 0 ? 0 : Math.min(safePage * safePerPage, safeTotal);
  return {
    page: safePage,
    perPage: safePerPage,
    total: safeTotal,
    totalPages,
    hasPrev: safePage > 1,
    hasNext: safePage < totalPages,
    firstItem,
    lastItem,
  };
}

/**
 * Parse a user-entered `user_id` filter string into a positive integer, or null
 * when it is empty (no filter) or not a positive integer. The view treats a
 * non-empty-but-invalid input as "invalid", distinct from "no filter".
 */
export function parseUserIdFilter(raw: string): { userId: number | null } | { error: string } {
  const trimmed = raw.trim();
  if (trimmed === '') return { userId: null };
  if (!/^\d+$/.test(trimmed)) {
    return { error: 'Enter a numeric user id.' };
  }
  const n = Number(trimmed);
  if (!Number.isInteger(n) || n <= 0) {
    return { error: 'Enter a numeric user id.' };
  }
  return { userId: n };
}

/**
 * Parse the user-entered `target_type`/`target_id` audit filter fields.
 * Mirrors the server's validation (internal/handlers/audit.go's List): an
 * empty target_id with a non-empty target_type filters by type alone; a
 * non-empty target_id REQUIRES a non-empty target_type, since a bare
 * target_id is ambiguous across target types (subscriber #5 and
 * email_campaign #5 are unrelated rows) — see #0114 and
 * internal/audit/read.go's Filter doc comment. Both fields empty is "no
 * filter".
 */
export function parseTargetFilter(
  targetTypeRaw: string,
  targetIdRaw: string,
): { targetType: string | null; targetId: number | null } | { error: string } {
  const targetType = targetTypeRaw.trim();
  const targetIdTrimmed = targetIdRaw.trim();

  if (targetIdTrimmed === '') {
    return { targetType: targetType === '' ? null : targetType, targetId: null };
  }
  if (!/^\d+$/.test(targetIdTrimmed)) {
    return { error: 'Enter a numeric target id.' };
  }
  const n = Number(targetIdTrimmed);
  if (!Number.isInteger(n) || n <= 0) {
    return { error: 'Enter a numeric target id.' };
  }
  if (targetType === '') {
    return { error: 'Target type is required when target id is set.' };
  }
  return { targetType, targetId: n };
}

/** Whether the `registrations_enabled` setting value string is the truthy "true". */
export function registrationsEnabled(value: string | undefined): boolean {
  return value === 'true';
}

// ── Interests taxonomy (#0024) ───────────────────────────────────────────────

// Mirrors interests.ValidSlug / the interests_slug_format CHECK constraint
// (internal/interests/store.go): lowercase alphanumerics separated by single
// hyphens, no leading, trailing, or doubled hyphens. Validating here lets the
// create form reject an obviously bad slug before a round trip; the server
// remains the source of truth (and the sole enforcer of uniqueness).
const INTEREST_SLUG_PATTERN = /^[a-z0-9]+(-[a-z0-9]+)*$/;

/** Whether a string is a valid interest slug per interests.ValidSlug's rule. */
export function isValidInterestSlug(slug: string): boolean {
  return INTEREST_SLUG_PATTERN.test(slug);
}

/**
 * Validate a new-interest form (slug + name) before submitting. Trims both;
 * an invalid slug or empty name yields an `error` message, otherwise the
 * trimmed values to submit. The server independently enforces slug
 * uniqueness (409 on a duplicate) — this only catches what can be checked
 * client-side.
 */
export function validateNewInterest(
  slug: string,
  name: string,
): { slug: string; name: string } | { error: string } {
  const trimmedSlug = slug.trim();
  const trimmedName = name.trim();
  if (!isValidInterestSlug(trimmedSlug)) {
    return {
      error: 'Slug must be lowercase letters, numbers, and single hyphens (e.g. "home-automation").',
    };
  }
  if (trimmedName === '') {
    return { error: 'Name is required.' };
  }
  return { slug: trimmedSlug, name: trimmedName };
}

/** Interests ordered by sort_order then name, matching the server's ListAll ordering. */
export function sortedInterests(list: Interest[]): Interest[] {
  return [...list].sort((a, b) => a.sort_order - b.sort_order || a.name.localeCompare(b.name));
}

/** One interest's id + the sort_order to write it to, half of a reorder swap. */
export interface SortOrderTarget {
  id: number;
  sortOrder: number;
}

/** A pair of sort_order writes that swaps two adjacent interests' positions. */
export interface SortOrderSwap {
  moved: SortOrderTarget;
  other: SortOrderTarget;
}

/**
 * Compute the sort_order swap for moving the interest with `id` one position
 * `direction` within `sorted` (already in display order — pass the result of
 * sortedInterests). Returns null when `id` isn't found or is already at that
 * end of the list (nothing to swap with). The caller PATCHes both `moved`
 * and `other` with each other's current sort_order; a duplicate sort_order
 * between two rows is not itself an error (there's no uniqueness constraint
 * on it), it would just make their relative order ambiguous, which this
 * swap avoids by construction.
 */
export function reorderSwap(
  sorted: Interest[],
  id: number,
  direction: 'up' | 'down',
): SortOrderSwap | null {
  const idx = sorted.findIndex((it) => it.id === id);
  if (idx === -1) return null;
  const otherIdx = direction === 'up' ? idx - 1 : idx + 1;
  if (otherIdx < 0 || otherIdx >= sorted.length) return null;
  const moved = sorted[idx];
  const other = sorted[otherIdx];
  return {
    moved: { id: moved.id, sortOrder: other.sort_order },
    other: { id: other.id, sortOrder: moved.sort_order },
  };
}

/**
 * Whether an interest should show a "has subscribers" hint next to its
 * Delete control -- the UI surfaces the consequence (server will refuse,
 * suggest deactivating) before the admin clicks, not just after a failed
 * request.
 */
export function hasSubscribers(interest: Interest): boolean {
  return interest.subscriber_count > 0;
}

// ── Subscribers screen (#0032) ────────────────────────────────────────────────

/** The five subscribers.Status* values, in the order the header counts show them. */
export const SUBSCRIBER_STATUSES: readonly string[] = [
  'pending',
  'active',
  'unsubscribed',
  'bounced',
  'complained',
] as const;

/** Human-readable label for a subscriber status value. An unknown value is returned as-is. */
export function subscriberStatusLabel(status: string): string {
  switch (status) {
    case 'pending':
      return 'Pending';
    case 'active':
      return 'Active';
    case 'unsubscribed':
      return 'Unsubscribed';
    case 'bounced':
      return 'Bounced';
    case 'complained':
      return 'Complained';
    default:
      return status;
  }
}

/**
 * Badge CSS class for a subscriber status: green for the healthy state,
 * red for the two states that mean mail stopped reaching someone
 * unexpectedly (bounced/complained), muted for the other two (pending is
 * normal-in-progress; unsubscribed is a deliberate, expected end state).
 */
export function subscriberStatusBadgeClass(status: string): string {
  switch (status) {
    case 'active':
      return 'badge-success';
    case 'bounced':
    case 'complained':
      return 'badge-danger';
    default:
      return 'badge-muted';
  }
}

/**
 * Whether the "Clear complaint" action should be offered for a subscriber.
 * Mirrors the server's own guard (subscribers.Store.AdminClearComplaint
 * returns ErrNotComplained otherwise) — #0032's carried-in review finding
 * requires the screen to only offer this action on complained rows, not
 * let an admin discover the refusal after clicking.
 */
export function canClearComplaint(status: string): boolean {
  return status === 'complained';
}

/** The current status/interest/query filter for the subscribers screen — the
 * same triple `listSubscribers` sends and `GET /admin/subscribers/export`
 * accepts and audits (internal/handlers/admin_subscribers_export.go). */
export interface SubscribersExportFilter {
  status?: string;
  interestId?: number;
  q?: string;
}

/**
 * #0219: the href for the subscribers CSV export link, carrying whatever
 * filter is currently applied so the download matches what the admin is
 * looking at. Deliberately a plain string builder, not a fetch — the export
 * endpoint streams (`#0059`), and a `fetch(...).then(r => r.blob())` client
 * would buffer the whole response in memory first, undoing exactly the
 * property the server was built to have. The caller renders this as an
 * `<a href={...} download>`, a real browser navigation, not this function
 * performing one.
 *
 * Mirrors listSubscribers' own query-building (src/lib/api.ts) except there
 * is no page/per_page — the export is the whole filtered list, not one page
 * of it.
 */
export function subscribersExportHref(filter: SubscribersExportFilter = {}): string {
  const sp = new URLSearchParams();
  if (filter.status) sp.set('status', filter.status);
  if (filter.interestId) sp.set('interest_id', String(filter.interestId));
  if (filter.q) sp.set('q', filter.q);
  const qs = sp.toString();
  return qs ? `/admin/subscribers/export?${qs}` : '/admin/subscribers/export';
}

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** A permissive client-side email syntax check — the server remains the source of truth. */
export function isPlausibleEmail(email: string): boolean {
  return EMAIL_PATTERN.test(email.trim());
}

/**
 * Validate the manual-add form's email field before submitting. Trims and
 * checks syntax only; the server independently checks disposable domains
 * are not restricted for admin adds and enforces the real double opt-in
 * dispatch.
 */
export function validateManualAddEmail(email: string): { email: string } | { error: string } {
  const trimmed = email.trim();
  if (!isPlausibleEmail(trimmed)) {
    return { error: 'Enter a valid email address.' };
  }
  return { email: trimmed };
}

/**
 * Validate the suppress-note form field: required, non-empty after
 * trimming, matching the server's own 400 on a blank note.
 */
export function validateSuppressNote(note: string): { note: string } | { error: string } {
  const trimmed = note.trim();
  if (trimmed === '') {
    return { error: 'A note is required.' };
  }
  return { note: trimmed };
}

/**
 * Render a subscriber's consent-evidence "signup" line for the detail view:
 * the signup timestamp, plus IP/user agent when present (both are nullable —
 * a manually-added subscriber, #0032's own Create endpoint, has neither).
 */
export function signupEvidenceSummary(sub: Subscriber): string {
  const when = formatDateTime(sub.created_at);
  if (!sub.signup_ip && !sub.signup_user_agent) {
    return `${when} — no browser signup evidence recorded (added by an admin)`;
  }
  const parts = [when];
  if (sub.signup_ip) parts.push(`from ${sub.signup_ip}`);
  if (sub.signup_user_agent) parts.push(`(${sub.signup_user_agent})`);
  return parts.join(' ');
}

/**
 * Render the #0039 soft-bounce summary line for the subscriber detail view.
 * `soft_bounce_count`/`threshold`/`window_days` are populated by the detail
 * endpoint only (see the Subscriber type's doc comment) — a nil count means
 * either the backend hasn't computed it (dev mode / a nil dependency) rather
 * than "zero bounces", so that case gets its own message instead of "0 of
 * undefined".
 *
 * Worded "soft bounce", not "transient bounce": #0109 widened the backend
 * count to include Undetermined bounces (excluding the sender-fault
 * Transient subtypes), so "transient" would misdescribe what is actually
 * being counted.
 */
export function softBounceSummary(sub: Subscriber): string {
  if (sub.soft_bounce_count == null || sub.soft_bounce_threshold == null || sub.soft_bounce_window_days == null) {
    return 'Not available.';
  }
  const { soft_bounce_count: count, soft_bounce_threshold: threshold, soft_bounce_window_days: days } = sub;
  const suffix = count === 1 ? '' : 's';
  const base = `${count} soft bounce${suffix} in the last ${days} days (threshold: ${threshold}).`;
  if (count >= threshold) {
    return `${base} This address should already be suppressed.`;
  }
  return base;
}

// ── Subscriber activity log (#0126, PRD §6.11) ───────────────────────────────

/**
 * Human-readable label for a subscriber_events action value. Matches
 * internal/subscribers.Action's closed set. An unknown value is returned
 * as-is (the server's own closed-set validation is what actually prevents
 * an unrecognized action from ever reaching this table — see
 * internal/subscribers/events.go — so "as-is" here is a display fallback,
 * not a validation gap).
 */
export function subscriberEventActionLabel(action: string): string {
  switch (action) {
    case 'signup_requested':
      return 'Signup requested';
    case 'confirmation_sent':
      return 'Confirmation email sent';
    case 'confirmed':
      return 'Confirmed';
    case 'confirmation_expired':
      return 'Confirmation link expired';
    case 'welcome_sent':
      return 'Welcome email sent';
    case 'interests_changed':
      return 'Interests changed';
    case 'unsubscribed':
      return 'Unsubscribed';
    case 'resubscribed':
      return 'Resubscribed';
    case 'imported':
      return 'Imported';
    case 'invite_sent':
      return 'Import invite sent';
    case 'invite_accepted':
      return 'Import invite accepted';
    case 'invite_expired':
      return 'Import invite expired';
    case 'import_revoked':
      return 'Import revoked';
    case 'campaign_sent':
      return 'Campaign email sent';
    case 'bounced_soft':
      return 'Soft bounce (repeated)';
    case 'bounced_hard':
      return 'Hard bounce';
    case 'complained':
      return 'Spam complaint';
    case 'delivered':
      return 'Delivered';
    case 'suppressed':
      return 'Suppressed';
    case 'unsuppressed':
      return 'Suppression removed';
    case 'admin_edited':
      return 'Edited by staff';
    case 'erased':
      return 'Erased (GDPR/CCPA)';
    default:
      return action;
  }
}

// ── Admin suppression-list screen (#0100) ────────────────────────────────────

/** The four subscribers.SuppressionReason* values, matching the server's CHECK constraint. */
export const SUPPRESSION_REASONS: readonly string[] = [
  'hard_bounce',
  'complaint',
  'manual',
  'repeated_soft_bounce',
] as const;

/** Human-readable label for a suppression reason value. An unknown value is returned as-is. */
export function suppressionReasonLabel(reason: string): string {
  switch (reason) {
    case 'hard_bounce':
      return 'Hard bounce';
    case 'complaint':
      return 'Complaint';
    case 'manual':
      return 'Manual';
    case 'repeated_soft_bounce':
      return 'Repeated soft bounce';
    default:
      return reason;
  }
}

/**
 * Validate the suppression-removal form's note field: required, non-empty
 * after trimming, matching the server's own 400 on a blank note. Mirrors
 * validateSuppressNote above (same rule, different form) — kept as a
 * separate function rather than a shared alias so each screen's error
 * copy can diverge if the two forms' needs ever do.
 */
export function validateSuppressionNote(note: string): { note: string } | { error: string } {
  const trimmed = note.trim();
  if (trimmed === '') {
    return { error: 'A note is required.' };
  }
  return { note: trimmed };
}

/**
 * Whether removing a suppression row should be blocked client-side before
 * the request is even sent, and if so, why. Mirrors the server's one
 * refusal (internal/handlers/admin_suppressions.go's Remove): a
 * reason="complaint" row is removable directly ONLY when no subscribers row
 * exists for the address (an orphan, #0060) — while a subscriber record
 * exists, Subscribers → Clear complaint is the sole sanctioned, audited path
 * (CLAUDE.md §9). Pre-disabling the button here, the same pattern the
 * Interests section's disabled Delete already uses, means the admin never
 * discovers this as a failed request.
 */
export function suppressionRemovalBlocked(reason: string, hasSubscriber: boolean): string | null {
  if (reason === 'complaint' && hasSubscriber) {
    return 'Use Subscribers → Clear complaint to remove a complaint suppression while a subscriber record exists.';
  }
  return null;
}

// ── Subscriber import (#0125, PRD §6.10) ─────────────────────────────────────
// The CSV import wizard's pure logic: form validation, CSV header parsing
// for the column-mapping dropdowns, and display labels. The upload/preview/
// commit calls themselves live in api.ts (importPreview/importCommit/
// revokeImport) since they are I/O, not logic.

/**
 * importMaxFileBytes/importMaxDataRows mirror
 * internal/handlers/admin_subscribers_import.go's importMaxFileBytes/
 * importMaxDataRows EXACTLY — there is no runtime way to derive one from
 * the other since client and server are different processes, so both
 * copies must be kept in sync by hand. Used to reject an oversized upload
 * before it ever leaves the browser, and to state the bound in the wizard's
 * own copy (#0125's acceptance criteria: "the bound is stated in the UI").
 */
export const IMPORT_MAX_FILE_BYTES = 5 * 1024 * 1024; // 5 MiB
export const IMPORT_MAX_DATA_ROWS = 5000;

/** One option in the import-source dropdown: stored value + label, matching subscriber_imports_source_check. */
export interface ImportSourceOption {
  value: string;
  label: string;
}

/** The five subscriber_imports.source values, in dropdown order. */
export const IMPORT_SOURCES: readonly ImportSourceOption[] = [
  { value: 'manual_csv', label: 'Manual CSV (spreadsheet, sign-in sheet)' },
  { value: 'eventbrite', label: 'Eventbrite' },
  { value: 'meetup', label: 'Meetup' },
  { value: 'luma', label: 'Luma' },
  { value: 'other', label: 'Other' },
] as const;

/** Human-readable label for a stored import source value. */
export function importSourceLabel(value: string): string {
  const s = IMPORT_SOURCES.find((s) => s.value === value);
  return s ? s.label : value;
}

/**
 * One option in the consent-mode choice: stored value + label + what it
 * does, plus whether this wizard can offer it. #0125 implements
 * prior_consent only; invite (#0129) is listed so the wizard's copy can name
 * it as "coming soon" rather than pretending only one mode exists, but
 * `available: false` is what stops the UI from silently downgrading a
 * request for it (#0125's acceptance criteria: "the UI must not offer
 * invite until #0129 lands, and must never silently downgrade an invite
 * request to prior_consent").
 */
export interface ConsentModeOption {
  value: string;
  label: string;
  description: string;
  available: boolean;
}

export const CONSENT_MODES: readonly ConsentModeOption[] = [
  {
    value: 'prior_consent',
    label: 'Prior consent',
    description:
      'Asserts consent was already obtained at the source. Lands every new address active immediately and sends nothing — no confirmation, no welcome email.',
    available: true,
  },
  {
    value: 'invite',
    label: 'Invite to confirm',
    description:
      'Asks: sends one invitation naming where the address came from, and only activates it if they confirm. Coming soon — not yet available in this wizard.',
    available: false,
  },
] as const;

/**
 * canspamWarning is the CAN-SPAM/GDPR notice #0125's acceptance criteria
 * requires IN THE INTERFACE, not only in documentation: "importing without
 * prior consent violates CAN-SPAM and burns the sending domain." The wizard
 * renders this text directly rather than linking out to it.
 */
export const IMPORT_CANSPAM_WARNING =
  'Importing addresses without genuine prior consent is unlawful under CAN-SPAM and GDPR, and burns this domain’s sending reputation. Only import an audience that clearly opted in at the source, and say how in the note below.';

/**
 * parseCSVHeaderRow splits a CSV file's first line into column names for
 * the mapping dropdowns. Deliberately NOT a full CSV parser (no quoted-field
 * handling, no embedded-comma/newline support) — it exists only to label
 * columns for a human to pick from, and the actual data rows are parsed
 * server-side by encoding/csv, a real parser. A header containing a quoted
 * comma will mislabel a dropdown option; it will never misparse a DATA row,
 * since this function is never used for those.
 */
export function parseCSVHeaderRow(firstLine: string): string[] {
  return firstLine.split(',').map((h) => h.trim().replace(/^"|"$/g, ''));
}

/** The import wizard's form fields, before file-reading. */
export interface ImportFormFields {
  source: string;
  sourceDetail: string;
  consentMode: string;
  consentNote: string;
  collectedAt: string; // YYYY-MM-DD
  emailColumn: number | null;
  interestColumn: number | null;
  fileBytes: number; // File.size, for the bound check without re-reading the file
}

/**
 * validateImportForm gates the Preview button client-side, mirroring the
 * server's own validation (internal/subscribers.ImportStore.Commit) so an
 * invalid submit never round-trips. Returns the trimmed/normalized fields on
 * success or a message to show.
 */
export function validateImportForm(
  f: ImportFormFields,
): { source: string; sourceDetail: string; consentMode: string; consentNote: string; collectedAt: string; emailColumn: number; interestColumn: number | null } | { error: string } {
  if (!IMPORT_SOURCES.some((s) => s.value === f.source)) {
    return { error: 'Choose where this list came from.' };
  }
  const mode = CONSENT_MODES.find((m) => m.value === f.consentMode);
  if (!mode) {
    return { error: 'Choose a consent mode.' };
  }
  if (!mode.available) {
    return { error: `${mode.label} is not available yet.` };
  }
  const consentNote = f.consentNote.trim();
  if (consentNote === '') {
    return { error: 'Explain how consent was obtained — this is required and is recorded on the batch.' };
  }
  if (!/^\d{4}-\d{2}-\d{2}$/.test(f.collectedAt)) {
    return { error: 'Enter the date the source collected these addresses.' };
  }
  if (f.emailColumn === null) {
    return { error: 'Choose which column holds the email address.' };
  }
  if (f.fileBytes > IMPORT_MAX_FILE_BYTES) {
    return { error: `File is too large — the limit is ${Math.floor(IMPORT_MAX_FILE_BYTES / (1024 * 1024))} MB.` };
  }
  return {
    source: f.source,
    sourceDetail: f.sourceDetail.trim(),
    consentMode: f.consentMode,
    consentNote,
    collectedAt: f.collectedAt,
    emailColumn: f.emailColumn,
    interestColumn: f.interestColumn,
  };
}

/**
 * importRowCountExceeded reports whether a parsed CSV's data-row count (the
 * file minus its header line) exceeds IMPORT_MAX_DATA_ROWS, for a
 * client-side warning before the upload is even attempted.
 */
export function importRowCountExceeded(dataRowCount: number): boolean {
  return dataRowCount > IMPORT_MAX_DATA_ROWS;
}
