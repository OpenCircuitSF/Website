// Pure, framework-free helpers backing the campaign compose screen (#0047):
// status/mode vocabulary, the CRUD-save validation gate, subject/preheader
// length guidance, the post-scheduling demotion detector, and cancel copy.
//
// #0094 records that no test in this repository imports a .svelte file —
// there is no jsdom, no component harness, and anything expressed in a
// component is unverifiable. That is what makes this module (and its
// siblings preflight.ts/audience.ts/sendConfirm.ts) the single most
// load-bearing part of #0047's plan: every {#if}, disabled/aria-disabled,
// and computed string in Campaigns.svelte/CampaignEditor.svelte/
// CampaignSendDialog.svelte must be either a call into one of these four
// modules, a plain value one of them returned, or a value taken straight off
// a server response — never a comparison, `.trim()`, `.length >`, status
// literal, or `||` fallback chain written inline in a template.
//
// See #0047's plan §2 for the full module split and #0041/#0045's Fix
// sections for the exact server-side rules mirrored here (canEditCampaign
// etc. are UI *offer* gates, not enforcement — the server independently
// refuses every illegal transition with a 409 regardless of what this module
// says).

import type { Campaign } from './types';
import { formatDateTime } from './admin';

// ── Campaign status vocabulary (migration 000017) ────────────────────────────

/**
 * The seven mailing.CampaignStatus* values, in no particular order.
 * `paused_delivery_health` (#0124, PRD §6.9) is the circuit breaker's own
 * status: a campaign the send worker stopped mid-drain because its running
 * bounce or complaint rate crossed a configured threshold, distinct from
 * `failed` — a paused campaign is expected to be resumed, not retried from
 * scratch.
 */
export const CAMPAIGN_STATUSES: readonly string[] = [
  'draft',
  'scheduled',
  'sending',
  'paused_delivery_health',
  'sent',
  'canceled',
  'failed',
] as const;

/** Human-readable label for a campaign status value. An unknown value is returned as-is. */
export function campaignStatusLabel(status: string): string {
  switch (status) {
    case 'draft':
      return 'Draft';
    case 'scheduled':
      return 'Scheduled';
    case 'sending':
      return 'Sending';
    case 'paused_delivery_health':
      return 'Paused — delivery health';
    case 'sent':
      return 'Sent';
    case 'canceled':
      return 'Canceled';
    case 'failed':
      return 'Failed';
    default:
      return status;
  }
}

/**
 * Badge CSS class for a campaign status: green for a completed send, red for
 * a failed OR breaker-paused one (both demand operator attention — no
 * separate amber badge variant exists in app.css, and reusing the danger
 * color is the correct signal here, not a placeholder), muted for every
 * other in-progress or terminal-but-uneventful state
 * (draft/scheduled/sending/canceled) — mirrors subscriberStatusBadgeClass's
 * shape in lib/admin.ts.
 */
export function campaignStatusBadgeClass(status: string): string {
  switch (status) {
    case 'sent':
      return 'badge-success';
    case 'failed':
    case 'paused_delivery_health':
      return 'badge-danger';
    default:
      return 'badge-muted';
  }
}

// ── Audience modes (mailing.Audience* in internal/mailing/campaigns.go) ─────

/** One audience mode: the stored value, a label, and one-line help text. */
export interface AudienceModeOption {
  value: string;
  label: string;
  help: string;
}

/** The four mailing.Audience* values, in the order the editor's radio group shows them. */
export const AUDIENCE_MODES: readonly AudienceModeOption[] = [
  { value: 'all', label: 'Everyone', help: 'Every active subscriber, regardless of interests.' },
  { value: 'any_of', label: 'Any of these interests', help: 'Subscribers with at least one selected interest.' },
  { value: 'all_of', label: 'All of these interests', help: 'Subscribers with every selected interest.' },
  {
    value: 'none_selected',
    label: 'No interests selected',
    help: 'Subscribers who have not selected any interest yet.',
  },
] as const;

