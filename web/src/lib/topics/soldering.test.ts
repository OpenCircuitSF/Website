import { readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import { renderTemps } from '../temperature';
import {
  ALLOYS,
  ART_BASE,
  JOINT_ART,
  SPOOL_ART,
  TINNING_ART,
  HEADER_PINS,
  HEADER_STEPS,
  JOINT_TYPES,
  NEW_TIP,
  QUIZ_ORDER,
  TIP_ACTIONS,
  alloyById,
  art,
  diameterAdvice,
  jointAt,
  tinningResult,
  tipLook,
  tipStep,
  tipWets,
  type TipState,
} from './soldering';

describe('solder alloys', () => {
  it('63/37 is eutectic at 183 °C and SAC305 melts at 217–220 °C', () => {
    expect(alloyById('sn63').meltC).toEqual([183, 183]);
    expect(alloyById('sac305').meltC).toEqual([217, 220]);
  });

  it('lead-free wants a hotter iron than leaded', () => {
    expect(alloyById('sac305').ironC![0]).toBeGreaterThan(alloyById('sn63').ironC![0]);
  });

  it('plumbing solder is the only one not for electronics', () => {
    expect(ALLOYS.filter((a) => !a.forElectronics).map((a) => a.id)).toEqual(['plumbing']);
  });

  it('0.6–0.8 mm is the right diameter; 1.6 mm is not', () => {
    expect(diameterAdvice(0.8).fit).toBe('good');
    expect(diameterAdvice(0.6).fit).toBe('good');
    expect(diameterAdvice(1.6).fit).toBe('poor');
    expect(diameterAdvice(0.5).fit).toBe('ok');
  });
});

describe('iron temperature', () => {
  it('340 °C with a chisel tip makes a good leaded joint in 2–3 s', () => {
    const r = jointAt(340, 'sn63', 'chisel');
    expect(r.kind).toBe('good');
    expect(r.seconds).toBeGreaterThanOrEqual(2);
    expect(r.seconds).toBeLessThanOrEqual(3);
  });

  it('the same dial is too cool for lead-free; 365 °C is right', () => {
    expect(jointAt(340, 'sac305', 'chisel').kind).toBe('slow');
    expect(jointAt(365, 'sac305', 'chisel').kind).toBe('good');
  });

  it('runs from no-melt through cold to scorch as the dial rises', () => {
    expect(jointAt(190, 'sn63', 'chisel').kind).toBe('no-melt');
    expect(jointAt(190, 'sn63', 'chisel').seconds).toBeNull();
    expect(jointAt(250, 'sn63', 'chisel').kind).toBe('cold');
    expect(jointAt(380, 'sn63', 'chisel').kind).toBe('hot');
    expect(jointAt(450, 'sn63', 'chisel').kind).toBe('scorch');
  });

  it('no-melt text follows the unit', () => {
    const f = jointAt(150, 'sn63', 'chisel').body;
    expect(f).toContain('about 280 °F');
    expect(f).toContain('well over 361 °F');
    const c = jointAt(150, 'sn63', 'chisel', 'C').body;
    expect(c).toContain('about 135 °C');
    expect(c).toContain('well over 183 °C');
  });

  it('every alloy verdict renders without leftover tokens', () => {
    for (const a of ALLOYS)
      for (const u of ['F', 'C'] as const) expect(renderTemps(a.verdict, u)).not.toContain('{temp');
    expect(renderTemps(ALLOYS.find((a) => a.id === 'sn60')!.verdict, 'F')).toContain('Between 361 °F and 374 °F');
  });

  it('a too-hot joint is never described as a cold one', () => {
    const hot = jointAt(380, 'sn63', 'chisel');
    expect(hot.art.src).toBe(JOINT_ART.cold.src);
    expect(hot.art.alt.toLowerCase()).not.toContain('cold');
  });

  it('a fine tip at a good setting is slow: contact beats the dial', () => {
    expect(jointAt(340, 'sn63', 'fine').kind).toBe('slow');
  });

  it('colder is never faster', () => {
    let last = 0;
    for (let c = 450; c >= 200; c -= 10) {
      const s = jointAt(c, 'sn63', 'chisel').seconds;
      if (s === null) break;
      expect(s).toBeGreaterThanOrEqual(last);
      last = s;
    }
  });
});

describe('tinning', () => {
  it('two tinned surfaces join fastest and cleanest', () => {
    expect(tinningResult(true, true)).toMatchObject({ kind: 'clean', seconds: 1 });
    expect(tinningResult(true, false).kind).toBe('slow');
    expect(tinningResult(false, true).kind).toBe('slow');
    expect(tinningResult(false, false)).toMatchObject({ kind: 'weak', seconds: 5 });
  });

  it('names the bare side', () => {
    expect(tinningResult(true, false).body).toContain('bare pad');
    expect(tinningResult(false, true).body).toContain('bare wire');
  });
});

describe('joint types', () => {
  it('exactly one is good, and the quiz shows every type once', () => {
    expect(JOINT_TYPES.filter((j) => j.good).map((j) => j.id)).toEqual(['good']);
    expect([...QUIZ_ORDER].sort()).toEqual(JOINT_TYPES.map((j) => j.id).sort());
  });
});

describe('tip care', () => {
  const run = (...actions: Parameters<typeof tipStep>[1][]) =>
    actions.reduce<TipState>((s, a) => tipStep(s, a).state, NEW_TIP);

  it('a new tip is shiny and wets', () => {
    expect(tipLook(NEW_TIP)).toBe('shiny');
    expect(tipWets(NEW_TIP)).toBe(true);
  });

  it('storing it bare twice turns it black, and then it won’t wet', () => {
    const s = run('store-bare', 'store-bare');
    expect(tipLook(s)).toBe('black');
    expect(tipWets(s)).toBe(false);
    expect(tipStep(s, 'tin').state).toEqual(s);
  });

  it('storing it tinned keeps it shiny', () => {
    expect(tipLook(run('solder', 'store-tinned'))).toBe('shiny');
  });

  it('tip tinner rescues a black tip', () => {
    const s = run('store-bare', 'store-bare', 'tinner');
    expect(tipLook(s)).toBe('shiny');
    expect(tipWets(s)).toBe(true);
  });

  it('every action has a message', () => {
    for (const a of TIP_ACTIONS) expect(tipStep(NEW_TIP, a.id).message.length).toBeGreaterThan(10);
  });
});

describe('header pins', () => {
  it('tacks opposite corners before soldering the rest', () => {
    const tacks = HEADER_STEPS.find((s) => s.soldered.length === 2)!;
    expect(tacks.soldered).toEqual([0, HEADER_PINS - 1]);
    expect(HEADER_STEPS.at(-1)!.soldered).toHaveLength(HEADER_PINS);
  });
});

describe('art', () => {
  it('every illustration has its 1x and 2x PNG in web/public', () => {
    const arts = [SPOOL_ART, ...Object.values(JOINT_ART), ...Object.values(TINNING_ART)];
    const dir = fileURLToPath(new URL('../../../public/img/guides/soldering', import.meta.url));
    const files = new Set(readdirSync(dir, { withFileTypes: true }).map((d) => d.name));
    for (const a of arts) {
      for (const p of a.srcset.split(', ').map((s) => s.split(' ')[0])) {
        expect(p.startsWith(ART_BASE + '/'), p).toBe(true);
        expect(files.has(p.slice(ART_BASE.length + 1)), p).toBe(true);
      }
    }
  });

  it('builds 1x and 2x paths under one base', () => {
    const a = art('joint-good', 400, 250, 'x');
    expect(a.src).toBe('/img/guides/soldering/joint-good.png');
    expect(a.srcset).toBe('/img/guides/soldering/joint-good.png 1x, /img/guides/soldering/joint-good@2x.png 2x');
  });
});
