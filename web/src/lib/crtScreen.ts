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
 *  and would make this element information rather than decoration. */
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
