// #0270: the live perspective-warped CRT screen, ported from
// prototypes/crt-live-screen (#0233). Pure TypeScript so it is unit-testable
// without a DOM (CLAUDE.md §1); the component supplies the elements.
//
// The corner calibration is the user's own hand-dragged measurement from
// #0233, as a percentage of the photograph, so it survives any resize.
export const CRT_CORNERS = {
  tl: { x: 23.03, y: 28.84 },
  tr: { x: 68.38, y: 23.03 },
  br: { x: 67.68, y: 77.51 },
  bl: { x: 23.0, y: 72.3 },
} as const;

export const CRT_SRC_W = 640;
export const CRT_SRC_H = 512;

type Pt = { x: number; y: number };

/** Gauss-Jordan with partial pivoting. Eight unknowns: the projective
 *  homography's independent coefficients (h33 is fixed at 1). */
export function solveLinear(A: number[][], b: number[]): number[] {
  const n = b.length;
  const M = A.map((row, i) => [...row, b[i]]);
  for (let c = 0; c < n; c++) {
    let p = c;
    let best = Math.abs(M[p][c]);
    for (let r = c + 1; r < n; r++) {
      const v = Math.abs(M[r][c]);
      if (v > best) { best = v; p = r; }
    }
    if (best < 1e-12) throw new Error('degenerate quadrilateral');
    if (p !== c) { const t = M[c]; M[c] = M[p]; M[p] = t; }
    const piv = M[c][c];
    for (let k = c; k <= n; k++) M[c][k] /= piv;
    for (let r = 0; r < n; r++) {
      if (r === c) continue;
      const f = M[r][c];
      for (let k = c; k <= n; k++) M[r][k] -= f * M[c][k];
    }
  }
  return M.map((r) => r[n]);
}

export function homography(src: Pt[], dst: Pt[]): number[] {
  const A: number[][] = [];
  const b: number[] = [];
  for (let i = 0; i < 4; i++) {
    const { x, y } = src[i];
    const X = dst[i].x;
    const Y = dst[i].y;
    A.push([x, y, 1, 0, 0, 0, -X * x, -X * y]); b.push(X);
    A.push([0, 0, 0, x, y, 1, -Y * x, -Y * y]); b.push(Y);
  }
  return solveLinear(A, b);
}

/** CSS matrix3d is column-major; with z=0 this yields
 *    X = (h0*x + h1*y + h2) / (h6*x + h7*y + 1)
 *    Y = (h3*x + h4*y + h5) / (h6*x + h7*y + 1) */
export function crtMatrix3d(stageW: number, stageH: number): string {
  const pct = (p: Pt) => ({ x: (stageW * p.x) / 100, y: (stageH * p.y) / 100 });
  const src = [
    { x: 0, y: 0 }, { x: CRT_SRC_W, y: 0 },
    { x: CRT_SRC_W, y: CRT_SRC_H }, { x: 0, y: CRT_SRC_H },
  ];
  const dst = [pct(CRT_CORNERS.tl), pct(CRT_CORNERS.tr), pct(CRT_CORNERS.br), pct(CRT_CORNERS.bl)];
  const h = homography(src, dst);
  const m = [h[0], h[3], 0, h[6], h[1], h[4], 0, h[7], 0, 0, 1, 0, h[2], h[5], 0, 1];
  return 'matrix3d(' + m.map((v) => Number((Math.abs(v) < 1e-12 ? 0 : v).toFixed(12))).join(',') + ')';
}

/** The session. Illustrative brand copy, not the workshops API — see #0233's
 *  recorded decision: real dates duplicate "Next up" directly below the hero
 *  and would make this element information rather than decoration.
 *
 *  #0393 moved the live session into a `crt_commands` database table (an
 *  admin-editable superset of this array, plus ten additional inactive "fun"
 *  rows) served at GET /api/crt-session. This constant does NOT go away: it
 *  is the fallback for STORAGE=json (no crt_commands-table backing), for a
 *  pre-seed deploy (the migration has run but no rows exist yet), and for
 *  any failure of that endpoint's own fetch (Home.svelte falls back to this
 *  array on a non-OK response). That means two copies of the session exist
 *  by design — this constant and the seed rows in
 *  migrations/000028_create_crt_commands.up.sql — and they are ALLOWED TO
 *  DRIFT: this constant is a floor (the worst case the screen ever shows),
 *  not a mirror of whatever an admin has since edited into the database. Do
 *  not "fix" a difference between this array and the seeded/live rows by
 *  syncing them — that is not a bug. */
