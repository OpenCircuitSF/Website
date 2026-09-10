import { describe, it, expect } from 'vitest';
import {
  isValidCrtSlug,
  isValidCrtSource,
  validateNewCrtCommand,
  sortedCrtCommands,
  crtReorderSwap,
  crtSourceLabel,
  CRT_SOURCES,
} from './crtCommands';
import type { CrtCommand } from './types';

function makeCommand(overrides: Partial<CrtCommand> = {}): CrtCommand {
  return {
    id: 1,
    slug: 'whoami',
    command: 'whoami',
    output: 'open circuit sf',
    source: 'static',
    sort_order: 10,
    active: true,
    created_at: '2026-09-03T00:00:00Z',
    ...overrides,
  };
}

describe('isValidCrtSlug', () => {
  it('accepts lowercase hyphenated slugs', () => {
    expect(isValidCrtSlug('workshops-next')).toBe(true);
    expect(isValidCrtSlug('uptime')).toBe(true);
  });

  it('rejects malformed slugs', () => {
    for (const bad of ['Upper-Case', 'has_underscore', 'trailing-', '-leading', 'double--hyphen', '']) {
      expect(isValidCrtSlug(bad)).toBe(false);
    }
  });
});

describe('isValidCrtSource', () => {
  it('accepts exactly the four backend-recognised sources', () => {
    for (const s of CRT_SOURCES) expect(isValidCrtSource(s.value)).toBe(true);
    expect(CRT_SOURCES.map((s) => s.value).sort()).toEqual(
      ['interests', 'list_stats', 'static', 'workshops'].sort(),
    );
  });

  it('rejects anything else', () => {
    for (const bad of ['', 'STATIC', 'bogus', 'workshop']) {
      expect(isValidCrtSource(bad)).toBe(false);
    }
  });
});

describe('validateNewCrtCommand', () => {
  it('trims and accepts a well-formed submission', () => {
    const result = validateNewCrtCommand('  fortune  ', ' fortune ', ' solder flows toward heat. ');
    expect(result).toEqual({ slug: 'fortune', command: 'fortune', output: 'solder flows toward heat.' });
  });

  it('rejects an invalid slug', () => {
    const result = validateNewCrtCommand('Bad Slug', 'x', 'y');
    expect('error' in result).toBe(true);
  });

  it('rejects an empty command', () => {
    const result = validateNewCrtCommand('valid-slug', '   ', 'y');
    expect(result).toEqual({ error: 'Command is required.' });
  });

  it('rejects an empty output', () => {
    const result = validateNewCrtCommand('valid-slug', 'x', '   ');
    expect(result).toEqual({ error: 'Output is required.' });
  });
});

describe('sortedCrtCommands', () => {
  it('orders by sort_order then slug', () => {
    const list = [
      makeCommand({ id: 1, slug: 'zzz', sort_order: 10 }),
      makeCommand({ id: 2, slug: 'aaa', sort_order: 10 }),
      makeCommand({ id: 3, slug: 'mid', sort_order: 5 }),
    ];
    const sorted = sortedCrtCommands(list);
    expect(sorted.map((c) => c.id)).toEqual([3, 2, 1]);
  });

  it('does not mutate the input array', () => {
    const list = [makeCommand({ id: 1, sort_order: 20 }), makeCommand({ id: 2, sort_order: 10 })];
    const original = [...list];
    sortedCrtCommands(list);
    expect(list).toEqual(original);
  });
});

describe('crtReorderSwap', () => {
  const sorted = [
    makeCommand({ id: 1, slug: 'a', sort_order: 10 }),
    makeCommand({ id: 2, slug: 'b', sort_order: 20 }),
    makeCommand({ id: 3, slug: 'c', sort_order: 30 }),
  ];

  it('swaps sort_order with the previous row when moving up', () => {
    const swap = crtReorderSwap(sorted, 2, 'up');
    expect(swap).toEqual({
      moved: { id: 2, sortOrder: 10 },
      other: { id: 1, sortOrder: 20 },
    });
  });

  it('swaps sort_order with the next row when moving down', () => {
    const swap = crtReorderSwap(sorted, 2, 'down');
    expect(swap).toEqual({
      moved: { id: 2, sortOrder: 30 },
      other: { id: 3, sortOrder: 20 },
    });
  });

  it('returns null at the top boundary', () => {
    expect(crtReorderSwap(sorted, 1, 'up')).toBeNull();
  });

  it('returns null at the bottom boundary', () => {
    expect(crtReorderSwap(sorted, 3, 'down')).toBeNull();
  });

  it('returns null for an unknown id', () => {
    expect(crtReorderSwap(sorted, 999, 'up')).toBeNull();
  });
});

describe('crtSourceLabel', () => {
  it('returns the labelled source for a known value', () => {
    expect(crtSourceLabel('workshops')).toContain('workshops');
  });

  it('falls back to the raw value for an unknown source', () => {
    expect(crtSourceLabel('mystery')).toBe('mystery');
  });
});
