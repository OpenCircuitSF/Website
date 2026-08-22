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
