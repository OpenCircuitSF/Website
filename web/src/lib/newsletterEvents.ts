// #0643: the campaign editor's "Add upcoming events" button. The server
// builds the block (POST /admin/campaigns/{id}/upcoming-events); this module
// appends it to the body without ever replacing or reordering what is there,
// and words the result for the operator.
import { currentMonthValue } from './campaigns';
import type { UpcomingEventItem, UpcomingEventsResponse } from './types';

/**
 * Appends `block` after `body`, separated by one blank line. Trailing
 * whitespace on `body` is trimmed so the gap is always exactly one blank
 * line; nothing else in `body` changes. An empty block leaves the body as it
 * is, and an empty (or whitespace-only) body yields the block alone.
 */
export function appendBlock(body: string, block: string): string {
  if (block === '') return body;
  if (body.trim() === '') return block;
  return body.replace(/\s+$/, '') + '\n\n' + block;
}

// A newsletter slug is the month, then the year (#0405's slug template), with
// an optional collision suffix ("-2", "-3", ...) that Create appends.
const newsletterSlugPattern = /^(0[1-9]|1[0-2])-(\d{4})(?:-\d+)?$/;

/** The YYYY-MM month a newsletter slug names, or null for any other slug. */
export function newsletterMonthFromSlug(slug: string): string | null {
  const m = newsletterSlugPattern.exec(slug);
  return m ? `${m[2]}-${m[1]}` : null;
}

/** The month the editor's picker starts on: the newsletter's own, else now's. */
export function defaultEventsMonth(slug: string, now: Date): string {
  return newsletterMonthFromSlug(slug) ?? currentMonthValue(now);
}

const reasonWords: Record<string, string> = {
  no_campaign: 'no Outreach campaign yet',
  not_configured: 'ShortLinks unavailable',
  rejected: 'ShortLinks unavailable',
  rate_limited: 'ShortLinks rate limit',
  timeout: 'ShortLinks timed out',
  failed: 'ShortLinks error',
};

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** Names the events that fell back to the plain link, and why; null if none. */
export function fallbackSummary(events: UpcomingEventItem[]): string | null {
  const fell = events.filter((e) => e.link === 'plain');
  if (fell.length === 0) return null;
  const parts = fell.map((e) => `${e.title} (${reasonWords[e.reason] ?? 'ShortLinks error'})`);
  return `${plural(fell.length, 'event uses', 'events use')} the plain event link: ${parts.join(', ')}.`;
}

/** One sentence describing what the button did. */
export function resultSummary(r: UpcomingEventsResponse): string {
  if (r.events.length === 0) return `No upcoming events in ${r.label}.`;
  return `Added ${plural(r.events.length, 'event', 'events')} (${plural(r.created, 'new link', 'new links')}, ${r.reused} reused).`;
}