export const CRT_SESSION: ReadonlyArray<{ cmd: string; out: readonly string[] }> = [
  { cmd: 'workshops --next', out: ['soldering 101 ..... sat 12:30', 'kicad from scratch  sep 18', 'esp32 + sensors ... oct 02', '3 scheduled, 12 seats open'] },
  { cmd: 'whoami', out: ['open circuit sf', 'a san francisco group that', 'builds things on tables.'] },
  { cmd: 'ls tools/', out: ['irons  multimeters  scopes', 'logic-analysers  hot-air', 'all provided. bring nothing.'] },
  { cmd: 'cat topics.txt', out: ['microcontrollers', 'soldering', 'homelab', 'home automation'] },
  { cmd: 'where --venues', out: ['makerspaces, co-working rooms,', "somebody's garage.", 'venue-independent by design.'] },
  { cmd: 'skill --required', out: ['none.', 'absolute beginners welcome.'] },
  { cmd: 'subscribe --interests', out: ['pick only what you want:', '[x] workshops  [ ] digests', '[ ] announcements', 'double opt-in. leave anytime.'] },
  { cmd: 'uptime', out: ['soldering irons hot since 2026', 'no analytics. no trackers.'] },
];

/** One step of the running session, as Home.svelte types it out and
 *  live-enriches it (#0393). `source` selects which endpoint, if any,
 *  replaces `out` at render time — see the Design §1 table in issues/0393.md:
 *  'static' (never replaced), 'workshops' (GET /api/workshops), 'list_stats'
 *  (GET /api/list-stats' confirmed/pending), 'interests' (that same
 *  response's new `interests` array, via crtInterestLines). `out` is always
 *  the fallback shown when the live fetch is skipped, fails, or returns
 *  nothing usable — #0274's rule that the screen must never degrade to a
 *  blank block or an error string applies per-row, not just to the session
 *  as a whole. */
export type CrtScriptStep = { cmd: string; out: string[]; source: string };

/** GET /api/crt-session's response shape (internal/handlers/
 *  public_crt_session.go's publicCrtSessionResponse): active rows only, in
 *  order, carrying no id/active/timestamp. */
export type CrtSessionResponse = { commands: ReadonlyArray<{ cmd: string; out: readonly string[]; source: string }> };

/** Builds a live-enrichable script from the compiled-in CRT_SESSION,
 *  tagging each step with the same `source` values
 *  migrations/000028_create_crt_commands.up.sql's seed uses for the two
 *  originally-live commands ('workshops --next' -> 'workshops',
 *  'subscribe --interests' -> 'list_stats', everything else -> 'static') —
 *  so the fallback path live-enriches identically to the database-backed
 *  session. This is the STORAGE=json / pre-seed-deploy / crt-session-fetch-
 *  failed path (#0393's Design §2). */
export function crtFallbackScript(): CrtScriptStep[] {
  return CRT_SESSION.map((c) => ({
    cmd: c.cmd,
    out: [...c.out],
    source: c.cmd === 'workshops --next' ? 'workshops' : c.cmd === 'subscribe --interests' ? 'list_stats' : 'static',
  }));
}

/** Converts a fetched GET /api/crt-session response into a running script,
 *  falling back to crtFallbackScript() when `resp` is null/undefined (the
 *  fetch failed or answered non-OK — the caller passes null in that case)
 *  or carries no commands (an empty array is itself "no usable data": a
 *  pre-seed deploy where the migration ran but nothing has been seeded
 *  into it, or every row has been deactivated). Pure and DOM-free so it is
 *  unit-testable without mounting Home.svelte. */
export function crtSessionToScript(resp: CrtSessionResponse | null | undefined): CrtScriptStep[] {
  if (!resp || !Array.isArray(resp.commands) || resp.commands.length === 0) return crtFallbackScript();
  return resp.commands.map((c) => ({ cmd: c.cmd, out: [...c.out], source: c.source }));
}

/** paint() draws only the last MAX_LINES, so pushing one past that shifts
 *  everything up — the scroll, without a scrollback buffer. */
export const CRT_MAX_LINES = 13;
export function visibleLines(lines: readonly string[]): string[] {
  return lines.slice(-CRT_MAX_LINES);
}

