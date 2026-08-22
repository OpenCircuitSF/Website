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

/** The six mailing.CampaignStatus* values, in no particular order. */
export const CAMPAIGN_STATUSES: readonly string[] = [
  'draft',
  'scheduled',
  'sending',
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
 * a failed one, muted for every in-progress or terminal-but-uneventful state
 * (draft/scheduled/sending/canceled) — mirrors subscriberStatusBadgeClass's
 * shape in lib/admin.ts.
 */
export function campaignStatusBadgeClass(status: string): string {
  switch (status) {
    case 'sent':
      return 'badge-success';
    case 'failed':
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

/** Whether the Cancel control should be shown for a campaign at this status. */
export function canCancelCampaign(status: string): boolean {
  return status === 'scheduled' || status === 'sending';
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
 * list row. Deliberately does not restate WHY the worker refused it — that
 * is the live pre-send panel (lib/preflight.ts's renderer), which is the
 * actionable answer since `#0114` records the send_refused audit row is not
 * yet filterable to this campaign.
 */
export function demotionExplanation(c: Campaign): string {
  const when = c.scheduled_at ? formatDateTime(c.scheduled_at) : 'an earlier time';
  return `This campaign was scheduled for ${when} but the send worker refused it and returned it to draft. The checks below are its current state.`;
}

// ── Cancel copy (#0041's Cancel transition) ──────────────────────────────────

/**
 * Operator-facing copy for the cancel confirmation, keyed by the campaign's
 * CURRENT status (only `scheduled` and `sending` ever offer Cancel — see
 * canCancelCampaign). Deliberately carries no number for the `sending` case:
 * the remaining-recipient count is #0048's progress stream to add, and a
 * stale number here would be worse than none.
 */
export function cancelCopy(status: string): string {
  if (status === 'sending') {
    return 'Messages already sent cannot be recalled. Cancelling stops the worker from sending to anyone not already mailed.';
  }
  return 'Nothing has been sent yet. Cancelling stops the send before it starts.';
}
