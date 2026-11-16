import { describe, expect, it } from 'vitest';
import {
  MIN_V,
  RAIL_V,
  capPathNH,
  capUnits,
  capVoltage,
  decodeCapCode,
  effectiveNH,
  eiaMultiplier,
  fmtAmps,
  fmtOhms,
  fmtSeconds,
  percentAfter,
  polarityOk,
  railDip,
  railTrace,
  ratingVerdict,
  tau,
  toEiaCode,
} from './capacitors';

function pf(code: string): number {
  const r = decodeCapCode(code);
  if (!r.ok) throw new Error(`${code}: ${r.error}`);
  return r.value.pf;
}

describe('EIA three-digit codes', () => {
  it('two digits of value, then the number of zeros, in pF', () => {
    expect(pf('104')).toBe(100000);
    expect(pf('472')).toBe(4700);
    expect(pf('103')).toBe(10000);
    expect(pf('105')).toBe(1000000);
    expect(pf('220')).toBe(22);
    expect(pf('100')).toBe(10);
  });
  it('8 and 9 divide instead, and 7 is not used', () => {
    expect(eiaMultiplier(8)).toBe(0.01);
    expect(eiaMultiplier(9)).toBe(0.1);
    expect(eiaMultiplier(7)).toBeNull();
    expect(pf('479')).toBeCloseTo(4.7);
    expect(decodeCapCode('477').ok).toBe(false);
  });
  it('a trailing tolerance letter is read', () => {
    const r = decodeCapCode('104K');
    expect(r.ok && r.value.tolerance).toBe('±10%');
    expect(r.ok && r.value.pf).toBe(100000);
    const j = decodeCapCode('472j');
    expect(j.ok && j.value.tolerance).toBe('±5%');
    expect(decodeCapCode('104Q').ok).toBe(false);
  });
  it('one or two digits are picofarads; four or more is an error', () => {
    expect(pf('47')).toBe(47);
    expect(pf('1')).toBe(1);
    expect(decodeCapCode('1000').ok).toBe(false);
  });
});

describe('values with units and decimal letters', () => {
  it('reads µF, uF, μF (Greek mu), nF and pF', () => {
    expect(pf('0.1µF')).toBeCloseTo(100000);
    expect(pf('0.1uF')).toBeCloseTo(100000);
    expect(pf('0.1μF')).toBeCloseTo(100000);
    expect(pf('100nF')).toBe(100000);
    expect(pf('22pF')).toBe(22);
    expect(pf('10 µF')).toBe(10000000);
    expect(pf('1F')).toBe(1e12);
  });
  it('R and unit letters as the decimal point', () => {
    expect(pf('4R7')).toBeCloseTo(4.7);
    expect(pf('R5')).toBeCloseTo(0.5);
    expect(pf('4n7')).toBeCloseTo(4700);
    expect(pf('2u2')).toBeCloseTo(2.2e6);
  });
  it('picks out a voltage rating, with or without a space', () => {
    for (const s of ['10µF 16V', '10µF16V', '10uF/16V', '10µF, 16 VDC']) {
      const r = decodeCapCode(s);
      expect(r.ok, s).toBe(true);
      if (r.ok) {
        expect(r.value.pf, s).toBe(10000000);
        expect(r.value.volts, s).toBe(16);
      }
    }
    const k = decodeCapCode('104K 50V');
    expect(k.ok && k.value.volts).toBe(50);
  });
  it('rejects empty input, a bare voltage, a bare decimal and nonsense', () => {
    expect(decodeCapCode('').ok).toBe(false);
    expect(decodeCapCode('16V').ok).toBe(false);
    const bare = decodeCapCode('0.1');
    expect(bare.ok).toBe(false);
    if (!bare.ok) expect(bare.error).toContain('Add a unit');
    expect(decodeCapCode('hello').ok).toBe(false);
    expect(decodeCapCode('0pF').ok).toBe(false);
  });
});

describe('formatting', () => {
  it('shows the same value in all three units, without float noise', () => {
    expect(capUnits(100000)).toEqual({ pF: '100000 pF', nF: '100 nF', uF: '0.1 µF', best: '100 nF' });
    expect(capUnits(4700).best).toBe('4.7 nF');
    expect(capUnits(22).best).toBe('22 pF');
    expect(capUnits(10_000_000).best).toBe('10 µF');
    expect(capUnits(0.1 * 1e6).uF).toBe('0.1 µF');
    expect(capUnits(1).uF).toBe('0.000001 µF');
  });
  it('turns values back into the code printed on the part', () => {
    expect(toEiaCode(100000)).toBe('104');
    expect(toEiaCode(4700)).toBe('472');
    expect(toEiaCode(10)).toBe('100');
    expect(toEiaCode(1e6)).toBe('105');
    expect(toEiaCode(4.7)).toBe('4R7');
    expect(toEiaCode(1e8)).toBeNull();
    expect(toEiaCode(4750)).toBeNull();
  });
  it('time, resistance and current read naturally', () => {
    expect(fmtSeconds(1)).toBe('1 s');
    expect(fmtSeconds(0.047)).toBe('47 ms');
    expect(fmtSeconds(0.0001)).toBe('100 µs');
    expect(fmtOhms(10000)).toBe('10 kΩ');
    expect(fmtOhms(470)).toBe('470 Ω');
    expect(fmtOhms(1e6)).toBe('1 MΩ');
    expect(fmtAmps(0.00033)).toBe('330 µA');
    expect(fmtAmps(0.0033)).toBe('3.3 mA');
    expect(fmtAmps(0)).toBe('0 mA');
  });
});

