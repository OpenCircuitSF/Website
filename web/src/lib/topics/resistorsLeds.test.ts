import { describe, expect, it } from 'vitest';
import {
  E12,
  E24,
  SLIDER_OHMS,
  bandsFor,
  choicesFor,
  formatAmps,
  formatOhms,
  formatWatts,
  isEValue,
  ledById,
  ledResistor,
  ledState,
  nearestE,
  nextBand,
  nextEUp,
  parseOhms,
  readBands,
  solve,
} from './resistorsLeds';

describe('color code (IEC 60062)', () => {
  it('reads common 4-band parts', () => {
    expect(readBands(['red', 'red', 'brown', 'gold'])).toMatchObject({ ohms: 220, tolerance: 5 });
    expect(readBands(['yellow', 'violet', 'red', 'gold']).ohms).toBe(4700);
    expect(readBands(['brown', 'black', 'orange', 'gold']).ohms).toBe(10000);
    expect(readBands(['brown', 'black', 'green', 'silver'])).toMatchObject({ ohms: 1e6, tolerance: 10 });
    expect(readBands(['yellow', 'violet', 'gold', 'gold']).ohms).toBe(4.7);
    expect(readBands(['yellow', 'violet', 'silver', 'gold']).ohms).toBe(0.47);
  });

  it('reads 5-band parts with three digits', () => {
    expect(readBands(['red', 'red', 'black', 'black', 'brown'])).toMatchObject({ ohms: 220, tolerance: 1 });
    expect(readBands(['brown', 'black', 'black', 'red', 'brown']).ohms).toBe(10000);
    expect(readBands(['yellow', 'violet', 'black', 'brown', 'brown']).ohms).toBe(4700);
  });

  it('gives the tolerance range', () => {
    const r = readBands(['brown', 'black', 'red', 'gold']);
    expect(r.min).toBe(950);
    expect(r.max).toBe(1050);
  });

  it('rejects colors in the wrong place', () => {
    expect(() => readBands(['gold', 'red', 'red', 'gold'])).toThrow();
    expect(() => readBands(['red', 'red', 'red', 'black'])).toThrow();
    expect(() => readBands(['red', 'red', 'red'])).toThrow();
  });

  it('turns values into bands and back', () => {
    expect(bandsFor(220, 4)?.bands).toEqual(['red', 'red', 'brown', 'gold']);
    expect(bandsFor(4700, 4)?.bands).toEqual(['yellow', 'violet', 'red', 'gold']);
    expect(bandsFor(4700, 5)?.bands).toEqual(['yellow', 'violet', 'black', 'brown', 'brown']);
    expect(bandsFor(10, 4)?.bands).toEqual(['brown', 'black', 'black', 'gold']);
    expect(bandsFor(1, 4)?.bands).toEqual(['brown', 'black', 'gold', 'gold']);
    expect(bandsFor(0.47, 4)?.bands).toEqual(['yellow', 'violet', 'silver', 'gold']);
    for (const v of [1, 4.7, 68, 330, 1500, 82000, 2.2e6]) {
      expect(readBands(bandsFor(v, 4)!.bands).ohms, String(v)).toBe(v);
      expect(readBands(bandsFor(v, 5)!.bands).ohms, String(v)).toBe(v);
    }
  });

  it('rounds values that need more digits, and says so', () => {
    const b = bandsFor(4750, 4)!;
    expect(b.exact).toBe(false);
    expect(b.ohms).toBe(4800);
    expect(bandsFor(4750, 5)).toMatchObject({ exact: true, ohms: 4750 });
    // 995 rounds up a decade on two digits.
    expect(bandsFor(995, 4)).toMatchObject({ ohms: 1000, bands: ['brown', 'black', 'red', 'gold'] });
  });

  it('refuses values out of range', () => {
    expect(bandsFor(0, 4)).toBeNull();
    expect(bandsFor(-5, 4)).toBeNull();
    expect(bandsFor(0.01, 4)).toBeNull();
    expect(bandsFor(1e12, 4)).toBeNull();
  });

  it('cycles each band through the colors allowed there', () => {
    expect(choicesFor(4, 0)).not.toContain('black');
    expect(choicesFor(4, 1)).toContain('black');
    expect(choicesFor(4, 2)).toContain('gold');
    expect(choicesFor(5, 3)).toContain('silver');
    expect(choicesFor(4, 3)).toEqual(choicesFor(5, 4));
    expect(nextBand(4, 0, 'white')).toBe('brown');
    expect(nextBand(4, 1, 'red')).toBe('orange');
    expect(nextBand(4, 1, 'black', -1)).toBe('white');
    expect(nextBand(4, 3, 'gold')).toBe('silver');
  });
});

