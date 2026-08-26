import { describe, it, expect } from 'vitest';
import {
  crtTruncate,
  crtShortDate,
  crtWorkshopLines,
  crtListLines,
  CRT_LINE_CHARS,
} from './crtScreen';

describe('crtTruncate', () => {
  it('leaves a line that fits alone', () => {
    expect(crtTruncate('soldering 101')).toBe('soldering 101');
  });

  // #0274: a long title must not run off the glass. The screen is decorative
  // and an overflowing line reads as a bug.
  it('truncates a long title to the glass width', () => {
    const long = 'Introduction to Soldering and Surface Mount Rework for Absolute Beginners';
    const out = crtTruncate(long);
    expect(out.length).toBeLessThanOrEqual(CRT_LINE_CHARS);
    expect(out.endsWith('…')).toBe(true);
  });
});

describe('crtShortDate', () => {
  it('formats an ISO date in the screen register', () => {
    expect(crtShortDate('2026-09-04T01:30:00Z')).toMatch(/^[a-z]{3} [a-z]{3} \d+$/);
  });

  // An unparseable date must not reach the glass as "Invalid Date".
  it('returns empty for an unparseable date', () => {
    expect(crtShortDate('not-a-date')).toBe('');
  });
});

describe('crtWorkshopLines', () => {
  it('shows at most three workshops and a total', () => {
    const ws = Array.from({ length: 5 }, (_, i) => ({
      title: 'Workshop ' + i,
      starts_at: '2026-09-04T01:30:00Z',
    }));
    const lines = crtWorkshopLines(ws);
    expect(lines).toHaveLength(4);
    expect(lines[3]).toBe('5 scheduled.');
  });

  it('singularises a lone workshop', () => {
    const lines = crtWorkshopLines([{ title: 'Only one', starts_at: '2026-09-04T01:30:00Z' }]);
    expect(lines[lines.length - 1]).toBe('1 scheduled.');
  });

  // Empty means "no data", and the caller falls back to the illustrative
  // session rather than printing an empty block (#0274).
  it('returns nothing for an empty list', () => {
    expect(crtWorkshopLines([])).toEqual([]);
  });

  it('keeps every line within the glass width', () => {
    const lines = crtWorkshopLines([
      { title: 'Introduction to Soldering and Surface Mount Rework', starts_at: '2026-09-04T01:30:00Z' },
    ]);
    for (const l of lines) expect(l.length).toBeLessThanOrEqual(CRT_LINE_CHARS);
  });
});

describe('crtListLines', () => {
  // pending is bucketed server-side, so the screen must not state it as exact.
  it('marks the bucketed pending count as approximate', () => {
    const lines = crtListLines(42, 5);
    expect(lines.some((l) => l.includes('~5'))).toBe(true);
  });

  it('omits pending entirely when there is none', () => {
    const lines = crtListLines(42, 0);
    expect(lines.some((l) => l.includes('awaiting'))).toBe(false);
  });

  it('singularises one confirmed subscriber', () => {
    expect(crtListLines(1, 0)[0]).toBe('1 confirmed subscriber');
  });
});
