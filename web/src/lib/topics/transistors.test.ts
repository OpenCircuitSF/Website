import { describe, expect, it } from 'vitest';
import {
  BASE_RESISTORS,
  BJTS,
  COILS,
  DIODE_VF,
  MOSFETS,
  SWITCH_RATING,
  amps,
  bjtSwitch,
  flyback,
  flybackTrace,
  heatLevel,
  interpLog,
  mosfetPoint,
  mosfetSwitch,
  ohms,
  pickSwitch,
  suggestBaseResistor,
  watts,
  PICK_CURRENTS,
  PICK_DRIVES,
  PICK_SUPPLIES,
} from './transistors';

describe('formatting', () => {
  it('ohms, amps and watts', () => {
    expect(ohms(470)).toBe('470 Ω');
    expect(ohms(1000)).toBe('1 kΩ');
    expect(ohms(2200)).toBe('2.2 kΩ');
    expect(amps(0.0055)).toBe('5.5 mA');
    expect(amps(0.05)).toBe('50 mA');
    expect(amps(0.0005)).toBe('500 µA');
    expect(amps(2)).toBe('2.00 A');
    expect(watts(0.25)).toBe('250 mW');
    expect(watts(1.25)).toBe('1.25 W');
  });

  it('base resistors are E12 values in order', () => {
    const e12 = [10, 12, 15, 18, 22, 27, 33, 39, 47, 56, 68, 82];
    for (const r of BASE_RESISTORS) {
      const mant = r / 10 ** Math.floor(Math.log10(r) - 1);
      expect(e12, String(r)).toContain(Math.round(mant));
    }
    expect([...BASE_RESISTORS].sort((a, b) => a - b)).toEqual(BASE_RESISTORS);
  });
});

describe('interpLog', () => {
  const t = [
    [0.001, 10],
    [0.1, 30],
  ] as const;
  it('hits the table points, clamps outside, and interpolates on log x', () => {
    expect(interpLog(t, 0.001)).toBe(10);
    expect(interpLog(t, 0.1)).toBe(30);
    expect(interpLog(t, 0.00001)).toBe(10);
    expect(interpLog(t, 5)).toBe(30);
    expect(interpLog(t, 0.01)).toBeCloseTo(20, 6);
  });
});

describe('NPN transistor switch', () => {
  const q = BJTS.PN2222A;

  it('pin low: off, the whole supply across it', () => {
    const r = bjtSwitch(q, false, 470, 0.05);
    expect(r.state).toBe('off');
    expect(r.ic).toBe(0);
    expect(r.vce).toBe(5);
  });

  it('470 Ω and 50 mA: about 5.5 mA of base current, saturated', () => {
    const r = bjtSwitch(q, true, 470, 0.05);
    expect(r.ib * 1000).toBeCloseTo(5.53, 2);
    expect(r.state).toBe('saturated');
    expect(r.gainNeeded).toBeLessThanOrEqual(10);
    expect(r.vce).toBeLessThan(0.3);
    expect(r.pinLevel).toBe('ok');
  });

  it('1 kΩ and 100 mA: on, but not guaranteed (Ic/Ib above 10)', () => {
    const r = bjtSwitch(q, true, 1000, 0.1);
    expect(r.state).toBe('marginal');
    expect(r.gainNeeded).toBeGreaterThan(10);
    expect(r.gainNeeded).toBeLessThan(r.hfeMin);
  });

  it('10 kΩ and 100 mA: not enough base current, the load gets a fraction and the rest is heat', () => {
    const r = bjtSwitch(q, true, 10000, 0.1);
    expect(r.state).toBe('linear');
    expect(r.ic).toBeLessThan(0.02);
    expect(r.vce).toBeGreaterThan(4);
    expect(r.power).toBeGreaterThan(0.05);
  });

  it('100 Ω pulls more than 20 mA from the pin', () => {
    expect(bjtSwitch(q, true, 100, 0.1).pinLevel).toBe('too-high');
    expect(bjtSwitch(q, true, 220, 0.1).pinLevel).toBe('high');
  });

  it('a 2N3904 asked for 300 mA is over its 200 mA rating', () => {
    expect(bjtSwitch(BJTS['2N3904'], true, 220, 0.3).overCurrent).toBe(true);
    expect(bjtSwitch(BJTS.PN2222A, true, 220, 0.3).overCurrent).toBe(false);
  });

  it('suggests the largest E12 base resistor for Ic/Ib = 10, none past the pin limit', () => {
    expect(suggestBaseResistor(0.05)).toBe(470);
    expect(suggestBaseResistor(0.1)).toBe(220);
    expect(suggestBaseResistor(0.01)).toBe(2200);
    expect(suggestBaseResistor(0.2)).toBeUndefined();
    expect(suggestBaseResistor(0.5)).toBeUndefined();
    expect(suggestBaseResistor(0.1, 5)).toBe(330);
  });

  it('TO-92 pins are E-B-C, with a maker note', () => {
    expect(BJTS['2N3904'].pins).toEqual(['E', 'B', 'C']);
    expect(BJTS.PN2222A.pinNote).toContain('P2N2222A is C-B-E');
  });
});

