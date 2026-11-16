// Component guides: capacitors (#0691). The logic behind three demos:
// reading capacitor markings (CapCodeDemo), RC charging (RcChargeDemo) and
// decoupling (DecouplingDemo). Plain TypeScript so it is unit-testable
// without a DOM; the Svelte demos only draw what these functions return.

// ---------------------------------------------------------------------------
// Reading capacitor markings

/** A decoded capacitor marking. Values are kept in picofarads. */
export interface CapValue {
  pf: number;
  /** Tolerance from a trailing letter, e.g. "±10%" for K. */
  tolerance?: string;
  /** Rated voltage, when the marking includes one ("16V"). */
  volts?: number;
  /** One line explaining how the marking was read. */
  how: string;
}

export type DecodeResult = { ok: true; value: CapValue } | { ok: false; error: string };

/** EIA tolerance letters used on capacitors. */
export const TOLERANCE_LETTERS: Record<string, string> = {
  B: '±0.1 pF',
  C: '±0.25 pF',
  D: '±0.5 pF',
  F: '±1%',
  G: '±2%',
  J: '±5%',
  K: '±10%',
  M: '±20%',
  Z: '+80% / −20%',
};

/** The EIA three-digit code's third digit: 0–6 add that many zeros, 8 and 9 divide. */
export function eiaMultiplier(digit: number): number | null {
  if (digit >= 0 && digit <= 6) return 10 ** digit;
  if (digit === 8) return 0.01;
  if (digit === 9) return 0.1;
  return null;
}

const PREFIX_PF: Record<string, number> = { p: 1, n: 1e3, µ: 1e6 };

function prefixPf(ch: string): number | undefined {
  const c = ch === 'u' || ch === 'U' || ch === 'μ' ? 'µ' : ch.toLowerCase();
  return PREFIX_PF[c];
}

function ok(pf: number, how: string, extra: Partial<CapValue> = {}): DecodeResult {
  if (!(pf > 0) || !Number.isFinite(pf)) return { ok: false, error: 'A capacitor’s value has to be more than zero.' };
  return { ok: true, value: { pf, how, ...extra } };
}

/**
 * Reads a capacitor marking: a three-digit EIA code ("104", "472K"), a small
 * value printed in picofarads ("47"), R or unit-letter decimal points ("4R7",
 * "4n7"), or a value with units ("0.1µF", "100nF", "10µF 16V").
 */
export function decodeCapCode(raw: string): DecodeResult {
  let s = raw.trim().replace(/μ/g, 'µ');
  if (!s) return { ok: false, error: 'Type a marking, like 104 or 10µF 16V.' };

  // A voltage, either its own word or stuck on after the F ("10µF16V").
  let volts: number | undefined;
  const vMatch =
    s.match(/^(.*?)(?:[\s,/]+|(?<=[fF]))(\d+(?:\.\d+)?)\s*V(?:DC)?$/i) ?? s.match(/^()(\d+(?:\.\d+)?)\s*V(?:DC)?$/i);
  if (vMatch) {
    volts = Number(vMatch[2]);
    s = vMatch[1].trim();
    if (!s) return { ok: false, error: 'That is only a voltage. Add the value too, like 10µF 16V.' };
  }
  const extra = volts !== undefined ? { volts } : {};
  const c = s.replace(/\s+/g, '');

  // Three digits, maybe a tolerance letter: the EIA code.
  let m = c.match(/^(\d)(\d)(\d)([A-Za-z])?$/);
  if (m) {
    const mult = eiaMultiplier(Number(m[3]));
    if (mult === null) return { ok: false, error: `The third digit is a multiplier, and 7 isn’t used. Check the marking.` };
    const sig = Number(m[1] + m[2]);
    const pf = sig * mult;
    const tol = m[4] ? TOLERANCE_LETTERS[m[4].toUpperCase()] : undefined;
    if (m[4] && !tol) return { ok: false, error: `“${m[4]}” isn’t a tolerance letter. Common ones are J (±5%), K (±10%) and M (±20%).` };
    const zeros = Number(m[3]);
    const how =
      zeros <= 6
        ? `${m[1]}${m[2]} followed by ${zeros} zero${zeros === 1 ? '' : 's'}, in picofarads`
        : `${m[1]}${m[2]} × ${mult}, in picofarads`;
    return ok(pf, how, { ...extra, ...(tol ? { tolerance: tol } : {}) });
  }

  // One or two digits: printed straight in picofarads.
  m = c.match(/^(\d{1,2})$/);
  if (m) return ok(Number(m[1]), 'Just the value, in picofarads (small ceramics are marked this way)', extra);

  if (/^\d{4,}$/.test(c)) return { ok: false, error: 'Codes have three digits: two digits of value, then the number of zeros.' };

  // R as the decimal point: 4R7 = 4.7 pF.
  m = c.match(/^(\d*)[Rr](\d+)$/);
  if (m) return ok(Number(`${m[1] || '0'}.${m[2]}`), 'R marks the decimal point, in picofarads', extra);

  // A unit letter as the decimal point: 4n7 = 4.7 nF, 2µ2 = 2.2 µF.
  m = c.match(/^(\d+)([pPnNuUµ])(\d+)[fF]?$/);
  if (m) {
    const f = prefixPf(m[2])!;
    return ok(Number(`${m[1]}.${m[3]}`) * f, 'The unit letter marks the decimal point', extra);
  }

  // A number with units: 0.1µF, 100nF, 22pF, 1F.
  m = c.match(/^(\d+(?:\.\d+)?|\.\d+)([pPnNuUµ])?([fF])?$/);
  if (m && (m[2] || m[3])) {
    const n = Number(m[1]);
    const f = m[2] ? prefixPf(m[2])! : 1e12;
    return ok(n * f, 'The value is printed with its unit', extra);
  }
  if (m) return { ok: false, error: 'Add a unit, like 0.1µF or 0.1nF. A bare decimal could mean either.' };

  return { ok: false, error: 'Not a marking this decoder knows. Try 104, 472K, 4R7, 0.1µF or 10µF 16V.' };
}

