import { describe, expect, it } from 'vitest';
import {
  assignableVenues,
  defaultVenueId,
  nextVenueSortOrder,
  sortedVenues,
  validateNewVenue,
  venueDeleteBlock,
  venueOptionLabel,
} from './venues';
import type { Venue } from './types';

function venue(id: number, over: Partial<Venue> = {}): Venue {
  return {
    id,
    name: `Venue ${id}`,
    address: null,
    note: null,
    enabled: true,
    is_default: false,
    sort_order: id * 10,
    workshop_count: 0,
    created_at: '2026-11-04T00:00:00Z',
    updated_at: '2026-11-04T00:00:00Z',
    ...over,
  };
}

describe('venues', () => {
  it('sorts by sort_order then name', () => {
    const got = sortedVenues([
      venue(1, { sort_order: 20, name: 'B' }),
      venue(2, { sort_order: 20, name: 'A' }),
      venue(3, { sort_order: 10 }),
    ]);
    expect(got.map((v) => v.id)).toEqual([3, 2, 1]);
  });

  it('requires a name and trims it', () => {
    expect(validateNewVenue('  ')).toHaveProperty('error');
    expect(validateNewVenue(' The Shop ')).toEqual({ name: 'The Shop' });
  });

  it('puts a new venue last', () => {
    expect(nextVenueSortOrder([venue(1), venue(4)])).toBe(50);
    expect(nextVenueSortOrder([])).toBe(10);
  });

  it('offers enabled venues plus a disabled current one', () => {
    const list = [venue(1), venue(2, { enabled: false }), venue(3, { enabled: false })];
    expect(assignableVenues(list, 2).map((v) => v.id)).toEqual([1, 2]);
    expect(assignableVenues(list, null).map((v) => v.id)).toEqual([1]);
  });

  it('finds the default, or null', () => {
    expect(defaultVenueId([venue(1), venue(2, { is_default: true })])).toBe(2);
    expect(defaultVenueId([venue(1)])).toBeNull();
  });

  it('labels an option with its address and disabled state', () => {
    expect(venueOptionLabel(venue(1, { name: 'A', address: '1 St' }))).toBe('A, 1 St');
    expect(venueOptionLabel(venue(1, { name: 'A' }))).toBe('A');
    expect(venueOptionLabel(venue(1, { name: 'A', address: '1 St', enabled: false }))).toBe('A, 1 St (disabled)');
  });

  it('explains why a venue cannot be deleted', () => {
    expect(venueDeleteBlock(venue(1, { is_default: true }))).toContain('default');
    expect(venueDeleteBlock(venue(1, { workshop_count: 2 }))).toContain('Disable');
    expect(venueDeleteBlock(venue(1))).toBeNull();
  });
});