describe('MOSFET switch', () => {
  it('datasheet Rds(on) points', () => {
    expect(mosfetPoint(MOSFETS.AO3400A, 4.5)?.rds).toBe(0.032);
    expect(mosfetPoint(MOSFETS.IRLZ44N, 5)?.rds).toBe(0.025);
    expect(mosfetPoint(MOSFETS.IRFZ44N, 10)?.rds).toBe(0.0175);
    expect(mosfetPoint(MOSFETS.AO3400A, 3.3)?.estimate).toBe(true);
    expect(mosfetPoint(MOSFETS.IRFZ44N, 7)).toBeUndefined();
  });

  it('AO3400A at 3.3 V carrying 1 A: fully on, cool', () => {
    const r = mosfetSwitch(MOSFETS.AO3400A, 'high', 3.3, true, 1);
    expect(r.state).toBe('on');
    expect(r.id).toBeCloseTo(0.992, 3);
    expect(r.vds).toBeLessThan(0.05);
    expect(r.power).toBeLessThan(0.05);
    expect(r.heat).toBe('cool');
  });

  it('IRFZ44N at 3.3 V barely conducts: the load gets about 5%', () => {
    const r = mosfetSwitch(MOSFETS.IRFZ44N, 'high', 3.3, true, 1);
    expect(r.state).toBe('partly');
    expect(r.id).toBe(0.05);
    expect(r.vds).toBeCloseTo(4.75, 6);
    expect(r.rds).toBeCloseTo(95, 6);
    const at10 = mosfetSwitch(MOSFETS.IRFZ44N, 'high', 10, true, 1);
    expect(at10.state).toBe('on');
  });

  it('IRFZ44N at 5 V, 5 A on 12 V: partly on and far too hot', () => {
    const r = mosfetSwitch(MOSFETS.IRFZ44N, 'high', 5, true, 5, 12);
    expect(r.state).toBe('partly');
    expect(r.power).toBeCloseTo(14.4, 6);
    expect(r.heat).toBe('too-hot');
  });

  it('gate low, or floating with a pull-down: off. Floating without one: unknown', () => {
    expect(mosfetSwitch(MOSFETS.AO3400A, 'low', 3.3, false, 1).state).toBe('off');
    expect(mosfetSwitch(MOSFETS.AO3400A, 'floating', 3.3, true, 1).state).toBe('off');
    expect(mosfetSwitch(MOSFETS.AO3400A, 'floating', 3.3, false, 1).state).toBe('unknown');
  });

  it('flags a supply over the drain rating and current over the part rating', () => {
    expect(mosfetSwitch(MOSFETS.AO3400A, 'high', 10, true, 1, 48).overVoltage).toBe(true);
    expect(mosfetSwitch(MOSFETS.AO3400A, 'high', 10, true, 7, 12).overCurrent).toBe(true);
  });

  it('heat levels', () => {
    expect(heatLevel(40)).toBe('cool');
    expect(heatLevel(90)).toBe('warm');
    expect(heatLevel(140)).toBe('hot');
    expect(heatLevel(200)).toBe('too-hot');
  });

  it('throws for a gate drive the part has no point for', () => {
    expect(() => mosfetSwitch(MOSFETS.IRFZ44N, 'high', 7, true, 1)).toThrow();
  });
});