const NUM = new Intl.NumberFormat('en-US', { maximumSignificantDigits: 4, useGrouping: false });

/** A number without exponent notation and without float noise (4700.000000001 → "4700"). */
export function fmtNum(n: number): string {
  return NUM.format(n);
}

/** The same value in pF, nF and µF, and the unit people would normally use. */
export function capUnits(pf: number): { pF: string; nF: string; uF: string; best: string } {
  const best = pf < 1000 ? `${fmtNum(pf)} pF` : pf < 1e6 ? `${fmtNum(pf / 1e3)} nF` : `${fmtNum(pf / 1e6)} µF`;
  return { pF: `${fmtNum(pf)} pF`, nF: `${fmtNum(pf / 1e3)} nF`, uF: `${fmtNum(pf / 1e6)} µF`, best };
}

/** The three-digit code a value would be printed as, or null if it has none. */
export function toEiaCode(pf: number): string | null {
  if (pf < 10) {
    const tenths = Math.round(pf * 10);
    if (Math.abs(tenths - pf * 10) > 1e-9 || tenths < 1) return null;
    return `${Math.floor(tenths / 10)}R${tenths % 10}`;
  }
  const exp = Math.floor(Math.log10(pf) + 1e-9) - 1;
  if (exp > 6) return null;
  const sig = pf / 10 ** exp;
  const rounded = Math.round(sig);
  if (Math.abs(sig - rounded) > 1e-6) return null;
  return `${rounded}${exp}`;
}

// ---------------------------------------------------------------------------
// Polarity and voltage rating

export type CapKind = 'electrolytic' | 'tantalum' | 'ceramic';

/** What the printed mark (stripe, band or "+") on each kind of capacitor means. */
export const MARK_MEANS: Record<CapKind, 'negative' | 'positive' | 'none'> = {
  electrolytic: 'negative',
  tantalum: 'positive',
  ceramic: 'none',
};

export type Orientation = 'mark-to-ground' | 'mark-to-plus';

/** Is this kind of capacitor the right way round? Ceramics have no polarity. */
export function polarityOk(kind: CapKind, o: Orientation): boolean {
  const mark = MARK_MEANS[kind];
  if (mark === 'none') return true;
  return mark === 'negative' ? o === 'mark-to-ground' : o === 'mark-to-plus';
}

export type RatingVerdict = 'ok' | 'tight' | 'over';

/**
 * A rated voltage against the rail it sits on. "ok" means at least 1.5× the
 * rail, a common rule of thumb; "tight" means above the rail but with little
 * margin; "over" means the rail exceeds the rating.
 */
export function ratingVerdict(ratedV: number, railV: number): RatingVerdict {
  if (ratedV < railV) return 'over';
  if (ratedV < railV * 1.5) return 'tight';
  return 'ok';
}

// ---------------------------------------------------------------------------
// RC charging

/** The time constant τ = R × C, in seconds. */
export function tau(ohms: number, farads: number): number {
  return ohms * farads;
}

/** Capacitor voltage t seconds after switching towards target, from v0. */
export function capVoltage(t: number, tauS: number, target: number, v0: number): number {
  if (tauS <= 0) return target;
  return target + (v0 - target) * Math.exp(-t / tauS);
}

/** How far a charge or discharge has got after n time constants, in percent. */
export function percentAfter(n: number): number {
  return (1 - Math.exp(-n)) * 100;
}

/** Seconds as µs, ms or s, to three significant figures. */
export function fmtSeconds(s: number): string {
  const f = (n: number) => new Intl.NumberFormat('en-US', { maximumSignificantDigits: 3, useGrouping: false }).format(n);
  if (s < 1e-3) return `${f(s * 1e6)} µs`;
  if (s < 1) return `${f(s * 1e3)} ms`;
  return `${f(s)} s`;
}