/**
 * Whether the interest checkbox fieldset should be enabled for a given
 * audience mode — true only for any_of/all_of, the two modes whose targeting
 * actually reads interest_ids. Mirrors #0044's own warn-rather-than-reject
 * treatment of interests attached to all/none_selected: the fieldset is
 * DISABLED (not hidden) in those two modes so a stored, stale interest
 * selection stays visible while the server's own warning (rendered verbatim
 * elsewhere, never restated here) explains what those rows mean.
 */
export function interestsApplyToMode(mode: string): boolean {
  return mode === 'any_of' || mode === 'all_of';
}

// ── Transition-offer gates (UI affordance only — the server enforces) ───────

/** Whether the editor should be offered for a campaign at this status (#0041's rule). */
export function canEditCampaign(status: string): boolean {
  return status === 'draft' || status === 'scheduled';
}

/** Whether the Send control should be shown for a campaign at this status. */
export function canSendCampaign(status: string): boolean {
  return status === 'draft';
}

/**
 * Whether the Cancel control should be shown for a campaign at this status.
 * `paused_delivery_health` (#0124) added: an operator who has diagnosed the
 * underlying problem may cancel a paused campaign directly rather than
 * being forced through a resume-then-cancel round trip — mirrors
 * mailing.CampaignStore.Cancel's own accepted source-status set.
 */
export function canCancelCampaign(status: string): boolean {
  return status === 'scheduled' || status === 'sending' || status === 'paused_delivery_health';
}

/**
 * Whether the Resume control should be shown — #0124's circuit-breaker
 * recovery path, the only way to un-trip it. Mirrors
 * mailing.CampaignStore.Resume's own single accepted source status.
 */
export function canResumeCampaign(status: string): boolean {
  return status === 'paused_delivery_health';
}

// ── CRUD-save validation (deliberately NOT the Preflight send gate) ─────────

/** The fields validateCampaignDraft checks. */
export interface CampaignDraftInput {
  name: string;
  subject: string;
  bodyMd: string;
  mode: string;
  interestIds: number[];
}

/** validateCampaignDraft's result: ok, or an error message to show. */
export type CampaignDraftValidation = { ok: true } | { ok: false; error: string };

/**
 * Validate a campaign draft before a CRUD save (create or PATCH) —
 * deliberately the ONLY gate this module applies before letting a save
 * proceed. It knows nothing about the physical address, a test send, the
 * audience count, or whether the body renders: those are #0045's Preflight
 * gate (lib/preflight.ts renders its verdict verbatim; this function must
 * never restate any of it, or the two copies drift — #0047's plan §9's own
 * anti-drift test asserts this stays true for a draft with no test send, no
 * physical address, and a zero audience).
 */
export function validateCampaignDraft(input: CampaignDraftInput): CampaignDraftValidation {
  if (input.name.trim() === '') {
    return { ok: false, error: 'Name is required.' };
  }
  if (input.subject.trim() === '') {
    return { ok: false, error: 'Subject is required.' };
  }
  if (input.bodyMd.trim() === '') {
    return { ok: false, error: 'Body is required.' };
  }
  if (interestsApplyToMode(input.mode) && input.interestIds.length === 0) {
    return { ok: false, error: 'Select at least one interest for this audience mode.' };
  }
  return { ok: true };
}

// ── Subject / preheader length guidance (advisory, never a block) ───────────

/** One length-guidance verdict: the current count, the soft limit, a tone, and a message. */
export interface LengthAdvice {
  count: number;
  limit: number;
  tone: 'ok' | 'warn' | 'over';
  message: string;
}

// Most inbox previews truncate a subject line well before 78 characters;
// 50 is the conservative point at which truncation starts to bite on a
// mobile-width inbox list. Guidance only — validateCampaignDraft above
// never reads these numbers, so an over-length subject cannot block a save.
const SUBJECT_LIMIT = 78;
const SUBJECT_WARN_AT = 50;

