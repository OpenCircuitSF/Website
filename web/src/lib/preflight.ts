// Pure helpers for the campaign compose screen's "pre-send checks" panel
// (#0047 §5). This module owns exactly one thing: turning a server response
// into the ordered list of unmet requirements the panel renders — and NOT
// owning a code→message table, which is the whole point (see parseUnmet's
// doc comment). Two sources land here through the same parser so there is
// only ever one renderer: the 200 advisory body from GET
// .../campaigns/{id}/preflight, and the {"unmet":[...]} 409 body POST
// .../send answers when its own advisory check fails.

import { ApiError } from './api';
import type { UnmetRequirement } from './types';

export type { UnmetRequirement };

/**
 * Parse a preflight "unmet requirements" body, whether it came from a 200
 * advisory response or an `ApiError.body` off a 409. Returns `null` when
 * `body` is not the `{"unmet":[{code,message}]}` shape at all — DISTINCT
 * from `[]`, which means "checked, nothing unmet". Order is preserved
 * exactly as received: #0045 pins ten codes in a fixed, tested order and
 * this function must never re-sort or group them.
 *
 * An entry whose `message` is missing or blank (after trimming) falls back
 * to the raw `code` string — never invented copy, and never dropped: a
 * requirement the client does not recognise must still appear.
 */
export function parseUnmet(body: unknown): UnmetRequirement[] | null {
  if (body === null || typeof body !== 'object' || !('unmet' in body)) {
    return null;
  }
  const raw = (body as { unmet: unknown }).unmet;
  if (!Array.isArray(raw)) {
    return null;
  }
  const out: UnmetRequirement[] = [];
  for (const entry of raw) {
    if (entry === null || typeof entry !== 'object') {
      continue;
    }
    const code = (entry as { code?: unknown }).code;
    if (typeof code !== 'string') {
      continue;
    }
    const rawMessage = (entry as { message?: unknown }).message;
    const message =
      typeof rawMessage === 'string' && rawMessage.trim() !== '' ? rawMessage : code;
    out.push({ code, message });
  }
  return out;
}

/**
 * Extract a preflight unmet list from a failed `sendCampaign`/`cancelCampaign`
 * call's caught error: `err.body` for an `ApiError`, `null` for anything
 * else (a network failure, or a 409 that used the plain `{"error":"..."}`
 * shape rather than the preflight one — see admin_campaigns.go's own doc
 * comment on the two distinguishable 409 shapes).
 */
export function parseUnmetFromError(err: unknown): UnmetRequirement[] | null {
  if (err instanceof ApiError) {
    return parseUnmet(err.body);
  }
  return null;
}

/** Whether a parsed unmet list means the send gate is currently blocked. */
export function isPreflightBlocked(list: UnmetRequirement[]): boolean {
  return list.length > 0;
}

/** Where in this SPA an operator fixes a given unmet-requirement code. */
export interface FixLocation {
  section: string;
  label: string;
}

/**
 * Map a preflight code to the admin subtab where an operator fixes it, or
 * `null` when there is no in-SPA affordance for it (deploy-only
 * configuration like EMAIL_REPLY_TO/EMAIL_LIST_DOMAIN, or a condition that's
 * already visible on the current screen, like the audience panel or the
 * test-send control). Deliberately NOT a restatement of the server's
 * message — an affordance only. Returns `null` for any code it does not
 * recognise, so an unrecognised future code renders its server message with
 * no link and nothing breaks.
 */
export function fixLocation(code: string): FixLocation | null {
  if (code === 'physical_address_missing') {
    return { section: 'settings', label: 'Go to Settings' };
  }
  return null;
}
