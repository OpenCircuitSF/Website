// The logic behind the resistor and LED guides (#0690): the IEC 60062 color
// code, the E12 and E24 series, Ohm's law for one or two resistors, and the
// LED current-limiting resistor. Plain functions so they can be tested
// without a DOM; the demos only draw what these return.

// ---------------------------------------------------------------- color code

export type BandColor =
  | 'black'
  | 'brown'
  | 'red'
  | 'orange'
  | 'yellow'
  | 'green'
  | 'blue'
  | 'violet'
  | 'grey'
  | 'white'
  | 'gold'
  | 'silver';

/** The ten digit colors, in digit order (black = 0 … white = 9). */
export const DIGIT_COLORS: BandColor[] = ['black', 'brown', 'red', 'orange', 'yellow', 'green', 'blue', 'violet', 'grey', 'white'];

/** Multiplier band: the power of ten each color means. */
export const MULTIPLIER_EXP: Partial<Record<BandColor, number>> = {
  black: 0,
  brown: 1,
  red: 2,
  orange: 3,
  yellow: 4,
  green: 5,
  blue: 6,
  violet: 7,
  grey: 8,
  white: 9,
  gold: -1,
  silver: -2,
};
export const MULTIPLIER_COLORS: BandColor[] = [...DIGIT_COLORS, 'gold', 'silver'];

/** Tolerance band, in percent. The common ones only. */
export const TOLERANCE: Partial<Record<BandColor, number>> = {
  brown: 1,
  red: 2,
  green: 0.5,
  blue: 0.25,
  violet: 0.1,
  gold: 5,
  silver: 10,
};
export const TOLERANCE_COLORS: BandColor[] = ['gold', 'silver', 'brown', 'red', 'green', 'blue', 'violet'];

/** How each band is painted in the drawings. */
export const BAND_FILL: Record<BandColor, string> = {
  black: '#1a1a1a',
  brown: '#7a4a24',
  red: '#d7261e',
  orange: '#f27a12',
  yellow: '#f4d000',
  green: '#2e9e3e',
  blue: '#2563d9',
  violet: '#8a3fc9',
  grey: '#8c8c8c',
  white: '#f5f5f5',
  gold: '#c9a227',
  silver: '#c0c4c8',
};

export type BandCount = 4 | 5;

/** The band colors a reader can cycle through at position i of a resistor. */
export function choicesFor(count: BandCount, i: number): BandColor[] {
  const sig = count - 2;
  if (i < sig) return i === 0 ? DIGIT_COLORS.slice(1) : DIGIT_COLORS;
  if (i === sig) return MULTIPLIER_COLORS;
  return TOLERANCE_COLORS;
}

/** The next color in a band's cycle. */
export function nextBand(count: BandCount, i: number, current: BandColor, step = 1): BandColor {
  const list = choicesFor(count, i);
  const at = list.indexOf(current);
  return list[(((at < 0 ? 0 : at) + step) % list.length + list.length) % list.length];
}

/** Clears floating-point dust: 4.7 * 1000 and friends. */
function clean(n: number): number {
  return Number(n.toPrecision(12));
}

export interface Reading {
  ohms: number;
  tolerance: number;
  /** The lowest and highest value the part may really be. */
  min: number;
  max: number;
}

/** Reads a 4-band (2 digits) or 5-band (3 digits) resistor. */
export function readBands(bands: BandColor[]): Reading {
  if (bands.length !== 4 && bands.length !== 5) throw new Error('resistors: need 4 or 5 bands');
  const sig = bands.length - 2;
  let digits = 0;
  for (let i = 0; i < sig; i++) {
    const d = DIGIT_COLORS.indexOf(bands[i]);
    if (d < 0) throw new Error(`resistors: ${bands[i]} is not a digit color`);
    digits = digits * 10 + d;
  }
  const exp = MULTIPLIER_EXP[bands[sig]];
  const tol = TOLERANCE[bands[sig + 1]];
  if (exp === undefined) throw new Error(`resistors: ${bands[sig]} is not a multiplier color`);
  if (tol === undefined) throw new Error(`resistors: ${bands[sig + 1]} is not a tolerance color`);
  const ohms = clean(digits * 10 ** exp);
  return { ohms, tolerance: tol, min: clean(ohms * (1 - tol / 100)), max: clean(ohms * (1 + tol / 100)) };
}

export interface Banding {
  bands: BandColor[];
  /** The value those bands mean, after rounding to 2 or 3 digits. */
  ohms: number;
  /** False when the value had to be rounded to fit the bands. */
  exact: boolean;
}

/**
 * The bands for a value: 2 significant digits on a 4-band part, 3 on a
 * 5-band one, then the multiplier and the given tolerance band. Null when
 * the value is out of range (below 0.1 Ω on 4 bands, or above what white
 * ×1 GΩ can show).
 */
