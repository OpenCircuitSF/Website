import { describe, it, expect } from 'vitest';
import {
  formatWorkshopDate,
  workshopLocationLabel,
  isCanceled,
  workshopBadgeLabel,
  hasNoUpcoming,
  nextWorkshops,
  NEXT_UP_LIMIT,
} from './workshops';
import type { PublicWorkshop, WorkshopsListResponse } from './types';

function workshop(fields: Partial<PublicWorkshop> = {}): PublicWorkshop {
  return {
    slug: 'soldering-101',
    title: 'Soldering fundamentals',
    status: 'published',
    interests: [],
    ...fields,
  };
}

describe('formatWorkshopDate', () => {
  it('returns "Date TBA" when starts_at is absent', () => {
    expect(formatWorkshopDate(undefined)).toBe('Date TBA');
    expect(formatWorkshopDate(null)).toBe('Date TBA');
  });

  it('returns "Date TBA" for an unparseable starts_at', () => {
    expect(formatWorkshopDate('not-a-date')).toBe('Date TBA');
  });

  it('formats a start time alone when ends_at is absent', () => {
    const s = formatWorkshopDate('2026-09-12T18:00:00Z');
    // Locale-dependent formatting, so assert on the pieces that must appear
    // rather than the whole string.
    expect(s).toContain('2026');
    expect(s).not.toContain('–');
  });

  it('formats a start–end time range when both are present and parseable', () => {
    const s = formatWorkshopDate('2026-09-12T18:00:00Z', '2026-09-12T20:00:00Z');
    expect(s).toContain('2026');
    expect(s).toContain('–');
  });

  it('falls back to the start alone when ends_at does not parse', () => {
    const s = formatWorkshopDate('2026-09-12T18:00:00Z', 'garbage');
    expect(s).not.toContain('–');
  });
});

describe('workshopLocationLabel', () => {
  it('returns the location name when set', () => {
    expect(workshopLocationLabel({ location_name: 'Noisebridge' })).toBe('Noisebridge');
  });

  it('falls back to "Location TBA" when unset or blank', () => {
    expect(workshopLocationLabel({ location_name: undefined })).toBe('Location TBA');
    expect(workshopLocationLabel({ location_name: '' })).toBe('Location TBA');
    expect(workshopLocationLabel({ location_name: '   ' })).toBe('Location TBA');
  });
});

describe('isCanceled / workshopBadgeLabel', () => {
  it('is true, with a "Canceled" badge, only for status "canceled"', () => {
    expect(isCanceled({ status: 'canceled' })).toBe(true);
    expect(workshopBadgeLabel({ status: 'canceled' })).toBe('Canceled');
  });

  it('is false, with no badge, for "published" and any other status', () => {
    for (const status of ['published', 'draft', '', 'unknown']) {
      expect(isCanceled({ status })).toBe(false);
      expect(workshopBadgeLabel({ status })).toBeNull();
    }
  });
});

describe('hasNoUpcoming', () => {
  it('is true when upcoming is empty, regardless of past', () => {
    expect(hasNoUpcoming({ upcoming: [] })).toBe(true);
  });

  it('is false when upcoming has at least one workshop', () => {
    expect(hasNoUpcoming({ upcoming: [workshop()] })).toBe(false);
  });
});

describe('nextWorkshops', () => {
  const upcoming = [
    workshop({ slug: 'a' }),
    workshop({ slug: 'b' }),
    workshop({ slug: 'c' }),
    workshop({ slug: 'd' }),
  ];

  it('caps at NEXT_UP_LIMIT (3) by default', () => {
    const list: Pick<WorkshopsListResponse, 'upcoming'> = { upcoming };
    const result = nextWorkshops(list);
    expect(result).toHaveLength(NEXT_UP_LIMIT);
    expect(result.map((w) => w.slug)).toEqual(['a', 'b', 'c']);
  });

  it('respects an explicit smaller limit', () => {
    expect(nextWorkshops({ upcoming }, 1).map((w) => w.slug)).toEqual(['a']);
  });

  it('does not re-sort — trusts the server\'s chronological ordering', () => {
    const reversed = [workshop({ slug: 'z' }), workshop({ slug: 'y' })];
    expect(nextWorkshops({ upcoming: reversed }).map((w) => w.slug)).toEqual(['z', 'y']);
  });

  it('returns [] when upcoming is empty, and clamps a negative limit to []', () => {
    expect(nextWorkshops({ upcoming: [] })).toEqual([]);
    expect(nextWorkshops({ upcoming }, -1)).toEqual([]);
  });
});