/** #0274: the screen now shows real data when it can. These builders take
 *  already-fetched values so they stay pure and testable without a DOM. */
export type CrtWorkshop = {
  title: string;
  starts_at: string;
  location_name?: string | null;
};

/** The glass fits roughly 36 characters at the rendered font size. Longer
 *  titles are truncated with an ellipsis rather than overflowing the tube --
 *  the screen is decorative, and a line running off the glass reads as a bug. */
export const CRT_LINE_CHARS = 36;

export function crtTruncate(text: string, max = CRT_LINE_CHARS): string {
  if (text.length <= max) return text;
  if (max <= 1) return text.slice(0, Math.max(max, 0));
  return text.slice(0, max - 1).trimEnd() + '\u2026';
}

/** "sat 12:30" style, matching the illustrative copy's register. Invalid or
 *  missing dates yield '' rather than "Invalid Date". */
export function crtShortDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  const day = d.toLocaleDateString('en-US', { weekday: 'short' }).toLowerCase();
  const mon = d.toLocaleDateString('en-US', { month: 'short' }).toLowerCase();
  return day + ' ' + mon + ' ' + d.getDate();
}

export function crtWorkshopLines(workshops: readonly CrtWorkshop[]): string[] {
  const rows = workshops.slice(0, 3).map((w) => {
    const when = crtShortDate(w.starts_at);
    return crtTruncate(when ? w.title + ' — ' + when : w.title);
  });
  if (!rows.length) return [];
  const n = workshops.length;
  rows.push(n === 1 ? '1 scheduled.' : n + ' scheduled.');
  return rows;
}

export function crtListLines(confirmed: number, pending: number): string[] {
  const lines = [confirmed === 1 ? '1 confirmed subscriber' : confirmed + ' confirmed subscribers'];
  // pending is bucketed server-side (#0274), so report it as approximate --
  // stating a rounded number as exact would be a small lie on a public page.
  if (pending > 0) lines.push('~' + pending + ' awaiting confirmation');
  lines.push('double opt-in. leave anytime.');
  return lines;
}

/** #0393: GET /api/list-stats' new `interests` array -- one entry per
 *  interest with at least one active subscriber, already ordered by count
 *  descending then sort_order (see internal/subscribers.Store.
 *  ActiveInterestCounts). `count` is exact, not bucketed -- see that
 *  method's doc comment for why that is safe. */
export type CrtInterestCount = { slug: string; name: string; count: number };

/** A "label ...... value" leader line, truncated defensively to `width` if
 *  an unusually long label would otherwise overflow it. Shared by
 *  crtInterestLines below; kept private since nothing else needs a dotted
 *  leader yet. */
function dottedLine(label: string, value: string, width: number = CRT_LINE_CHARS): string {
  const left = label + ' ';
  const right = ' ' + value;
  const dotsLen = Math.max(width - left.length - right.length, 1);
  return crtTruncate(left + '.'.repeat(dotsLen) + right, width);
}

/** #0393: renders the top three interests by active-subscriber count as
 *  dotted-leader lines (`microcontrollers ...... 4`), plus a trailing
 *  singular/plural summary of how many interests have at least one
 *  subscriber -- matching crtWorkshopLines' own "N scheduled." convention.
 *  Empty input yields an empty result, the same "no data -> caller keeps
 *  the stored fallback" contract crtWorkshopLines already has (#0274's
 *  rule: the screen must never degrade to a blank block). */
export function crtInterestLines(counts: readonly CrtInterestCount[]): string[] {
  if (!counts.length) return [];
  const lines = counts.slice(0, 3).map((c) => dottedLine(c.slug, String(c.count)));
  const n = counts.length;
  lines.push(n === 1 ? '1 topic has subscribers.' : n + ' topics have subscribers.');
  return lines;
}

/** Whatever live data Home.svelte managed to fetch for one script step's
 *  `source`, or nothing for a field whose own fetch failed/was skipped —
 *  see crtEnrichStep below. */
export type CrtLiveData = {
  workshops?: readonly CrtWorkshop[];
  listStats?: { confirmed?: number; pending?: number };
  interests?: readonly CrtInterestCount[];
};