export function bandsFor(ohms: number, count: BandCount, tolerance: BandColor = count === 4 ? 'gold' : 'brown'): Banding | null {
  if (!Number.isFinite(ohms) || ohms <= 0) return null;
  const sig = count - 2;
  let exp = Math.floor(Math.log10(ohms)) - (sig - 1);
  let digits = Math.round(clean(ohms / 10 ** exp));
  if (digits >= 10 ** sig) {
    exp += 1;
    digits = Math.round(clean(ohms / 10 ** exp));
  }
  if (exp < -2 || exp > 9) return null;
  const bands: BandColor[] = [];
  const s = String(digits).padStart(sig, '0');
  for (const ch of s) bands.push(DIGIT_COLORS[Number(ch)]);
  const mult = MULTIPLIER_COLORS.find((c) => MULTIPLIER_EXP[c] === exp)!;
  bands.push(mult, tolerance);
  const shown = clean(digits * 10 ** exp);
  return { bands, ohms: shown, exact: Math.abs(shown - ohms) <= ohms * 1e-9 };
}

/**
 * Parses what a reader types: "220", "4.7k", "4k7", "1M", "10 kΩ",
 * "4R7", "470 ohms". Null when it isn't a resistance.
 */
export function parseOhms(text: string): number | null {
  const t = text
    .trim()
    .replace(/\s+/g, '')
    .replace(/(Ω|ohms?)$/i, '');
  // "4k7" style: the letter is the decimal point.
  let m = /^(\d+)([rkmg])(\d+)$/i.exec(t);
  if (m) return scale(Number(`${m[1]}.${m[3]}`), m[2]);
  m = /^(\d+(?:\.\d+)?|\.\d+)([rkmg])?$/i.exec(t);
  if (!m) return null;
  return scale(Number(m[1]), m[2] ?? 'r');
}
function scale(n: number, unit: string): number | null {
  const mult = { r: 1, k: 1e3, m: 1e6, g: 1e9 }[unit.toLowerCase()] ?? 1;
  const v = clean(n * mult);
  return v > 0 ? v : null;
}

// ------------------------------------------------------------------ E-series

/** IEC 60063: the E12 and E24 preferred numbers for one decade. */
export const E12 = [10, 12, 15, 18, 22, 27, 33, 39, 47, 56, 68, 82];
export const E24 = [10, 11, 12, 13, 15, 16, 18, 20, 22, 24, 27, 30, 33, 36, 39, 43, 47, 51, 56, 62, 68, 75, 82, 91];
export type Series = 'E12' | 'E24';
const SERIES: Record<Series, number[]> = { E12, E24 };

function candidates(ohms: number, series: Series): number[] {
  const d = Math.floor(Math.log10(ohms));
  const out: number[] = [];
  for (let e = d - 2; e <= d + 1; e++) for (const b of SERIES[series]) out.push(clean(b * 10 ** e));
  return out;
}

/** The series value closest to ohms (closest by ratio, as the series is spaced). */
export function nearestE(ohms: number, series: Series): number {
  let best = 0;
  let bestD = Infinity;
  for (const c of candidates(ohms, series)) {
    const d = Math.abs(Math.log(c / ohms));
    if (d < bestD - 1e-12) {
      best = c;
      bestD = d;
    }
  }
  return best;
}

/** The smallest series value at or above ohms: the safe pick for a current limiter. */
export function nextEUp(ohms: number, series: Series): number {
  return candidates(ohms, series).find((c) => c >= ohms * (1 - 1e-9))!;
}

export function isEValue(ohms: number, series: Series): boolean {
  return Math.abs(nearestE(ohms, series) - ohms) <= ohms * 1e-9;
}

// ---------------------------------------------------------------- formatting

/** Up to 3 significant digits with no trailing zeros. */
function sig3(n: number): string {
  return String(Number(n.toPrecision(3)));
}

export function formatOhms(ohms: number): string {
  if (ohms >= 1e9) return `${sig3(ohms / 1e9)} GΩ`;
  if (ohms >= 1e6) return `${sig3(ohms / 1e6)} MΩ`;
  if (ohms >= 1e3) return `${sig3(ohms / 1e3)} kΩ`;
  return `${sig3(ohms)} Ω`;
}

export function formatAmps(a: number): string {
  if (a >= 1) return `${sig3(a)} A`;
  return `${sig3(a * 1000)} mA`;
}

export function formatWatts(w: number): string {
  if (w >= 1) return `${sig3(w)} W`;
  return `${sig3(w * 1000)} mW`;
}

export function formatVolts(v: number): string {
  return `${sig3(v)} V`;
}

// ---------------------------------------------------------------- Ohm's law

/** An ordinary through-hole resistor is rated 1/4 W. */
export const QUARTER_WATT = 0.25;

export type Hookup = 'one' | 'series' | 'parallel';

