// Pure logic for the pending-subscriber admin screen (#0128) — no DOM, no
// fetch, unit-testable without jsdom (CLAUDE.md §1's "SPA logic goes in
// plain TypeScript modules" convention, mirroring lib/dashboard.ts and
// lib/admin.ts). Pending.svelte calls these; it makes no formatting or
// classification decisions of its own.

import type { PendingSubscriber } from './types';

/**
 * Humanizes age_seconds (server-computed, see PendingSubscriber's doc
 * comment) into the largest whole unit that reads naturally — "45 seconds",
 * "12 minutes", "3 hours", "2 days" — never a fraction, and never negative
 * (a clock-skew or in-flight request landing between reads clamps to "just
 * now" instead of "-3 seconds").
 */
export function formatPendingAge(ageSeconds: number): string {
  const s = Math.max(0, Math.floor(ageSeconds));
  if (s < 60) return 'just now';
  const minutes = Math.floor(s / 60);
  if (minutes < 60) return pluralUnit(minutes, 'minute');
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return pluralUnit(hours, 'hour');
  const days = Math.floor(hours / 24);
  return pluralUnit(days, 'day');
}

function pluralUnit(n: number, unit: string): string {
  return `${n} ${unit}${n === 1 ? '' : 's'}`;
}

/**
 * The signup-source summary column (#0128 criterion: "signup source, UTM
 * attribution"). Joins whichever of utm_source/utm_medium/utm_campaign are
 * present as "source / medium / campaign"; "Direct" when none are set — a
 * signup with no UTM parameters at all, not a missing-data placeholder.
 */
export function pendingSignupSource(row: Pick<PendingSubscriber, 'utm_source' | 'utm_medium' | 'utm_campaign'>): string {
  const parts = [row.utm_source, row.utm_medium, row.utm_campaign].filter(
    (p): p is string => Boolean(p && p.trim() !== ''),
  );
  return parts.length > 0 ? parts.join(' / ') : 'Direct';
}

/** Operator-facing label for a queue_state value. */
export function queueStateLabel(state: string): string {
  switch (state) {
    case 'queued':
      return 'Queued';
    case 'sending':
      return 'Sending';
    case 'sent':
      return 'Sent';
    case 'abandoned':
      return 'Abandoned';
    case 'none':
      return 'Not sent';
    default:
      return 'Unknown';
  }
}

/**
 * Badge class for a queue_state value — 'abandoned' is the one state that
 * actually explains a non-confirmation as a delivery failure (#0128's own
 * framing), so it gets the same danger treatment as an expired row.
 */
export function queueStateBadgeClass(state: string): string {
  switch (state) {
    case 'sent':
      return 'badge-success';
    case 'abandoned':
      return 'badge-danger';
    default:
      return 'badge-muted';
  }
}

/** Badge class for the expired flag — visually distinct from a live pending row (#0128 criterion). */
export function pendingExpiredBadgeClass(expired: boolean): string {
  return expired ? 'badge-danger' : 'badge-muted';
}

/**
 * Sorts rows for display. The server already sorts (GET
 * /admin/subscribers/pending?sort=...), so this exists only for a client
 * that wants to re-sort an already-loaded page without a round trip — used
 * by the age-column header's click-to-resort. Stable: ties keep their
 * existing relative order (Array.prototype.sort's ES2019+ guarantee).
 */
export function sortPendingByAge(rows: PendingSubscriber[], oldestFirst: boolean): PendingSubscriber[] {
  const sorted = [...rows];
  sorted.sort((a, b) => {
    const diff = b.age_seconds - a.age_seconds; // oldest (largest age) first
    return oldestFirst ? diff : -diff;
  });
  return sorted;
}