/** Length guidance for the subject field. Never blocks a save — see validateCampaignDraft. */
export function subjectLengthAdvice(len: number): LengthAdvice {
  if (len <= SUBJECT_WARN_AT) {
    return { count: len, limit: SUBJECT_LIMIT, tone: 'ok', message: `${len} characters` };
  }
  if (len <= SUBJECT_LIMIT) {
    return {
      count: len,
      limit: SUBJECT_LIMIT,
      tone: 'warn',
      message: `${len} characters — approaching ${SUBJECT_LIMIT}; some inboxes truncate long subject lines`,
    };
  }
  return {
    count: len,
    limit: SUBJECT_LIMIT,
    tone: 'over',
    message: `${len} characters — over ${SUBJECT_LIMIT}; likely to be truncated in the inbox list`,
  };
}

// The preheader is the inbox-preview snippet most clients show after the
// subject; that preview window is typically ~100 characters on mobile.
const PREHEADER_LIMIT = 100;
const PREHEADER_WARN_AT = 80;

/** Length guidance for the preheader field. Never blocks a save — see validateCampaignDraft. */
export function preheaderLengthAdvice(len: number): LengthAdvice {
  if (len <= PREHEADER_WARN_AT) {
    return { count: len, limit: PREHEADER_LIMIT, tone: 'ok', message: `${len} characters` };
  }
  if (len <= PREHEADER_LIMIT) {
    return {
      count: len,
      limit: PREHEADER_LIMIT,
      tone: 'warn',
      message: `${len} characters — approaching ${PREHEADER_LIMIT}; most inboxes preview less`,
    };
  }
  return {
    count: len,
    limit: PREHEADER_LIMIT,
    tone: 'over',
    message: `${len} characters — over ${PREHEADER_LIMIT}; most inboxes will cut this off`,
  };
}

// ── The post-scheduling demotion (#0045's worker) ────────────────────────────

/**
 * Whether a campaign currently showing `draft` was actually a `scheduled`
 * campaign the send worker refused and demoted back to `draft` (as opposed
 * to a campaign that was simply never scheduled). Detection needs no new
 * backend field: the demotion happens BEFORE the worker's start claim, so
 * `started_at` stays absent, and nothing clears `scheduled_at`.
 *
 * Uses `!= null` / `== null`, NOT `!== null` / `=== null` — `scheduled_at`
 * and `started_at` are `json:",omitempty"` on the Go side, so a NULL column
 * is an ABSENT key in the parsed response (`undefined`), never a present key
 * holding `null`. `undefined !== null` is `true`, so a strict-equality
 * version of this predicate would misfire on every never-scheduled draft;
 * `!= null` treats `undefined` and `null` identically, which is what the
 * real payload requires (#0041's carried-in review finding).
 */
export function wasDemotedAfterScheduling(c: Campaign): boolean {
  return c.status === 'draft' && c.scheduled_at != null && c.started_at == null;
}

/**
 * The operator-facing explanation shown atop a demoted campaign's editor and
 * list row. Deliberately does not restate WHY sending was refused — that
 * is the live pre-send panel (lib/preflight.ts's renderer), which is the
 * actionable answer since `#0114` records the send_refused audit row is not
 * yet filterable to this campaign.
 */
export function demotionExplanation(c: Campaign): string {
  const when = c.scheduled_at ? formatDateTime(c.scheduled_at) : 'an earlier time';
  return `This campaign was scheduled for ${when} but sending was refused and it was returned to draft. The checks below are its current state.`;
}

// ── Cancel copy (#0041's Cancel transition) ──────────────────────────────────

/**
 * Operator-facing copy for the cancel confirmation, keyed by the campaign's
 * CURRENT status (only `scheduled`, `sending`, and `paused_delivery_health`
 * ever offer Cancel — see canCancelCampaign).
 *
 * `remaining`, added by #0048, is the live remaining-recipient count from
 * lib/campaignProgress.ts's `remainingForCancel` — deliberately optional
 * and `undefined` by default (not a magic 0) so a caller with no live
 * snapshot yet (or none originally, before #0048) gets the same
 * no-digits fallback wording this function always returned: a stale or
 * fabricated number would be worse than none. `sending` and, since #0124,
 * `paused_delivery_health` both use it — a paused campaign has the
 * identical "some sent, some still queued" shape a sending one does, so
 * "nothing has been sent yet" would be actively wrong for it. `scheduled`
 * alone falls to the empty-audience wording: nothing has been sent yet,
 * so there is no "remaining out of what" to state.
 */