export interface Part {
  ohms: number;
  volts: number;
  amps: number;
  watts: number;
  /** More than a 1/4 W part can take. */
  overRated: boolean;
}

export interface Circuit {
  totalOhms: number;
  totalAmps: number;
  parts: Part[];
}

function part(ohms: number, volts: number): Part {
  const amps = volts / ohms;
  const watts = volts * amps;
  return { ohms, volts, amps, watts, overRated: watts > QUARTER_WATT };
}

/** A source of `volts` across one resistor, two in series, or two in parallel. */
export function solve(volts: number, r1: number, r2: number, hookup: Hookup): Circuit {
  if (r1 <= 0 || r2 <= 0) throw new Error('resistors: resistance must be positive');
  if (hookup === 'one') {
    const p = part(r1, volts);
    return { totalOhms: r1, totalAmps: p.amps, parts: [p] };
  }
  if (hookup === 'series') {
    const total = r1 + r2;
    const amps = volts / total;
    return { totalOhms: total, totalAmps: amps, parts: [part(r1, amps * r1), part(r2, amps * r2)] };
  }
  const p1 = part(r1, volts);
  const p2 = part(r2, volts);
  return { totalOhms: (r1 * r2) / (r1 + r2), totalAmps: p1.amps + p2.amps, parts: [p1, p2] };
}

/** E12 values from 10 Ω to 10 kΩ, the stops on the resistance sliders. */
export const SLIDER_OHMS: number[] = [1, 2, 3].flatMap((e) => E12.map((b) => clean(b * 10 ** (e - 1)))).concat([10000]);

// --------------------------------------------------------------------- LEDs

export type LedColor = 'red' | 'yellow' | 'green' | 'blue' | 'white';

export interface LedKind {
  id: LedColor;
  name: string;
  /** Typical forward voltage at 20 mA for a plain 5 mm LED. */
  vf: number;
  /** How the lit LED is drawn. */
  light: string;
}

export const LEDS: LedKind[] = [
  { id: 'red', name: 'Red', vf: 2.0, light: '#ff3b2f' },
  { id: 'yellow', name: 'Yellow', vf: 2.1, light: '#ffd21f' },
  { id: 'green', name: 'Green', vf: 2.2, light: '#3ee05a' },
  { id: 'blue', name: 'Blue', vf: 3.2, light: '#3a8bff' },
  { id: 'white', name: 'White', vf: 3.2, light: '#f4f7ff' },
];

export function ledById(id: LedColor): LedKind {
  return LEDS.find((l) => l.id === id)!;
}

/** The most a plain 5 mm indicator LED should carry, continuously. */
export const LED_MAX_A = 0.02;
/** Above this an ESP32-C3 pin is past its default drive strength; less is better. */
export const PIN_MAX_A = 0.02;
/** An ESP32-C3 GPIO is 3.3 V when high. */
export const PIN_VOLTS = 3.3;

export type LedState =
  | { kind: 'reversed' }
  | { kind: 'too-low' }
  | { kind: 'no-resistor' }
  | { kind: 'lit'; amps: number; brightness: number };

/**
 * What an LED does on `volts` through `ohms` (null: no resistor at all),
 * the right way round or not. The model is the usual one: no current until
 * the supply passes the forward voltage, then the resistor sets it.
 */
export function ledState(volts: number, vf: number, ohms: number | null, reversed: boolean): LedState {
  if (reversed) return { kind: 'reversed' };
  if (volts <= vf) return { kind: 'too-low' };
  if (ohms === null) return { kind: 'no-resistor' };
  const amps = (volts - vf) / ohms;
  return { kind: 'lit', amps, brightness: Math.min(1, amps / LED_MAX_A) };
}

export type LedWarning = 'led-max' | 'pin' | 'power';

export interface LedResistor {
  /** The exact resistance for the target current. */
  exact: number;
  /** The next E12 value up. */
  chosen: number;
  /** The current with the chosen resistor. */
  amps: number;
  /** Power in the chosen resistor. */
  watts: number;
  warnings: LedWarning[];
}

/**
 * R = (Vsupply − Vf) / I, rounded up to the next E12 value so the current
 * comes out a little under the target, never over. Null when the supply
 * can't reach the LED's forward voltage. `fromPin` adds the ESP32 pin check.
 */
export function ledResistor(volts: number, vf: number, targetAmps: number, fromPin: boolean): LedResistor | null {
  if (volts <= vf || targetAmps <= 0) return null;
  const exact = (volts - vf) / targetAmps;
  const chosen = nextEUp(exact, 'E12');
  const amps = (volts - vf) / chosen;
  const watts = amps * amps * chosen;
  const warnings: LedWarning[] = [];
  if (amps > LED_MAX_A) warnings.push('led-max');
  if (fromPin && amps > PIN_MAX_A) warnings.push('pin');
  if (watts > QUARTER_WATT) warnings.push('power');
  return { exact, chosen, amps, watts, warnings };
}
