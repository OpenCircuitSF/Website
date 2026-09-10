import { describe, it, expect } from 'vitest';
import {
  crtTruncate,
  crtShortDate,
  crtWorkshopLines,
  crtListLines,
  crtInterestLines,
  crtFallbackScript,
  crtSessionToScript,
  crtEnrichStep,
  CRT_LINE_CHARS,
  CRT_SESSION,
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

describe('crtInterestLines', () => {
  // Empty means "no data", and the caller falls back to the stored row
  // rather than printing an empty block (#0274, matching crtWorkshopLines).
  it('returns nothing for an empty array', () => {
    expect(crtInterestLines([])).toEqual([]);
  });

  it('shows at most three interests plus a summary line', () => {
    const counts = [
      { slug: 'microcontrollers', name: 'Microcontrollers', count: 4 },
      { slug: 'soldering', name: 'Soldering', count: 3 },
      { slug: 'homelab', name: 'Homelab', count: 1 },
      { slug: 'pcb-design', name: 'PCB Design', count: 1 },
    ];
    const lines = crtInterestLines(counts);
    expect(lines).toHaveLength(4); // 3 rows + summary
    expect(lines[0]).toContain('microcontrollers');
    expect(lines[0]).toContain('4');
    expect(lines[3]).toBe('4 topics have subscribers.');
  });

  it('singularises a lone interest', () => {
    const lines = crtInterestLines([{ slug: 'microcontrollers', name: 'Microcontrollers', count: 4 }]);
    expect(lines[lines.length - 1]).toBe('1 topic has subscribers.');
  });

  it('pluralises more than one interest', () => {
    const lines = crtInterestLines([
      { slug: 'microcontrollers', name: 'Microcontrollers', count: 4 },
      { slug: 'soldering', name: 'Soldering', count: 3 },
    ]);
    expect(lines[lines.length - 1]).toBe('2 topics have subscribers.');
  });

  it('keeps every line within the glass width, even for a long slug', () => {
    const lines = crtInterestLines([
      { slug: 'test-equipment-and-measurement-tools', name: 'Test Equipment', count: 12345 },
    ]);
    for (const l of lines) expect(l.length).toBeLessThanOrEqual(CRT_LINE_CHARS);
  });
});

describe('crtFallbackScript', () => {
  it('tags workshops --next and subscribe --interests with their live sources', () => {
    const script = crtFallbackScript();
    expect(script).toHaveLength(CRT_SESSION.length);
    const workshops = script.find((s) => s.cmd === 'workshops --next');
    const subscribe = script.find((s) => s.cmd === 'subscribe --interests');
    expect(workshops?.source).toBe('workshops');
    expect(subscribe?.source).toBe('list_stats');
  });

  it('tags every other command static', () => {
    const script = crtFallbackScript();
    const others = script.filter((s) => s.cmd !== 'workshops --next' && s.cmd !== 'subscribe --interests');
    expect(others.length).toBeGreaterThan(0);
    for (const s of others) expect(s.source).toBe('static');
  });

  it('returns copies, not references, of CRT_SESSION.out', () => {
    const script = crtFallbackScript();
    script[0].out.push('mutated');
    expect(CRT_SESSION[0].out).not.toContain('mutated');
  });
});

describe('crtSessionToScript', () => {
  // #0393 acceptance criteria: a null/undefined response (the fetch failed
  // or answered non-OK, per the caller's contract) or an empty commands
  // array must fall back to the compiled-in session, never a blank screen.
  it('falls back to crtFallbackScript for null', () => {
    expect(crtSessionToScript(null)).toEqual(crtFallbackScript());
  });

  it('falls back to crtFallbackScript for undefined', () => {
    expect(crtSessionToScript(undefined)).toEqual(crtFallbackScript());
  });

  it('falls back to crtFallbackScript for an empty commands array', () => {
    expect(crtSessionToScript({ commands: [] })).toEqual(crtFallbackScript());
  });

  it('maps a real response through, preserving order and source', () => {
    const resp = {
      commands: [
        { cmd: 'whoami', out: ['open circuit sf'], source: 'static' },
        { cmd: 'workshops --next', out: ['fallback line'], source: 'workshops' },
      ],
    };
    const script = crtSessionToScript(resp);
    expect(script).toEqual([
      { cmd: 'whoami', out: ['open circuit sf'], source: 'static' },
      { cmd: 'workshops --next', out: ['fallback line'], source: 'workshops' },
    ]);
  });
});

describe('crtEnrichStep', () => {
  // #0393 acceptance criteria: "With a live-source row's own fetch failing,
  // that row shows its stored fallback lines rather than a blank block or
  // an error string" -- this is the pure decision Home.svelte's fetch
  // orchestration defers to, so the claim is a real assertion here.
  it('keeps the stored fallback when live is undefined (the fetch failed/threw)', () => {
    const step = { cmd: 'workshops --next', out: ['fallback line'], source: 'workshops' };
    expect(crtEnrichStep(step, undefined)).toBe(step);
  });

  it('keeps the stored fallback when the matching live field is absent', () => {
    const step = { cmd: 'workshops --next', out: ['fallback line'], source: 'workshops' };
    expect(crtEnrichStep(step, { listStats: { confirmed: 5 } })).toBe(step);
  });

  it('keeps the stored fallback when the live workshops list is empty', () => {
    const step = { cmd: 'workshops --next', out: ['fallback line'], source: 'workshops' };
    expect(crtEnrichStep(step, { workshops: [] })).toBe(step);
  });

  it('replaces out with live workshop lines when available', () => {
    const step = { cmd: 'workshops --next', out: ['fallback line'], source: 'workshops' };
    const enriched = crtEnrichStep(step, {
      workshops: [{ title: 'Soldering 101', starts_at: '2026-09-04T01:30:00Z' }],
    });
    expect(enriched.out).not.toEqual(['fallback line']);
    expect(enriched.cmd).toBe('workshops --next');
    expect(enriched.source).toBe('workshops');
  });

  it('replaces out with live list_stats lines when confirmed is a number', () => {
    const step = { cmd: 'subscribe --interests', out: ['fallback line'], source: 'list_stats' };
    const enriched = crtEnrichStep(step, { listStats: { confirmed: 3, pending: 0 } });
    expect(enriched.out[0]).toBe('3 confirmed subscribers');
  });

  it('keeps the stored fallback when list_stats has no confirmed field', () => {
    const step = { cmd: 'subscribe --interests', out: ['fallback line'], source: 'list_stats' };
    expect(crtEnrichStep(step, { listStats: {} })).toBe(step);
  });

  it('replaces out with live interest lines when available', () => {
    const step = { cmd: 'topics --subscribers', out: ['fallback line'], source: 'interests' };
    const enriched = crtEnrichStep(step, {
      interests: [{ slug: 'microcontrollers', name: 'Microcontrollers', count: 4 }],
    });
    expect(enriched.out).not.toEqual(['fallback line']);
  });

  it('keeps the stored fallback when the live interests array is empty', () => {
    const step = { cmd: 'topics --subscribers', out: ['fallback line'], source: 'interests' };
    expect(crtEnrichStep(step, { interests: [] })).toBe(step);
  });

  it('never replaces a static step, even with live data present', () => {
    const step = { cmd: 'whoami', out: ['stored'], source: 'static' };
    const enriched = crtEnrichStep(step, {
      workshops: [{ title: 'X', starts_at: '2026-09-04T01:30:00Z' }],
      listStats: { confirmed: 99 },
      interests: [{ slug: 'x', name: 'X', count: 1 }],
    });
    expect(enriched).toBe(step);
  });
});