describe('flyback', () => {
  it('relay with the diode the right way: clamps one diode drop above the supply', () => {
    const r = flyback(COILS.relay, 'correct');
    expect(r.peak).toBeCloseTo(5 + DIODE_VF, 6);
    expect(r.overRating).toBe(false);
    expect(r.tau * 1e6).toBeCloseTo(714, 0);
    expect(r.i0).toBeCloseTo(0.0714, 4);
  });

  it('no diode: the spike would be far past the transistor rating, so it breaks down', () => {
    const r = flyback(COILS.relay, 'none');
    expect(r.unclamped).toBeGreaterThan(1000);
    expect(r.peak).toBe(SWITCH_RATING);
    expect(r.overRating).toBe(true);
    expect(r.shortsWhenOn).toBe(false);
  });

  it('reversed: no clamp, and a short across the supply when on', () => {
    const r = flyback(COILS.solenoid, 'reversed');
    expect(r.peak).toBe(SWITCH_RATING);
    expect(r.shortsWhenOn).toBe(true);
  });

  it('stored energy is half L I squared', () => {
    expect(flyback(COILS.solenoid, 'correct').energy).toBeCloseTo(0.5 * 0.1 * 0.25, 9);
  });

  it('trace: with the diode, the current decays and the voltage stays near supply', () => {
    const tr = flybackTrace(COILS.relay, 'correct', 50);
    expect(tr).toHaveLength(51);
    expect(Math.max(...tr.map((p) => p.v))).toBeCloseTo(5.7, 6);
    expect(tr[50].i).toBeLessThan(tr[0].i * 0.01);
    expect(tr[50].v).toBe(5);
  });

  it('trace: without it, a spike at the start that dies back to the supply', () => {
    const tr = flybackTrace(COILS.motor, 'none', 50);
    expect(tr[0].v).toBe(SWITCH_RATING);
    expect(tr[50].v).toBeCloseTo(5, 3);
  });
});

describe('BJT or MOSFET', () => {
  it('50 mA at 5 V from a 3.3 V pin: either, with a 470 Ω base resistor', () => {
    const r = pickSwitch(0.05, 5, 3.3, true);
    expect(r.pick).toBe('either');
    expect(r.bjt.why).toContain('470 Ω');
    expect(r.part).toBe('2N2222A or IRLZ44N');
  });

  it('half an amp: a MOSFET, because the transistor would need 50 mA from the pin', () => {
    const r = pickSwitch(0.5, 5, 3.3, false);
    expect(r.pick).toBe('mosfet');
    expect(r.part).toBe('AO3400A');
    expect(r.bjt.ok).toBe(false);
    expect(r.bjt.why).toContain('50 mA');
  });

  it('200 mA asks 20 mA of the pin: MOSFET', () => {
    expect(pickSwitch(0.2, 5, 3.3, false).pick).toBe('mosfet');
  });

  it('5 A at 12 V: the AO3400A would cook, so the IRLZ44N', () => {
    const r = pickSwitch(5, 12, 3.3, false);
    expect(r.pick).toBe('mosfet');
    expect(r.mosfet.part).toBe('IRLZ44N');
  });

  it('10 A from a 3.3 V pin, or 48 V: beyond a simple switch', () => {
    expect(pickSwitch(10, 12, 3.3, true).pick).toBe('neither');
    expect(pickSwitch(0.05, 48, 3.3, false).pick).toBe('neither');
  });

  it('24 V: the AO3400A is still within 80% of its rating; through-hole picks the IRLZ44N', () => {
    expect(pickSwitch(1, 24, 3.3, false).mosfet.part).toBe('AO3400A');
    expect(pickSwitch(1, 24, 3.3, true).mosfet.part).toBe('IRLZ44N');
  });

  it('pickSwitch reports an over-hot MOSFET in the chosen unit', () => {
    let hit: [number, number, number] | null = null;
    for (const a of PICK_CURRENTS)
      for (const v of PICK_SUPPLIES)
        for (const d of PICK_DRIVES)
          if (!hit && pickSwitch(a, v, d, true).mosfet.why.includes('without a heatsink')) hit = [a, v, d];
    expect(hit).not.toBeNull();
    const [a, v, d] = hit!;
    expect(pickSwitch(a, v, d, true).mosfet.why).toMatch(/about \d+ °F without/);
    expect(pickSwitch(a, v, d, true, 'C').mosfet.why).toMatch(/about \d+ °C without/);
  });
});