/** Ohms as Ω, kΩ or MΩ. */
export function fmtOhms(r: number): string {
  if (r >= 1e6) return `${fmtNum(r / 1e6)} MΩ`;
  if (r >= 1e3) return `${fmtNum(r / 1e3)} kΩ`;
  return `${fmtNum(r)} Ω`;
}

/** Current as mA or µA. */
export function fmtAmps(a: number): string {
  const abs = Math.abs(a);
  const f = (n: number) => new Intl.NumberFormat('en-US', { maximumSignificantDigits: 3, useGrouping: false }).format(n);
  if (abs < 1e-6) return '0 mA';
  if (abs < 1e-3) return `${f(a * 1e6)} µA`;
  return `${f(a * 1e3)} mA`;
}

// ---------------------------------------------------------------------------
// Decoupling: a teaching model, not a circuit simulation

/** The rail the chip runs on, and the ESP32-C3's lowest rated supply voltage. */
export const RAIL_V = 3.3;
export const MIN_V = 3.0;

/** Model constants. Typical orders of magnitude, chosen to teach, not measured. */
export const DECOUPLING_MODEL = {
  /** Inductance of the wiring back to the regulator. */
  supplyNH: 25,
  /** Resistance of that wiring. */
  supplyOhms: 0.2,
  /** The current step the chip pulls when it switches. */
  spikeA: 0.1,
  /** How fast that current rises (and falls). */
  riseNs: 5,
  /** How long each spike lasts. */
  spikeNs: 20,
  /** A small ceramic capacitor's own inductance. */
  capBodyNH: 1,
  /** Extra inductance per millimetre of trace between capacitor and pin. */
  nhPerMm: 1,
  /** Recovery time after a spike. */
  recoverNs: 15,
} as const;

/** Spike start times across the plotted window, in ns. */
export const SPIKES_NS = [40, 140, 240];
export const WINDOW_NS = 320;

export interface DecouplingOptions {
  /** Decoupling capacitance in farads, or null for none. */
  capF: number | null;
  /** Distance from the capacitor to the chip's power pin, in mm. */
  distanceMm: number;
}

/** Inductance in the capacitor's path to the pin, in nH. */
export function capPathNH(distanceMm: number): number {
  return DECOUPLING_MODEL.capBodyNH + DECOUPLING_MODEL.nhPerMm * Math.max(0, distanceMm);
}

/** The inductance the spike sees: the supply wiring, in parallel with the capacitor's path. */
export function effectiveNH(o: DecouplingOptions): number {
  const ls = DECOUPLING_MODEL.supplyNH;
  if (o.capF === null) return ls;
  const lc = capPathNH(o.distanceMm);
  return (ls * lc) / (ls + lc);
}

export interface RailDip {
  /** L × di/dt while the current rises. */
  inductiveV: number;
  /** The capacitor's own voltage drop from giving up charge (I × t / C). */
  chargeV: number;
  /** The wiring's resistance times the spike current. */
  resistiveV: number;
  /** The lowest voltage the pin sees. */
  minV: number;
}

export function railDip(o: DecouplingOptions): RailDip {
  const m = DECOUPLING_MODEL;
  const inductiveV = (effectiveNH(o) * 1e-9 * m.spikeA) / (m.riseNs * 1e-9);
  const chargeV = o.capF === null ? 0 : (m.spikeA * m.spikeNs * 1e-9) / o.capF;
  const resistiveV = m.spikeA * m.supplyOhms;
  // The inductive dip is over by the end of the rise; the charge sag builds through the spike.
  const atRise = inductiveV + resistiveV * (m.riseNs / m.spikeNs) + chargeV * (m.riseNs / m.spikeNs);
  const atEnd = resistiveV + chargeV;
  return { inductiveV, chargeV, resistiveV, minV: RAIL_V - Math.max(atRise, atEnd) };
}

/** Pin voltage at time tNs in the window. */
export function railVoltageAt(o: DecouplingOptions, tNs: number): number {
  const m = DECOUPLING_MODEL;
  const d = railDip(o);
  let v = RAIL_V;
  for (const s of SPIKES_NS) {
    const t = tNs - s;
    if (t < 0) continue;
    if (t < m.spikeNs) {
      const frac = t / m.spikeNs;
      v -= (d.resistiveV + d.chargeV) * frac;
      if (t < m.riseNs) v -= d.inductiveV;
    } else {
      const after = t - m.spikeNs;
      // Falling current kicks the rail up briefly, then the sag recovers.
      if (after < m.riseNs) v += d.inductiveV;
      v -= (d.resistiveV + d.chargeV) * Math.exp(-after / m.recoverNs);
    }
  }
  return v;
}

/** The trace sampled every stepNs across the window, as [ns, volts] pairs. */
export function railTrace(o: DecouplingOptions, stepNs = 1): [number, number][] {
  const out: [number, number][] = [];
  for (let t = 0; t <= WINDOW_NS; t += stepNs) out.push([t, railVoltageAt(o, t)]);
  return out;
}