describe('polarity and rating', () => {
  it('an electrolytic’s stripe is negative; a tantalum’s band is positive', () => {
    expect(polarityOk('electrolytic', 'mark-to-ground')).toBe(true);
    expect(polarityOk('electrolytic', 'mark-to-plus')).toBe(false);
    expect(polarityOk('tantalum', 'mark-to-plus')).toBe(true);
    expect(polarityOk('tantalum', 'mark-to-ground')).toBe(false);
  });
  it('a ceramic goes either way', () => {
    expect(polarityOk('ceramic', 'mark-to-plus')).toBe(true);
    expect(polarityOk('ceramic', 'mark-to-ground')).toBe(true);
  });
  it('rating against the rail', () => {
    expect(ratingVerdict(4, 5)).toBe('over');
    expect(ratingVerdict(6.3, 5)).toBe('tight');
    expect(ratingVerdict(10, 5)).toBe('ok');
    expect(ratingVerdict(6.3, 3.3)).toBe('ok');
    expect(ratingVerdict(5, 5)).toBe('tight');
  });
});

describe('RC charging', () => {
  it('τ = R × C', () => {
    expect(tau(10000, 100e-6)).toBeCloseTo(1);
    expect(tau(1000, 47e-6)).toBeCloseTo(0.047);
  });
  it('charges to 63.2% after one τ and over 99% after five', () => {
    expect(percentAfter(1)).toBeCloseTo(63.2, 1);
    expect(percentAfter(2)).toBeCloseTo(86.5, 1);
    expect(percentAfter(3)).toBeCloseTo(95.0, 1);
    expect(percentAfter(5)).toBeCloseTo(99.3, 1);
    expect(capVoltage(1, 1, 3.3, 0)).toBeCloseTo(3.3 * 0.632, 2);
    expect(capVoltage(0, 1, 3.3, 0)).toBe(0);
  });
  it('discharges to 36.8% after one τ, from wherever it started', () => {
    expect(capVoltage(1, 1, 0, 3.3)).toBeCloseTo(3.3 * 0.368, 2);
    expect(capVoltage(1, 1, 0, 2)).toBeCloseTo(2 * 0.368, 2);
    expect(capVoltage(5, 0, 0, 3.3)).toBe(0);
  });
});

describe('decoupling teaching model', () => {
  const none = { capF: null, distanceMm: 0 };
  const near = { capF: 100e-9, distanceMm: 2 };
  const far = { capF: 100e-9, distanceMm: 40 };

  it('the capacitor path adds inductance with distance', () => {
    expect(capPathNH(0)).toBe(1);
    expect(capPathNH(10)).toBe(11);
    expect(effectiveNH(none)).toBe(25);
    expect(effectiveNH(near)).toBeLessThan(effectiveNH(far));
    expect(effectiveNH(far)).toBeLessThan(effectiveNH(none));
  });
  it('no capacitor dips below the chip’s 3.0 V minimum; a close 100 nF keeps it near 3.3 V', () => {
    expect(railDip(none).minV).toBeLessThan(MIN_V);
    expect(railDip(near).minV).toBeGreaterThan(3.2);
    expect(railDip(far).minV).toBeLessThan(railDip(near).minV);
  });
  it('the dip gets worse steadily as the capacitor moves away', () => {
    let last = Infinity;
    for (let d = 0; d <= 40; d += 5) {
      const v = railDip({ capF: 100e-9, distanceMm: d }).minV;
      expect(v).toBeLessThan(last);
      last = v;
    }
  });
  it('too small a capacitor runs out of charge', () => {
    const small = railDip({ capF: 10e-9, distanceMm: 2 });
    expect(small.chargeV).toBeCloseTo(0.2, 5);
    expect(small.minV).toBeLessThan(railDip(near).minV);
    expect(railDip({ capF: 1e-6, distanceMm: 2 }).chargeV).toBeCloseTo(0.002, 5);
  });
  it('the plotted trace starts at the rail and its lowest point matches railDip', () => {
    for (const o of [none, near, far]) {
      const tr = railTrace(o, 0.5);
      expect(tr[0][1]).toBe(RAIL_V);
      const low = Math.min(...tr.map((p) => p[1]));
      expect(low).toBeGreaterThanOrEqual(railDip(o).minV - 1e-9);
      expect(low).toBeLessThan(railDip(o).minV + 0.02);
    }
  });
});
