import { describe, it, expect } from 'vitest';
import {
  isValidCrtSlug,
  isValidCrtSource,
  validateNewCrtCommand,
  sortedCrtCommands,
  crtReorderSwap,
  crtSourceLabel,
  crtOverBudgetLines,
  crtOverBudgetWarning,
  CRT_SOURCES,
  CRT_LINE_CHARS,
  CRT_COMMAND_LINE_CHARS,
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

// #0393 review-bounce follow-up: the admin CRT editor gave no line-width
// guidance and nothing warned about a stored line past the 36-char budget.
describe('crtOverBudgetLines', () => {
  it('returns nothing when every line fits', () => {
    expect(crtOverBudgetLines('short line\nanother short one')).toEqual([]);
  });

  it('reports the 1-based line number of an over-budget line', () => {
    const long = 'x'.repeat(CRT_LINE_CHARS + 1);
    expect(crtOverBudgetLines('fits\n' + long + '\nfits too')).toEqual([2]);
  });

  it('reports every over-budget line, in order', () => {
    const long = 'x'.repeat(CRT_LINE_CHARS + 5);
    expect(crtOverBudgetLines(long + '\nfits\n' + long)).toEqual([1, 3]);
  });

  it('treats a line at exactly the budget as fitting', () => {
    const exact = 'x'.repeat(CRT_LINE_CHARS);
    expect(crtOverBudgetLines(exact)).toEqual([]);
  });

  it('ignores trailing empty lines', () => {
    expect(crtOverBudgetLines('fits\n\n\n')).toEqual([]);
  });

  it('does not ignore a trailing over-budget line, only trailing empties', () => {
    const long = 'x'.repeat(CRT_LINE_CHARS + 1);
    expect(crtOverBudgetLines('fits\n' + long)).toEqual([2]);
  });
});

describe('crtOverBudgetWarning', () => {
  it('returns null when every line fits', () => {
    expect(crtOverBudgetWarning('short line')).toBeNull();
  });

  it('names the single over-budget line, singular', () => {
    const long = 'x'.repeat(CRT_LINE_CHARS + 1);
    const warning = crtOverBudgetWarning(long);
    expect(warning).toContain('Line 1 is');
    expect(warning).toContain(String(CRT_LINE_CHARS));
  });

  it('names multiple over-budget lines, plural', () => {
    const long = 'x'.repeat(CRT_LINE_CHARS + 1);
    const warning = crtOverBudgetWarning(long + '\nfits\n' + long);
    expect(warning).toContain('Lines 1, 3 are');
  });
});

// #0397: CrtCommands.svelte calls crtOverBudgetWarning/crtOverBudgetLines on
// the single-line command field too, passing CRT_COMMAND_LINE_CHARS as `max`
// -- two characters narrower than CRT_LINE_CHARS -- since the command is
// drawn with a "> " prompt prefix at render time (crtScreen.ts's
// crtCommandLine) and so has two characters less room than a plain output
// line. These pin the boundary these callers actually rely on.
describe('crtOverBudgetLines/crtOverBudgetWarning against the command budget (#0397)', () => {
  it('treats a command of exactly CRT_COMMAND_LINE_CHARS as fitting', () => {
    const exact = 'x'.repeat(CRT_COMMAND_LINE_CHARS);
    expect(crtOverBudgetLines(exact, CRT_COMMAND_LINE_CHARS)).toEqual([]);
    expect(crtOverBudgetWarning(exact, CRT_COMMAND_LINE_CHARS)).toBeNull();
  });

  it('flags a command one character over CRT_COMMAND_LINE_CHARS', () => {
    const oneOver = 'x'.repeat(CRT_COMMAND_LINE_CHARS + 1);
    expect(crtOverBudgetLines(oneOver, CRT_COMMAND_LINE_CHARS)).toEqual([1]);
    const warning = crtOverBudgetWarning(oneOver, CRT_COMMAND_LINE_CHARS);
    expect(warning).toContain('Line 1 is');
    expect(warning).toContain(String(CRT_COMMAND_LINE_CHARS));
  });

  it('a command that fits the command budget but not the full output budget is still flagged, proving the caller cannot fall back to CRT_LINE_CHARS by mistake', () => {
    // CRT_COMMAND_LINE_CHARS < CRT_LINE_CHARS, so a command sized to be
    // exactly one over the SMALLER budget is still comfortably under the
    // larger one -- if a caller passed CRT_LINE_CHARS by accident (the
    // literal #0397 defect, applied to the warning instead of the draw
    // sites), this line would wrongly read as fitting.
    const commandOnlyOver = 'x'.repeat(CRT_COMMAND_LINE_CHARS + 1);
    expect(commandOnlyOver.length).toBeLessThan(CRT_LINE_CHARS);
    expect(crtOverBudgetLines(commandOnlyOver, CRT_LINE_CHARS)).toEqual([]);
    expect(crtOverBudgetLines(commandOnlyOver, CRT_COMMAND_LINE_CHARS)).toEqual([1]);
  });
});
