// Pure, framework-free helpers backing the admin deliverability screen
// (#0124, PRD §6.9): the read side of the consecutive-streak repeated-
// soft-bounce rule and the per-address email_events history behind it.
//
// #0094 means nothing in a .svelte file is covered by a test, so every
// formatting decision Deliverability.svelte's table/history render makes
// routes through here rather than a template-inline comparison or string
// concatenation.

import type { DeliverabilityEvent } from './types';

/**
 * A one-line, human-readable label for one email_events row — the event
 * type, plus the bounce type/subtype when present (a Bounce event always
 * carries bounce_type; bounce_subtype is present more often than not but
 * SES does not guarantee it). Complaint/Delivery/other event types have no
 * bounce fields at all, so the label is just the event type for those.
 */
export function deliverabilityEventLabel(ev: DeliverabilityEvent): string {
  if (ev.event_type !== 'Bounce' || !ev.bounce_type) {
    return ev.event_type;
  }
  if (ev.bounce_subtype) {
    return `Bounce — ${ev.bounce_type} (${ev.bounce_subtype})`;
  }
  return `Bounce — ${ev.bounce_type}`;
}

/**
 * Badge CSS class for an email_events row's event type: red for a
 * Permanent bounce or a Complaint (both suppress immediately), muted amber-
 * adjacent for a Transient/Undetermined soft bounce (contributes to the
 * streak but doesn't suppress on its own), green for a Delivery (what
 * resets the streak), muted for anything else (Reject, RenderingFailure,
 * DeliveryDelay, Send, and anything SES adds later).
 */
export function deliverabilityEventBadgeClass(ev: DeliverabilityEvent): string {
  if (ev.event_type === 'Complaint') {
    return 'badge-danger';
  }
  if (ev.event_type === 'Bounce') {
    return ev.bounce_type === 'Permanent' ? 'badge-danger' : 'badge-muted';
  }
  if (ev.event_type === 'Delivery') {
    return 'badge-success';
  }
  return 'badge-muted';
}

/**
 * The streak cell's text for the deliverability list — "0" reads as "no
 * current streak" (the address has bounce HISTORY but nothing consecutive
 * right now, e.g. reset by a later Delivery), so it is worth distinguishing
 * from a nonzero streak rather than just printing the bare number both
 * ways.
 */
export function streakSummary(streak: number): string {
  return streak === 0 ? '0 (reset)' : String(streak);
}
