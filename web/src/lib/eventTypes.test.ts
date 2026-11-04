import { describe, expect, it } from 'vitest';
import {
  assignableEventTypes,
  defaultEventTypeId,
  eventTypeDeleteBlock,
  eventTypeOptionLabel,
  nextEventTypeSortOrder,
  sortedEventTypes,
  validateNewEventType,
} from './eventTypes';
import type { EventType } from './types';

function type(id: number, over: Partial<EventType> = {}): EventType {
  return {
    id,
    slug: `type-${id}`,
    name: `Type ${id}`,
    enabled: true,
    is_default: false,
    sort_order: id * 10,
    workshop_count: 0,
    created_at: '2026-11-04T00:00:00Z',
    ...over,
  };
}

describe('eventTypes', () => {
  it('sorts by sort_order then name', () => {
    const got = sortedEventTypes([
      type(1, { sort_order: 20, name: 'B' }),
      type(2, { sort_order: 20, name: 'A' }),
      type(3, { sort_order: 10 }),
    ]);
    expect(got.map((t) => t.id)).toEqual([3, 2, 1]);
  });

  it('rejects a bad slug and an empty name', () => {
    expect(validateNewEventType('Not A Slug', 'X')).toHaveProperty('error');
    expect(validateNewEventType('fine', '  ')).toHaveProperty('error');
    expect(validateNewEventType(' hack-night ', ' Hack Night ')).toEqual({
      slug: 'hack-night',
      name: 'Hack Night',
    });
  });

  it('offers enabled types plus the current one', () => {
    const list = [type(1), type(2, { enabled: false }), type(3, { enabled: false })];
    expect(assignableEventTypes(list, 2).map((t) => t.id)).toEqual([1, 2]);
    expect(assignableEventTypes(list, null).map((t) => t.id)).toEqual([1]);
  });

  it('finds the default type', () => {
    expect(defaultEventTypeId([type(1), type(2, { is_default: true })])).toBe(2);
    expect(defaultEventTypeId([type(1)])).toBeNull();
  });

  it('labels a disabled option', () => {
    expect(eventTypeOptionLabel(type(1, { name: 'Talk' }))).toBe('Talk');
    expect(eventTypeOptionLabel(type(1, { name: 'Talk', enabled: false }))).toBe('Talk (disabled)');
  });

  it('names why a type cannot be deleted', () => {
    expect(eventTypeDeleteBlock(type(1, { is_default: true }))).toMatch(/another default/);
    expect(eventTypeDeleteBlock(type(1, { workshop_count: 2 }))).toMatch(/Disable it instead/);
    expect(eventTypeDeleteBlock(type(1))).toBeNull();
  });

  it('appends after the highest sort_order', () => {
    expect(nextEventTypeSortOrder([type(1, { sort_order: 50 }), type(2, { sort_order: 20 })])).toBe(60);
    expect(nextEventTypeSortOrder([])).toBe(10);
  });
});