describe('parsing typed values', () => {
  it('reads the usual ways of writing a resistance', () => {
    expect(parseOhms('220')).toBe(220);
    expect(parseOhms('4.7k')).toBe(4700);
    expect(parseOhms('4k7')).toBe(4700);
    expect(parseOhms('4R7')).toBe(4.7);
    expect(parseOhms('1M')).toBe(1e6);
    expect(parseOhms('10 kΩ')).toBe(10000);
    expect(parseOhms('470 ohms')).toBe(470);
    expect(parseOhms('.47')).toBe(0.47);
  });

  it('rejects nonsense', () => {
    expect(parseOhms('')).toBeNull();
    expect(parseOhms('abc')).toBeNull();
    expect(parseOhms('0')).toBeNull();
    expect(parseOhms('4.7x')).toBeNull();
  });
});

describe('E-series (IEC 60063)', () => {
  it('has 12 and 24 values per decade', () => {
    expect(E12).toHaveLength(12);
    expect(E24).toHaveLength(24);
    for (const v of E12) expect(E24).toContain(v);
  });

  it('finds the nearest value and the next one up', () => {
    expect(nearestE(4750, 'E12')).toBe(4700);
    expect(nearestE(500, 'E24')).toBe(510);
    expect(nearestE(500, 'E12')).toBe(470);
    expect(nearestE(95, 'E12')).toBe(100);
    expect(nextEUp(65, 'E12')).toBe(68);
    expect(nextEUp(68, 'E12')).toBe(68);
    expect(nextEUp(90, 'E12')).toBe(100);
    expect(nextEUp(0.9, 'E12')).toBe(1);
  });

  it('knows E-series values from the rest', () => {
    expect(isEValue(4700, 'E12')).toBe(true);
    expect(isEValue(5100, 'E12')).toBe(false);
    expect(isEValue(5100, 'E24')).toBe(true);
  });

  it('slider stops run 10 Ω to 10 kΩ on E12', () => {
    expect(SLIDER_OHMS[0]).toBe(10);
    expect(SLIDER_OHMS.at(-1)).toBe(10000);
    expect(SLIDER_OHMS).toContain(220);
    expect(SLIDER_OHMS).toHaveLength(37);
  });
});

describe('formatting', () => {
  it('picks a sensible unit', () => {
    expect(formatOhms(220)).toBe('220 Ω');
    expect(formatOhms(4700)).toBe('4.7 kΩ');
    expect(formatOhms(1e6)).toBe('1 MΩ');
    expect(formatOhms(66.6667)).toBe('66.7 Ω');
    expect(formatAmps(0.0065)).toBe('6.5 mA');
    expect(formatWatts(0.25)).toBe('250 mW');
    expect(formatWatts(1.44)).toBe('1.44 W');
  });
});

