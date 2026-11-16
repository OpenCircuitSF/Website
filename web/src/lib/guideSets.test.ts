import { describe, expect, it } from 'vitest';
import { GUIDE_SETS, TOPIC_GUIDES, WORKSHOP_GUIDES } from './guides';
import { ALL, countsBySet, groupBySet, hrefForSet, setFromQuery } from './guideSets';

describe('guide sets', () => {
  it('every guide belongs to a known set, and every set has a topic', () => {
    const ids = GUIDE_SETS.map((s) => s.id);
    expect(ids).toEqual(['leds', 'esp32', 'multimeter', 'soldering']);
    for (const g of [...TOPIC_GUIDES, ...WORKSHOP_GUIDES]) expect(ids, g.slug).toContain(g.set);
    for (const id of ids) expect(TOPIC_GUIDES.some((t) => t.set === id), id).toBe(true);
  });

  it('reads the filter from the query, defaulting to all', () => {
    expect(setFromQuery('soldering')).toBe('soldering');
    expect(setFromQuery(null)).toBe(ALL);
    expect(setFromQuery('nope')).toBe(ALL);
    expect(hrefForSet(ALL)).toBe('/guides');
    expect(hrefForSet('multimeter')).toBe('/guides?set=multimeter');
  });

  it('groups in catalog order and filters to one set', () => {
    expect(groupBySet(TOPIC_GUIDES, ALL).map((g) => g.set.id)).toEqual(['leds', 'esp32', 'multimeter', 'soldering']);
    const only = groupBySet(TOPIC_GUIDES, 'multimeter');
    expect(only).toHaveLength(1);
    expect(only[0].guides.every((g) => g.slug.startsWith('multimeter-'))).toBe(true);
    const c = countsBySet(TOPIC_GUIDES);
    expect(c[ALL]).toBe(TOPIC_GUIDES.length);
    expect(c.leds + c.esp32 + c.multimeter + c.soldering).toBe(TOPIC_GUIDES.length);
  });
});
