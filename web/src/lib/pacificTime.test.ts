import { describe, expect, it } from 'vitest';
import { formatDateTime } from './admin';
import { weekdayTime } from './outreachCards';
import {
  formatPacificDateTime,
  formatPacificWeekdayTime,
  fromPacificInput,
  toPacificInput,
} from './pacificTime';

// Every assertion names explicit instants and Pacific strings, so the result
// is independent of the machine's TZ (also run under TZ=UTC and Asia/Tokyo).

// 6:30 PM PDT Oct 31, 2026 -- the Halloween workshop from the review.
const OCT31_630PM_PT = '2026-11-01T01:30:00Z';

describe('Pacific display (K1)', () => {
  it('renders the Oct 31 6:30 PM fixture as Oct 31, not Nov 1, whatever the host zone', () => {
    expect(formatDateTime(OCT31_630PM_PT)).toBe('Oct 31, 2026, 6:30 PM PT');
    expect(weekdayTime(OCT31_630PM_PT)).toBe('Sat, Oct 31, 6:30 PM PT');
    expect(formatPacificDateTime(OCT31_630PM_PT)).toContain('Oct 31');
    expect(formatPacificWeekdayTime(OCT31_630PM_PT)).toContain('Oct 31');
  });
  it('uses standard time after the November change', () => {
    expect(formatDateTime('2026-11-28T18:00:00Z')).toBe('Nov 28, 2026, 10:00 AM PT');
  });
  it('falls back to the raw string when it does not parse', () => {
    expect(formatDateTime('nope')).toBe('nope');
    expect(weekdayTime('nope')).toBe('nope');
  });
});

describe('named regression: Nov 1 1:30 AM vs Oct 31 6:30 PM (#0585)', () => {
  it('one instant never reads as two days: the list/editor/outreach/Results display and the editor input agree', () => {
    const shown = formatDateTime(OCT31_630PM_PT);
    expect(shown).not.toContain('Nov 1');
    expect(shown).not.toContain('1:30 AM');
    expect(weekdayTime(OCT31_630PM_PT)).not.toContain('Nov 1');
    // The editor input shows the same wall time the display shows.
    expect(toPacificInput(OCT31_630PM_PT)).toBe('2026-10-31T18:30');
    // And saving what the input shows returns the identical instant.
    expect(fromPacificInput('2026-10-31T18:30')).toBe(OCT31_630PM_PT);
  });
});

describe('Pacific input conversion across the 2026-11-01 DST boundary', () => {
  it('Oct 31 evening is daylight time (UTC-7)', () => {
    expect(fromPacificInput('2026-10-31T18:30')).toBe('2026-11-01T01:30:00Z');
  });
  it('Nov 1 noon is standard time (UTC-8)', () => {
    expect(fromPacificInput('2026-11-01T12:00')).toBe('2026-11-01T20:00:00Z');
    expect(toPacificInput('2026-11-01T20:00:00Z')).toBe('2026-11-01T12:00');
  });
  it('the ambiguous 2026-11-01 01:30 resolves to its FIRST occurrence (PDT, 08:30Z)', () => {
    expect(fromPacificInput('2026-11-01T01:30')).toBe('2026-11-01T08:30:00Z');
    // Both occurrences display as the same wall time.
    expect(toPacificInput('2026-11-01T08:30:00Z')).toBe('2026-11-01T01:30');
    expect(toPacificInput('2026-11-01T09:30:00Z')).toBe('2026-11-01T01:30');
  });
  it('a spring-forward gap time rolls forward (2026-03-08 02:30 -> 03:30 PDT)', () => {
    expect(fromPacificInput('2026-03-08T02:30')).toBe('2026-03-08T10:30:00Z');
    expect(toPacificInput('2026-03-08T10:30:00Z')).toBe('2026-03-08T03:30');
  });
  it('midnight renders as 00:00, not 24:00', () => {
    expect(toPacificInput('2026-07-01T07:00:00Z')).toBe('2026-07-01T00:00');
  });
  it('rejects garbage and empties', () => {
    expect(fromPacificInput('')).toBeNull();
    expect(fromPacificInput('tomorrow')).toBeNull();
    expect(toPacificInput('')).toBe('');
    expect(toPacificInput(null)).toBe('');
    expect(toPacificInput('garbage')).toBe('');
  });
});
