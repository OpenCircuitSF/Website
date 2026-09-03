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
  crtLoadScript,
  CRT_LINE_CHARS,
  CRT_SESSION,
} from './crtScreen';

/** A minimal stand-in for the fetch Response type: crtLoadScript only ever
 *  reads `.ok` and calls `.json()`, so nothing else needs to be modelled. */
type StubResponse = { ok: boolean; status?: number; json: () => Promise<unknown> };

function okJson(body: unknown): StubResponse {
  return { ok: true, status: 200, json: async () => body };
}

function failResponse(status: number): StubResponse {
  return { ok: false, status, json: async () => ({}) };
}

/** Builds a stub `fetch` keyed by URL, per #0393's review-bounce remedy
 *  ("inject a stub fetch"). `calls` (if passed) records every URL requested,
 *  in order -- used to pin the needsWorkshops/needsListStats short-circuits.
 *  A URL with no handler throws loudly rather than hanging, so a test that
 *  forgot to stub an endpoint crtLoadScript actually calls fails clearly
 *  instead of silently resolving `undefined`. */
function makeFetch(
  handlers: Record<string, () => StubResponse | Promise<StubResponse> | never>,
  calls: string[] = [],
): typeof fetch {
  return (async (input: RequestInfo | URL) => {
    const url = String(input);
    calls.push(url);
    const handler = handlers[url];
    if (!handler) throw new Error('crtScreen.test.ts stub fetch: unexpected URL ' + url);
    return (await handler()) as unknown as Response;
  }) as typeof fetch;
}

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

// #0393 review-bounce follow-up: crtTruncate is now applied at render time in
// Home.svelte as a safety net for admin-entered static output. This proves
// the safety net does not silently start trimming today's real copy: every
// line of every one of the eight EXISTING ACTIVE seed rows -- which the
// #0393 review verified match CRT_SESSION byte-for-byte -- passes through
// crtTruncate unchanged. If a future edit to CRT_SESSION (or the seed) pushes
// a line over CRT_LINE_CHARS, this fails loudly rather than the safety net
// quietly starting to clip real copy.
describe('crtTruncate is a no-op on the seeded active CRT_SESSION rows', () => {
  it('leaves every line of every CRT_SESSION command unchanged', () => {
    expect(CRT_SESSION.length).toBeGreaterThan(0);
    for (const { out } of CRT_SESSION) {
      for (const line of out) {
        expect(crtTruncate(line)).toBe(line);
      }
    }
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

// #0393 review bounce: crtLoadScript is the orchestration Home.svelte's
// onMount used to inline (loadSession()/loadLiveData()), extracted so the
// two untested acceptance criteria -- "with GET /api/crt-session returning
// 404 or 500 the home CRT still runs the compiled-in CRT_SESSION" and "a
// live-source row's own fetch failing shows its stored fallback lines" --
// are real assertions against the fetch/status-code decision itself, not
// just against the pure helpers it calls. No jsdom pragma: a stub `fetch` is
// injected directly, exactly as Home.svelte's onMount will call the real
// one (default parameter).
describe('crtLoadScript', () => {
  it('with /api/crt-session returning 404, still runs the compiled-in CRT_SESSION', async () => {
    const f = makeFetch({
      '/api/crt-session': () => failResponse(404),
      // needsWorkshops/needsListStats both come out true once the fallback
      // engages (CRT_SESSION carries both live sources), so both live
      // endpoints get one request too -- stub them failing as well so the
      // result is crtFallbackScript() unmodified, not "crtFallbackScript()
      // modulo live data neither of us provided".
      '/api/workshops': () => failResponse(500),
      '/api/list-stats': () => failResponse(500),
    });
    const script = await crtLoadScript(f);
    expect(script).toEqual(crtFallbackScript());
  });

  it('with /api/crt-session returning 500, still runs the compiled-in CRT_SESSION', async () => {
    const f = makeFetch({
      '/api/crt-session': () => failResponse(500),
      '/api/workshops': () => failResponse(500),
      '/api/list-stats': () => failResponse(500),
    });
    const script = await crtLoadScript(f);
    expect(script).toEqual(crtFallbackScript());
  });

  it('with /api/crt-session throwing, still runs the compiled-in CRT_SESSION', async () => {
    const f = makeFetch({
      '/api/crt-session': () => {
        throw new Error('network down');
      },
      '/api/workshops': () => failResponse(500),
      '/api/list-stats': () => failResponse(500),
    });
    const script = await crtLoadScript(f);
    expect(script).toEqual(crtFallbackScript());
  });

  it('with /api/crt-session returning an empty commands array, still runs the compiled-in CRT_SESSION', async () => {
    const f = makeFetch({
      '/api/crt-session': () => okJson({ commands: [] }),
      '/api/workshops': () => failResponse(500),
      '/api/list-stats': () => failResponse(500),
    });
    const script = await crtLoadScript(f);
    expect(script).toEqual(crtFallbackScript());
  });

  it('when a live-source row\'s own fetch fails, that row shows its stored fallback lines byte-identically, and other rows are untouched', async () => {
    const sessionResp = {
      commands: [
        { cmd: 'workshops --next', out: ['stored ws line one', 'stored ws line two'], source: 'workshops' },
        { cmd: 'whoami', out: ['stored static line'], source: 'static' },
      ],
    };
    const f = makeFetch({
      '/api/crt-session': () => okJson(sessionResp),
      // no 'list_stats'/'interests' row above, so /api/list-stats is never
      // requested and deliberately has no stub -- see the short-circuit test
      // below for a direct assertion of that.
      '/api/workshops': () => failResponse(500),
    });
    const script = await crtLoadScript(f);
    expect(script[0].out).toEqual(['stored ws line one', 'stored ws line two']);
    expect(script[1].out).toEqual(['stored static line']);
  });

  it('does not fetch /api/workshops when no step needs it (the needsWorkshops short-circuit)', async () => {
    const calls: string[] = [];
    const sessionResp = {
      commands: [
        { cmd: 'whoami', out: ['stored static line'], source: 'static' },
        { cmd: 'subscribe --interests', out: ['stored list line'], source: 'list_stats' },
      ],
    };
    const f = makeFetch(
      {
        '/api/crt-session': () => okJson(sessionResp),
        '/api/list-stats': () => okJson({ confirmed: 3, pending: 0 }),
      },
      calls,
    );
    const script = await crtLoadScript(f);
    expect(calls).not.toContain('/api/workshops');
    expect(calls).toContain('/api/list-stats');
    // and the fetch that DID happen actually enriched its row, proving this
    // isn't passing merely because nothing ran at all
    expect(script[1].out[0]).toBe('3 confirmed subscribers');
    expect(script[0].out).toEqual(['stored static line']);
  });
});
