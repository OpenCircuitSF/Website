// Pure helpers for the campaign compose screen's typed-count send
// confirmation and the Send button's guard state (#0047 §7). This module is
// the SINGLE SOURCE OF TRUTH for whether mail can go out from this screen —
// `sendGuardState` is the one function §9 of #0047's plan names its
// mutation-check proof against, so its branches must stay exactly as
// enumerated here (see that function's own doc comment).

import type { UnmetRequirement } from './types';

/**
 * Parse a typed recipient-count confirmation into a number, or `null` when
 * the input is not a bare non-negative integer. Accepts optional
 * surrounding whitespace and `,` (or a thin space, U+2009) as a thousands
 * separator — so "1,234" is not a trap. Rejects anything else, including
 * decimals, a negative sign, and trailing/embedded non-digit characters.
 */
export function normalizeCountInput(raw: string): number | null {
  const trimmed = raw.trim();
  if (trimmed === '') {
    return null;
  }
  const stripped = trimmed.replace(/[, ]/g, '');
  if (!/^\d+$/.test(stripped)) {
    return null;
  }
  const n = Number(stripped);
  if (!Number.isSafeInteger(n)) {
    return null;
  }
  return n;
}

/** Whether a typed confirmation exactly matches the authoritative count. Strict equality. */
export function confirmMatches(raw: string, count: number): boolean {
  const n = normalizeCountInput(raw);
  return n !== null && n === count;
}

/**
 * A short hint line for the typed-count field: `null` while the field is
 * untouched (empty) or already matches — no scolding an empty field — and
 * otherwise one line naming the expected count.
 */
export function confirmHint(raw: string, count: number): string | null {
  if (raw.trim() === '') {
    return null;
  }
  if (confirmMatches(raw, count)) {
    return null;
  }
  return `Doesn't match — type ${count} to confirm.`;
}

/** The facts sendGuardState needs to decide whether the Send control may act. */
export interface SendGuardInput {
  status: string;
  unmet: UnmetRequirement[];
  audienceCount: number;
  confirmRaw: string;
  inFlight: boolean;
}

/**
 * The Send button/dialog's guard state — a discriminated union so the
 * component never has to re-derive "can this send" from raw booleans.
 *
 *   - `unavailable`   — status isn't `draft` (the send control shouldn't
 *                        even be shown; canSendCampaign in campaigns.ts is
 *                        the render gate, this is the defensive fallback).
 *   - `sending`        — a send request for THIS campaign is already in
 *                        flight (the dialog's own submitting state).
 *   - `blocked`        — the pre-send checks list is non-empty. This wins
 *                        over a correct typed count: a correct confirmation
 *                        of an audience the campaign still cannot legally
 *                        mail is not "ready".
 *   - `empty-audience` — the checks pass but the live audience count is 0.
 *   - `needs-confirm`  — everything else is clear but the typed count does
 *                        not (yet) match.
 *   - `ready`          — every check passes, the audience is non-empty, and
 *                        the typed count matches exactly.
 */
export type SendGuardState =
  | { kind: 'unavailable' }
  | { kind: 'sending' }
  | { kind: 'blocked' }
  | { kind: 'empty-audience' }
  | { kind: 'needs-confirm' }
  | { kind: 'ready' };

/**
 * Compute the Send control's guard state. This is the single source of
 * truth for whether mail can go out from this screen — CampaignEditor.svelte
 * and CampaignSendDialog.svelte must only ever branch on `.kind`, never
 * recompute any of the conditions below themselves.
 */
export function sendGuardState(input: SendGuardInput): SendGuardState {
  if (input.status !== 'draft') {
    return { kind: 'unavailable' };
  }
  if (input.inFlight) {
    return { kind: 'sending' };
  }
  if (input.unmet.length > 0) {
    return { kind: 'blocked' };
  }
  if (input.audienceCount === 0) {
    return { kind: 'empty-audience' };
  }
  if (!confirmMatches(input.confirmRaw, input.audienceCount)) {
    return { kind: 'needs-confirm' };
  }
  return { kind: 'ready' };
}

/**
 * The pre-send panel's accessible summary line for a given guard state
 * (#0119). This is the ONLY thing that decides what the blocked Send
 * button's `aria-describedby` text says — CampaignEditor.svelte renders it
 * verbatim and must never re-derive its own wording from the raw booleans,
 * for the same reason `sendGuardState` itself is the single source of truth
 * for whether the button may act (see that function's doc comment).
 *
 * Every `SendGuardState.kind` gets a non-empty line EXCEPT `unavailable`
 * (the send control isn't rendered at all in that state — canSendCampaign
 * is the render gate — so there is nothing to describe) and `needs-confirm`
 * (that state exists only inside CampaignSendDialog's own typed-count
 * field, never at the editor level, whose `confirmRaw` is always fabricated
 * to match `audienceCount` — see the editor's `editorGuard` comment).
 *
 * `blocked` and `empty-audience` are both real, independently reachable
 * reasons the editor-level button can be disabled (`sendGateOpen` in
 * CampaignEditor.svelte), and #0119 was filed because a screen reader
 * announced neither. `blocked` deliberately says "Pre-send checks" so an
 * assistive-tech user hears the same phrase the panel itself is titled
 * with, per #0119's acceptance criteria.
 */
export function sendGuardDescription(guard: SendGuardState): string {
  switch (guard.kind) {
    case 'unavailable':
      return '';
    case 'sending':
      return 'A send for this campaign is already in progress.';
    case 'blocked':
      return 'Pre-send checks have not passed. See the list below.';
    case 'empty-audience':
      return 'Pre-send checks pass, but the current audience is empty. Adjust targeting before sending.';
    case 'needs-confirm':
      return 'Pre-send checks pass. Confirm the recipient count to send.';
    case 'ready':
      return 'All pre-send checks pass.';
  }
}

/**
 * The CSS class for `sendGuardDescription`'s text — `text-notice` (success
 * green, app.css) only when nothing is blocking the send; `text-muted`
 * otherwise. Kept out of CampaignEditor.svelte for the same reason as
 * `sendGuardDescription` itself: the component must never re-derive a
 * presentation decision from `.kind` inline (#0094).
 */
export function sendGuardSummaryClass(guard: SendGuardState): string {
  return guard.kind === 'ready' ? 'text-notice' : 'text-muted';
}