/**
 * The per-row half of #0274's rule, generalised to sourced script steps
 * (#0393): given one step and whatever live data Home.svelte fetched (or
 * `undefined` when that fetch failed, threw, or was never attempted),
 * returns the step enriched with live lines when there is something usable
 * for its `source`, or the ORIGINAL step, UNCHANGED, otherwise — so a
 * failed fetch, an empty live result (e.g. zero upcoming workshops), or a
 * 'static' step all fall through to the stored fallback `out` rather than a
 * blank block or an error string. Pure, so the "keeps the stored fallback
 * on failure" claim is a real assertion here, not just an argument about
 * Home.svelte's try/catch shape. */
export function crtEnrichStep(step: CrtScriptStep, live: CrtLiveData | undefined): CrtScriptStep {
  if (!live) return step;
  switch (step.source) {
    case 'workshops': {
      if (!live.workshops) return step;
      const lines = crtWorkshopLines(live.workshops);
      return lines.length ? { ...step, out: lines } : step;
    }
    case 'list_stats': {
      if (!live.listStats || typeof live.listStats.confirmed !== 'number') return step;
      return { ...step, out: crtListLines(live.listStats.confirmed, live.listStats.pending ?? 0) };
    }
    case 'interests': {
      if (!live.interests) return step;
      const lines = crtInterestLines(live.interests);
      return lines.length ? { ...step, out: lines } : step;
    }
    default:
      // 'static' and any unrecognised future source: never replaced.
      return step;
  }
}

/** #0393 review bounce: the fetch-and-fallback orchestration that used to
 *  live in Home.svelte's onMount as loadSession()/loadLiveData(), moved here
 *  verbatim (CLAUDE.md §1 -- SPA logic in lib/, components stay thin) so the
 *  acceptance criteria "with GET /api/crt-session returning 404 or 500 the
 *  home CRT still runs the compiled-in CRT_SESSION" and "a live-source row's
 *  own fetch failing shows its stored fallback lines" are unit-tested
 *  against the real orchestration rather than only against the pure helpers
 *  it calls. Behaviour is unchanged: one request to /api/crt-session, then
 *  (only if a resulting step needs it) one request each to /api/workshops
 *  and /api/list-stats, all under the same try/catch-then-fallback shape as
 *  before. `f` defaults to the global `fetch` and is overridden by tests with
 *  a stub -- see crtScreen.test.ts. */
export async function crtLoadScript(f: typeof fetch = fetch): Promise<CrtScriptStep[]> {
  let script: CrtScriptStep[];
  try {
    const res = await f('/api/crt-session', { headers: { accept: 'application/json' } });
    const body = res.ok ? ((await res.json()) as CrtSessionResponse) : null;
    script = crtSessionToScript(body);
  } catch {
    script = crtFallbackScript();
  }

  // #0274's rule generalised per-row (#0393): a live-source row's own fetch
  // failing leaves ITS stored `out` untouched. The DECISION of whether/how to
  // replace a row's `out` is crtEnrichStep above -- this function's only job
  // is fetching: one request per endpoint, not a poll (both are cached
  // server-side for 60s anyway), passed to crtEnrichStep as `undefined` when
  // the fetch failed, threw, or no row needs it, which crtEnrichStep always
  // treats as "keep the stored fallback".
  const needsWorkshops = script.some((s) => s.source === 'workshops');
  const needsListStats = script.some((s) => s.source === 'list_stats' || s.source === 'interests');

  let workshops: CrtWorkshop[] | undefined;
  if (needsWorkshops) {
    try {
      const res = await f('/api/workshops', { headers: { accept: 'application/json' } });
      if (res.ok) {
        const body = (await res.json()) as { upcoming?: CrtWorkshop[] };
        workshops = body.upcoming ?? [];
      }
    } catch {
      // workshops stays undefined -- crtEnrichStep keeps the stored fallback
    }
  }

  let listStats: { confirmed?: number; pending?: number } | undefined;
  let interests: CrtInterestCount[] | undefined;
  if (needsListStats) {
    try {
      const res = await f('/api/list-stats', { headers: { accept: 'application/json' } });
      if (res.ok) {
        const body = (await res.json()) as { confirmed?: number; pending?: number; interests?: CrtInterestCount[] };
        listStats = { confirmed: body.confirmed, pending: body.pending };
        interests = body.interests;
      }
    } catch {
      // listStats/interests stay undefined -- crtEnrichStep keeps the stored fallback
    }
  }

  return script.map((row) => crtEnrichStep(row, { workshops, listStats, interests }));
}
