import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';
import {
  BATTERY_9V,
  CONTINUITY_TARGETS,
  LED_CIRCUIT,
  METERS,
  VOLTAGE_TARGETS,
  jackPlace,
  measure,
  probeFeedback,
  type MeterId,
  type Setup,
} from './multimeter';

const A = METERS.a;
const B = METERS.b;
const cont = (id: string) => CONTINUITY_TARGETS.find((t) => t.id === id)!;
const volts = (id: string) => VOLTAGE_TARGETS.find((t) => t.id === id)!;
const setup = (meter: typeof A, dial: string, red: string, extra: Partial<Setup> = {}): Setup => ({
  meter,
  dial,
  black: 'com',
  red,
  ...extra,
});

describe('probes', () => {
  it('black belongs in COM on both meters', () => {
    expect(probeFeedback(A, 'black', 'com').ok).toBe(true);
    expect(probeFeedback(B, 'black', 'com').ok).toBe(true);
    const wrong = probeFeedback(B, 'black', 'v');
    expect(wrong.ok).toBe(false);
    expect(wrong.body).toContain('second from the right');
    expect(probeFeedback(A, 'black', 'vma').body).toContain('in the middle');
  });

  it('red belongs in the volts jack, never an amp jack', () => {
    expect(probeFeedback(A, 'red', 'vma', 'com').ok).toBe(true);
    expect(probeFeedback(B, 'red', 'v', 'com').ok).toBe(true);
    const ma = probeFeedback(B, 'red', 'ma', 'com');
    expect(ma.ok).toBe(false);
    expect(ma.body).toContain('only for measuring current');
    expect(probeFeedback(A, 'red', '10a', 'com').body).toContain('no fuse');
    expect(probeFeedback(A, 'red', 'com', 'com').ok).toBe(false);
  });

  it('describes jack positions in words', () => {
    expect(jackPlace(A, '10a')).toBe('on the far left');
    expect(jackPlace(B, 'ma')).toBe('second from the left');
    expect(jackPlace(B, 'v')).toBe('on the far right');
  });
});

describe('continuity', () => {
  it('beeps on a good wire and a pressed switch', () => {
    for (const m of [A, B]) {
      for (const id of ['wire', 'pressed']) {
        const r = measure(setup(m, 'cont', m === A ? 'vma' : 'v'), cont(id));
        expect(r.beep).toBe(true);
        expect(r.overRange).toBe(false);
      }
    }
  });

  it('shows over range and stays silent when open', () => {
    const a = measure(setup(A, 'cont', 'vma'), cont('broken'));
    expect([a.display, a.beep]).toEqual(['1', false]);
    const b = measure(setup(B, 'cont', 'v'), cont('released'));
    expect([b.display, b.beep]).toEqual(['OL', false]);
  });

  it('reads a resistor without beeping', () => {
    const r = measure(setup(B, 'cont', 'v'), cont('resistor'));
    expect([r.display, r.unit, r.beep]).toEqual(['220.0', 'Ω', false]);
    // Meter A's beep range tops out at 199.9 Ω: over range, but not open.
    const a = measure(setup(A, 'cont', 'vma'), cont('resistor'));
    expect([a.display, a.beep]).toEqual(['1', false]);
    expect(a.message).not.toContain('open');
    expect(a.message).toContain('a path');
  });

  it('is off until the dial is turned', () => {
    expect(measure(setup(A, 'off', 'vma'), cont('wire')).display).toBe('');
  });
});