describe("Ohm's law", () => {
  it('one resistor: I = V / R, P = V × I', () => {
    const c = solve(5, 220, 1000, 'one');
    expect(c.parts).toHaveLength(1);
    expect(c.totalAmps).toBeCloseTo(5 / 220, 9);
    expect(c.parts[0].watts).toBeCloseTo((5 * 5) / 220, 9);
    expect(c.parts[0].overRated).toBe(false);
  });

  it('series: resistances add and the voltage splits', () => {
    const c = solve(9, 1000, 2000, 'series');
    expect(c.totalOhms).toBe(3000);
    expect(c.totalAmps).toBeCloseTo(0.003, 9);
    expect(c.parts[0].volts).toBeCloseTo(3, 9);
    expect(c.parts[1].volts).toBeCloseTo(6, 9);
  });

  it('parallel: each sees the full voltage and the currents add', () => {
    const c = solve(6, 1000, 1000, 'parallel');
    expect(c.totalOhms).toBe(500);
    expect(c.parts[0].volts).toBe(6);
    expect(c.totalAmps).toBeCloseTo(0.012, 9);
  });

  it('flags a resistor past 1/4 W', () => {
    expect(solve(12, 100, 100, 'one').parts[0].overRated).toBe(true); // 1.44 W
    expect(solve(5, 100, 100, 'one').parts[0].overRated).toBe(false); // 0.25 W exactly
    expect(solve(12, 100, 10000, 'series').parts.map((p) => p.overRated)).toEqual([false, false]);
  });

  it('refuses a zero resistance', () => {
    expect(() => solve(5, 0, 100, 'one')).toThrow();
  });
});

describe('LEDs', () => {
  it('has the usual forward voltages', () => {
    expect(ledById('red').vf).toBe(2.0);
    expect(ledById('blue').vf).toBeGreaterThan(3);
  });

  it('a reversed LED stays dark', () => {
    expect(ledState(3.3, 2.0, 220, true)).toEqual({ kind: 'reversed' });
  });

  it('a supply below Vf cannot light it', () => {
    expect(ledState(3.3, 3.4, 100, false)).toEqual({ kind: 'too-low' });
  });

  it('no resistor means no limit', () => {
    expect(ledState(3.3, 2.0, null, false)).toEqual({ kind: 'no-resistor' });
  });

  it('the resistor sets the current', () => {
    const s = ledState(3.3, 2.0, 220, false);
    expect(s.kind).toBe('lit');
    if (s.kind === 'lit') {
      expect(s.amps).toBeCloseTo(1.3 / 220, 9);
      expect(s.brightness).toBeCloseTo(1.3 / 220 / 0.02, 9);
    }
    const blue = ledState(3.3, 3.2, 220, false);
    if (blue.kind === 'lit') expect(blue.amps).toBeLessThan(0.001);
  });

  it('picks the next E12 value up', () => {
    // Red on a 3.3 V pin at 10 mA: 130 Ω exact, 150 Ω chosen.
    const r = ledResistor(3.3, 2.0, 0.01, true)!;
    expect(r.exact).toBeCloseTo(130, 9);
    expect(r.chosen).toBe(150);
    expect(r.amps).toBeCloseTo(1.3 / 150, 9);
    expect(r.amps).toBeLessThanOrEqual(0.01);
    expect(r.warnings).toEqual([]);
    // Red on 5 V at 20 mA: 150 Ω exactly, an E12 value.
    expect(ledResistor(5, 2.0, 0.02, false)!.chosen).toBe(150);
  });

  it('warns past the LED, the pin and the 1/4 W rating', () => {
    expect(ledResistor(3.3, 2.0, 0.03, true)!.warnings).toEqual(['led-max', 'pin']);
    expect(ledResistor(5, 2.0, 0.03, false)!.warnings).toEqual(['led-max']);
    expect(ledResistor(24, 2.0, 0.015, false)!.warnings).toEqual(['power']);
  });

  it('returns null when the supply cannot reach Vf', () => {
    expect(ledResistor(3.3, 3.3, 0.01, true)).toBeNull();
    expect(ledResistor(5, 2, 0, false)).toBeNull();
  });
});
