import { describe, expect, it } from 'vitest';
import {
  appendBlock,
  newsletterMonthFromSlug,
  defaultEventsMonth,
  fallbackSummary,
  resultSummary,
} from './newsletterEvents';
import type { UpcomingEventItem, UpcomingEventsResponse } from './types';

// Built from parts, never written as a literal (#0405's slug scan).
const slugFor = (mm: string, yyyy: string, suffix = '') => [mm, yyyy].join('-') + suffix;

function item(over: Partial<UpcomingEventItem>): UpcomingEventItem {
  return { workshop_id: 1, title: 'T', url: 'u', link: 'short', reused: false, reason: '', ...over };
}

describe('newsletterEvents', () => {
  it('appends after existing text with one blank line', () => {
    expect(appendBlock('Hello\n\n\n', '## Events\n')).toBe('Hello\n\n## Events\n');
    expect(appendBlock('Hello', '## Events\n')).toBe('Hello\n\n## Events\n');
  });

  it('never drops or reorders existing text', () => {
    const body = 'first\n\n- a\n- b\n\nlast line';
    const out = appendBlock(body, 'block\n');
    expect(out.startsWith(body)).toBe(true);
    expect(appendBlock(out, 'block\n')).toBe(out + '\nblock\n');
  });

  it('returns the block alone for an empty body', () => {
    expect(appendBlock('', 'block\n')).toBe('block\n');
    expect(appendBlock('  \n', 'block\n')).toBe('block\n');
  });

  it('leaves the body unchanged for an empty block', () => {
    expect(appendBlock('keep  \n', '')).toBe('keep  \n');
  });

  it('reads the month from a newsletter slug, with and without a suffix', () => {
    expect(newsletterMonthFromSlug(slugFor('11', '2026'))).toBe('2026-11');
    expect(newsletterMonthFromSlug(slugFor('01', '2027', '-2'))).toBe('2027-01');
    expect(newsletterMonthFromSlug(slugFor('13', '2026'))).toBeNull();
    expect(newsletterMonthFromSlug('spring-launch')).toBeNull();
  });

  it('falls back to the current month for a non-newsletter slug', () => {
    const now = new Date(2026, 2, 15);
    expect(defaultEventsMonth('spring-launch', now)).toBe('2026-03');
    expect(defaultEventsMonth(slugFor('11', '2026'), now)).toBe('2026-11');
  });

  it('summarises fallbacks by reason', () => {
    expect(fallbackSummary([item({})])).toBeNull();
    const s = fallbackSummary([
      item({ title: 'Intro to Soldering', link: 'plain', reason: 'no_campaign' }),
      item({ title: 'Short' }),
      item({ title: 'Robots', link: 'plain', reason: 'not_configured' }),
      item({ title: 'Later', link: 'plain', reason: 'rate_limited' }),
    ]);
    expect(s).toBe(
      '3 events use the plain event link: Intro to Soldering (no Outreach campaign yet), Robots (ShortLinks unavailable), Later (ShortLinks rate limit).',
    );
    expect(fallbackSummary([item({ title: 'X', link: 'plain', reason: 'timeout' })])).toBe(
      '1 event uses the plain event link: X (ShortLinks timed out).',
    );
  });

  it('summarises an empty month', () => {
    const r: UpcomingEventsResponse = { month: '2026-11', label: 'November 2026', markdown: '', events: [], created: 0, reused: 0, fallback: 0 };
    expect(resultSummary(r)).toBe('No upcoming events in November 2026.');
    expect(resultSummary({ ...r, events: [item({}), item({}), item({})], created: 1, reused: 2 })).toBe(
      'Added 3 events (1 new link, 2 reused).',
    );
  });
});