export function cancelCopy(status: string, remaining?: number): string {
  if (status === 'sending' || status === 'paused_delivery_health') {
    if (remaining != null) {
      const who = remaining === 1 ? '1 recipient' : `${remaining} recipients`;
      return `Messages already sent cannot be recalled. Cancelling stops sending to the ${who} not yet mailed.`;
    }
    return 'Messages already sent cannot be recalled. Cancelling stops sending to anyone not already mailed.';
  }
  return 'Nothing has been sent yet. Cancelling stops the send before it starts.';
}

/**
 * Operator-facing copy for the resume confirmation (#0124) — the campaign
 * is currently paused, and resuming hands it back to the send worker,
 * which re-evaluates the same circuit breaker on the next batch (never
 * bypassed).
 */
export function resumeCopy(): string {
  return 'This campaign was paused by the delivery-health circuit breaker. Resuming lets the worker continue sending — it will pause again if the bounce or complaint rate is still over threshold.';
}

/**
 * Whether a typed confirmation matches campaignSubject for the Resume
 * dialog — POST /admin/campaigns/{id}/resume's own server-side check
 * (admin_campaigns.go's Resume), restated here ONLY to gate the client's
 * button as an offer, never as enforcement (CLAUDE.md §9, #0047's
 * "typed confirmation enforced only in the browser is theatre" — the
 * server independently re-checks and refuses a mismatch with 400
 * regardless of what this returns). Trimmed, case-sensitive exact match,
 * mirroring the server's `strings.TrimSpace` comparison exactly — no
 * case-folding, so a mistyped case is still caught here before the round
 * trip, not just server-side.
 */
export function resumeSubjectMatches(typed: string, campaignSubject: string): boolean {
  const t = typed.trim();
  return t !== '' && t === campaignSubject.trim();
}

// ── Archive URL (#0123, PRD §6.8) ────────────────────────────────────────────

/**
 * The campaign's permanent public archive URL, built from the viewer's own
 * origin (there is no server-injected BASE_URL constant on the client — the
 * admin console is always browsed from the canonical host) and the
 * campaign's slug, which is minted at draft time and therefore never blank
 * (Campaign.slug's own doc comment). CampaignEditor.svelte's copyable field
 * calls this with `window.location.origin`, passed in rather than read
 * directly so this stays a pure, DOM-free function (#0094).
 */
export function archiveURL(origin: string, campaign: Pick<Campaign, 'slug'>): string {
  return `${origin.replace(/\/$/, '')}/archive/${encodeURIComponent(campaign.slug)}`;
}

/**
 * Whether the archive URL is live yet — true once the campaign has actually
 * sent (archive_status moves pending -> published in the SAME transaction
 * as the send, worker_store.go's CompleteIfDone), false for draft/
 * scheduled/sending/failed/canceled (archive_status still 'pending') and
 * for a withheld campaign (an admin's deliberate retraction — still not
 * "live" in the sense this note means).
 */
export function isArchiveLive(campaign: Pick<Campaign, 'archive_status'>): boolean {
  return campaign.archive_status === 'published';
}

/**
 * The copyable field's helper note: reserved-but-not-live, live, or
 * withheld. CampaignEditor.svelte renders this string verbatim rather than
 * branching on archive_status itself (#0094).
 */
export function archiveURLNote(campaign: Pick<Campaign, 'archive_status'>): string {
  switch (campaign.archive_status) {
    case 'published':
      return 'Live — this URL is public.';
    case 'withheld':
      return 'Withheld — this page currently answers 410 Gone.';
    default:
      return 'Reserved — this URL goes live once the campaign is sent.';
  }
}

/** The Copy button's label for the archive-URL field's copy-to-clipboard
 * state, so CampaignEditor.svelte's markup contains no inline ternary over
 * `archiveURLCopyState` (#0094 — see this module's own header comment). */
export type ArchiveURLCopyState = 'idle' | 'copied' | 'error';

export function archiveURLCopyButtonLabel(state: ArchiveURLCopyState): string {
  switch (state) {
    case 'copied':
      return 'Copied';
    case 'error':
      return 'Copy failed';
    default:
      return 'Copy';
  }
}