describe('voltage', () => {
  it('reads a 9 V battery on the right manual range', () => {
    const r = measure(setup(A, 'dcv20', 'vma'), BATTERY_9V);
    expect([r.display, r.unit, r.tone]).toEqual(['9.42', 'V', 'ok']);
  });

  it('shows a lone 1 when the manual range is too low', () => {
    const r = measure(setup(A, 'dcv2', 'vma'), BATTERY_9V);
    expect(r.display).toBe('1');
    expect(r.overRange).toBe(true);
  });

  it('auto-ranges on meter B', () => {
    expect(measure(setup(B, 'dcv', 'v'), volts('aa')).display).toBe('1.580');
    expect(measure(setup(B, 'dcv', 'v'), BATTERY_9V).display).toBe('9.42');
    expect(measure(setup(B, 'dcv', 'v'), volts('pin3v3')).display).toBe('3.310');
    expect(measure(setup(B, 'mv', 'v'), volts('aa')).display).toBe('OL');
  });

  it('shows a minus sign when the probes are reversed', () => {
    const r = measure(setup(B, 'dcv', 'v', { reversed: true }), volts('pin5v'));
    expect(r.display).toBe('-4.980');
    expect(r.tone).toBe('info');
  });

  it('warns on AC volts and resistance settings', () => {
    expect(measure(setup(B, 'acv', 'v'), BATTERY_9V).tone).toBe('warn');
    expect(measure(setup(A, 'ohm2k', 'vma'), BATTERY_9V).tone).toBe('warn');
  });
});

describe('current', () => {
  it('reads about 18 mA in series through the mA jack', () => {
    const a = measure(setup(A, 'dca20m', 'vma'), LED_CIRCUIT);
    expect([a.display, a.unit, a.ledLit, a.tone]).toEqual(['17.95', 'mA', true, 'ok']);
    const b = measure(setup(B, 'ma', 'ma'), LED_CIRCUIT);
    expect([b.display, b.unit]).toEqual(['17.95', 'mA']);
  });

  it('shows coarse digits on the big-current range', () => {
    expect(measure(setup(A, '10a', '10a'), LED_CIRCUIT).display).toBe('0.02');
    expect(measure(setup(B, 'a', 'a'), LED_CIRCUIT).display).toBe('0.018');
  });

  it('flags a dial that does not match the jack', () => {
    const r = measure(setup(B, 'a', 'ma'), LED_CIRCUIT);
    expect(r.title).toBe('Dial and jack don’t match');
    expect(measure(setup(A, '10a', 'vma'), LED_CIRCUIT).ledLit).toBe(false);
  });

  it('a voltmeter in series blocks the current', () => {
    const r = measure(setup(B, 'dcv', 'v'), LED_CIRCUIT);
    expect(r.ledLit).toBe(false);
  });

  it('blows the fuse when the mA jack goes across a battery', () => {
    for (const [m, dial, red] of [
      [A, 'dca200m', 'vma'],
      [B, 'ma', 'ma'],
      [B, 'dcv', 'ma'],
    ] as const) {
      const r = measure(setup(METERS[m === A ? 'a' : 'b'], dial, red), BATTERY_9V);
      expect(r.blowsFuse).toBe(true);
      expect(r.title).toBe('Fuse blown');
    }
  });

  it('a blown fuse leaves the circuit open', () => {
    const r = measure(setup(B, 'ma', 'ma', { blown: ['ma'] }), LED_CIRCUIT);
    expect([r.display, r.ledLit, r.blowsFuse]).toEqual(['0.00', false, true]);
  });

  it('an unfused 10A jack across a battery is a short, not a blown fuse', () => {
    const r = measure(setup(A, '10a', '10a'), BATTERY_9V);
    expect(r.blowsFuse).toBe(false);
    expect(r.tone).toBe('bad');
    expect(r.message).toContain('no fuse');
  });

  it('the VΩmA jack on a volts setting is not a current path', () => {
    expect(measure(setup(A, 'dcv20', 'vma'), BATTERY_9V).blowsFuse).toBe(false);
  });
});

// The drawings and the data must agree until photos replace the drawings.
describe('drawings', () => {
  for (const id of ['a', 'b'] as MeterId[]) {
    it(`meter ${id} jack coordinates match its SVG`, () => {
      const svg = readFileSync(fileURLToPath(new URL(`../../../../assets/guides/multimeter/meter-${id}.svg`, import.meta.url)), 'utf-8');
      const m = METERS[id];
      expect(svg).toContain(`width="${m.image.width}" height="${m.image.height}"`);
      for (const j of m.jacks) expect(svg).toContain(`id="jack-${j.id}" cx="${j.x}" cy="${j.y}"`);
    });
  }
});
