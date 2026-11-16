// Logic for the transistor and MOSFET component guides (#0692): an NPN
// transistor as a switch, a logic-level MOSFET as a switch, flyback diodes,
// and choosing between the two. Plain functions so they unit-test without a
// DOM; the Svelte demos only draw what these return.
//
// Numbers are datasheet values for the named parts where a datasheet gives
// one (minimum hFE, maximum Vce(sat), maximum Rds(on)). Where it doesn't,
// the value is an estimate and is marked `estimate: true` so the demo can say
// so. These are simplified teaching models, not SPICE.

/** The ESP32-C3's high output level. */
export const PIN_VOLTS = 3.3;
/** Base-emitter drop of a switched-on silicon NPN, rounded for teaching. */
export const VBE = 0.7;
/** Base current we're happy to take from one GPIO. The pin can do more, but less is better. */
export const PIN_COMFORT_A = 0.01;
/** Above this the pin is being pushed past what we'd recommend. */
export const PIN_MAX_A = 0.02;
/** Ic/Ib that datasheets use for their Vce(sat) figures: the rule of thumb for a hard switch. */
export const FORCED_GAIN = 10;

/** E12 resistor values from 100 Ω to 47 kΩ, the base-resistor choices. */
export const BASE_RESISTORS = [100, 150, 220, 330, 470, 680, 1000, 1500, 2200, 3300, 4700, 6800, 10000, 22000, 47000];

export function ohms(r: number): string {
  if (r >= 1000) {
    const k = r / 1000;
    return `${Number.isInteger(k) ? k : k.toFixed(1)} kΩ`;
  }
  return `${r} Ω`;
}

export function amps(a: number): string {
  if (a === 0) return '0 mA';
  if (a < 0.001) return `${(a * 1e6).toFixed(0)} µA`;
  if (a < 1) return `${(a * 1000).toFixed(a < 0.01 ? 1 : 0)} mA`;
  return `${a.toFixed(a < 10 ? 2 : 1)} A`;
}

export function watts(w: number): string {
  if (w < 1) return `${(w * 1000).toFixed(0)} mW`;
  return `${w.toFixed(2)} W`;
}

/** Interpolate y at x in a table of [x, y] points sorted by x, on log(x). Clamps at the ends. */
export function interpLog(table: readonly (readonly [number, number])[], x: number): number {
  if (x <= table[0][0]) return table[0][1];
  const last = table[table.length - 1];
  if (x >= last[0]) return last[1];
  for (let i = 1; i < table.length; i++) {
    const [x1, y1] = table[i];
    if (x <= x1) {
      const [x0, y0] = table[i - 1];
      const f = (Math.log(x) - Math.log(x0)) / (Math.log(x1) - Math.log(x0));
      return y0 + f * (y1 - y0);
    }
  }
  return last[1];
}

// ---------------------------------------------------------------------------
// NPN transistor as a switch

export type BjtId = '2N3904' | 'PN2222A';

export interface BjtPart {
  id: BjtId;
  label: string;
  /** Absolute maximum collector current, A. */
  icMax: number;
  /** Collector-emitter breakdown (Vceo), V. */
  vceo: number;
  /** Maximum dissipation in TO-92 at 25 °C, W. */
  pdMax: number;
  /** Minimum DC current gain against collector current [A, hFE], from the datasheet table. */
  hfeMin: readonly (readonly [number, number])[];
  /** Maximum Vce(sat) at Ic/Ib = 10, [A, V]. The end points marked in `vceSatEstimated` are extrapolated. */
  vceSat: readonly (readonly [number, number])[];
  /** TO-92 pins left to right, flat face toward you, legs down. */
  pins: readonly ['E' | 'B' | 'C', 'E' | 'B' | 'C', 'E' | 'B' | 'C'];
  pinNote: string;
}

export const BJTS: Record<BjtId, BjtPart> = {
  '2N3904': {
    id: '2N3904',
    label: '2N3904',
    icMax: 0.2,
    vceo: 40,
    pdMax: 0.625,
    // onsemi 2N3904: hFE min 40 @ 0.1 mA, 70 @ 1 mA, 100 @ 10 mA, 60 @ 50 mA, 30 @ 100 mA (Vce = 1 V).
    hfeMin: [
      [0.0001, 40],
      [0.001, 70],
      [0.01, 100],
      [0.05, 60],
      [0.1, 30],
    ],
    // Vce(sat) max 0.2 V @ 10 mA, 0.3 V @ 50 mA (Ib = Ic/10). 1 mA and 200 mA points are estimates.
    vceSat: [
      [0.001, 0.1],
      [0.01, 0.2],
      [0.05, 0.3],
      [0.2, 0.6],
    ],
    pins: ['E', 'B', 'C'],
    pinNote: 'E-B-C on most TO-92 2N3904s. Check your maker’s datasheet.',
  },
  PN2222A: {
    id: 'PN2222A',
    label: '2N2222A (PN2222A)',
    icMax: 0.6,
    vceo: 40,
    pdMax: 0.625,
    // PN2222A: hFE min 35 @ 0.1 mA, 50 @ 1 mA, 75 @ 10 mA, 50 @ 150 mA (Vce = 1 V), 40 @ 500 mA (Vce = 10 V).
    hfeMin: [
      [0.0001, 35],
      [0.001, 50],
      [0.01, 75],
      [0.15, 50],
      [0.5, 40],
    ],
    // Vce(sat) max 0.3 V @ 150 mA, 1.0 V @ 500 mA (Ib = Ic/10). 1 mA and 600 mA points are estimates.
    vceSat: [
      [0.001, 0.1],
      [0.15, 0.3],
      [0.5, 1.0],
      [0.6, 1.2],
    ],
    pins: ['E', 'B', 'C'],
    pinNote: 'E-B-C on the PN2222A. The P2N2222A is C-B-E, and the metal-can 2N2222A is different again. Check the datasheet for the part you have.',
  },
};

export const LOAD_CURRENTS = [0.01, 0.02, 0.05, 0.1, 0.15, 0.2, 0.3, 0.5];

export type BjtState = 'off' | 'saturated' | 'marginal' | 'linear';

export interface BjtResult {
  state: BjtState;
  /** Base current from the pin, A. */
  ib: number;
  /** Collector (load) current, A. */
  ic: number;
  /** Current the load would draw with a perfect switch, A. */
  icWanted: number;
  /** Ic/Ib needed to carry the load. */
  gainNeeded: number;
  /** The datasheet's minimum hFE at this current. */
  hfeMin: number;
  /** Collector-emitter voltage, V. */
  vce: number;
  /** Heat in the transistor, W. */
  power: number;
  /** Supply minus Vce: what the load gets, V. */
  vLoad: number;
  pinLevel: 'ok' | 'high' | 'too-high';
  overCurrent: boolean;
  overPower: boolean;
  /** The largest E12 base resistor that still gives Ic/Ib ≤ 10 for this load. Undefined when none fits the pin. */
  suggestedRb?: number;
}

/**
 * A 3.3 V pin driving an NPN through `rb`, switching a resistive load on a
 * `supply`-volt rail that draws `loadA` through a perfect switch.
 */
export function bjtSwitch(part: BjtPart, pinHigh: boolean, rb: number, loadA: number, supply = 5): BjtResult {
  const rLoad = supply / loadA;
  const suggestedRb = suggestBaseResistor(loadA);
  const base = { icWanted: loadA, suggestedRb, overCurrent: false, overPower: false };
  if (!pinHigh) {
    return { ...base, state: 'off', ib: 0, ic: 0, gainNeeded: 0, hfeMin: interpLog(part.hfeMin, loadA), vce: supply, power: 0, vLoad: 0, pinLevel: 'ok' };
  }
  const ib = (PIN_VOLTS - VBE) / rb;
  const pinLevel = ib > PIN_MAX_A ? 'too-high' : ib > PIN_COMFORT_A ? 'high' : 'ok';
  const vSat = interpLog(part.vceSat, loadA);
  const icSat = (supply - vSat) / rLoad;
  const hfe = interpLog(part.hfeMin, icSat);
  const gainNeeded = icSat / ib;
  let state: BjtState;
  let ic: number;
  let vce: number;
  if (ib * hfe < icSat) {
    // Not enough base current: the transistor only passes hFE × Ib, and the
    // rest of the supply drops across it.
    state = 'linear';
    ic = ib * hfe;
    vce = supply - ic * rLoad;
  } else {
    state = gainNeeded <= FORCED_GAIN ? 'saturated' : 'marginal';
    ic = icSat;
    vce = vSat;
  }
  const power = vce * ic + VBE * ib;
  return {
    ...base,
    state,
    ib,
    ic,
    gainNeeded,
    hfeMin: hfe,
    vce,
    power,
    vLoad: supply - vce,
    pinLevel,
    overCurrent: ic > part.icMax,
    overPower: power > part.pdMax,
  };
}

/** Largest E12 value (100 Ω–47 kΩ) giving Ib ≥ Ic/10, or undefined if that needs more than the pin should give. */
export function suggestBaseResistor(loadA: number, drive = PIN_VOLTS): number | undefined {
  const ibNeeded = loadA / FORCED_GAIN;
  if (ibNeeded > PIN_MAX_A) return undefined;
  const maxR = (drive - VBE) / ibNeeded;
  let best: number | undefined;
  for (const r of BASE_RESISTORS) if (r <= maxR) best = r;
  if (best !== undefined && (drive - VBE) / best > PIN_MAX_A) return undefined;
  return best;
}

// ---------------------------------------------------------------------------
// N-channel MOSFET as a low-side switch

export type MosfetId = 'AO3400A' | 'IRLZ44N' | 'IRFZ44N';

export interface MosfetPoint {
  /** Gate-source voltage, V. */
  vgs: number;
  /** On-resistance at this Vgs, Ω. */
  rds: number;
  /** Roughly the most current the channel will pass at this Vgs, A. Infinity when the package rating is the limit. */
  idLimit: number;
  /** True when this point is not a datasheet guarantee. */
  estimate: boolean;
}

export interface MosfetPart {
  id: MosfetId;
  label: string;
  pkg: string;
  logicLevel: boolean;
  vdsMax: number;
  idMax: number;
  /** Gate threshold range, V. */
  vth: readonly [number, number];
  /** Junction-to-ambient thermal resistance with no heatsink, °C/W. */
  rthJA: number;
  points: readonly MosfetPoint[];
}

export const MOSFETS: Record<MosfetId, MosfetPart> = {
  AO3400A: {
    id: 'AO3400A',
    label: 'AO3400A',
    pkg: 'SOT-23 (surface mount)',
    logicLevel: true,
    vdsMax: 30,
    idMax: 5.7,
    vth: [0.65, 1.45],
    rthJA: 125,
    points: [
      { vgs: 2.5, rds: 0.048, idLimit: Infinity, estimate: false },
      { vgs: 3.3, rds: 0.04, idLimit: Infinity, estimate: true },
      { vgs: 4.5, rds: 0.032, idLimit: Infinity, estimate: false },
      { vgs: 5, rds: 0.031, idLimit: Infinity, estimate: true },
      { vgs: 10, rds: 0.0265, idLimit: Infinity, estimate: false },
    ],
  },
  IRLZ44N: {
    id: 'IRLZ44N',
    label: 'IRLZ44N',
    pkg: 'TO-220 (through-hole)',
    logicLevel: true,
    vdsMax: 55,
    idMax: 47,
    vth: [1, 2],
    rthJA: 62,
    points: [
      { vgs: 3.3, rds: 0.05, idLimit: 10, estimate: true },
      { vgs: 4, rds: 0.035, idLimit: Infinity, estimate: false },
      { vgs: 5, rds: 0.025, idLimit: Infinity, estimate: false },
      { vgs: 10, rds: 0.022, idLimit: Infinity, estimate: false },
    ],
  },
  IRFZ44N: {
    id: 'IRFZ44N',
    label: 'IRFZ44N',
    pkg: 'TO-220 (through-hole)',
    logicLevel: false,
    vdsMax: 55,
    idMax: 49,
    vth: [2, 4],
    rthJA: 62,
    points: [
      { vgs: 3.3, rds: 2, idLimit: 0.05, estimate: true },
      { vgs: 5, rds: 0.1, idLimit: 3, estimate: true },
      { vgs: 10, rds: 0.0175, idLimit: Infinity, estimate: false },
    ],
  },
};

export const GATE_DRIVES = [3.3, 5, 10] as const;
export const MOSFET_LOADS = [0.1, 0.25, 0.5, 1, 2, 3, 5];

export function mosfetPoint(part: MosfetPart, vgs: number): MosfetPoint | undefined {
  return part.points.find((p) => p.vgs === vgs);
}

export type GateState = 'low' | 'high' | 'floating';
export type MosfetState = 'off' | 'on' | 'partly' | 'unknown';

export interface MosfetResult {
  state: MosfetState;
  vgs: number;
  /** Drain current (load current), A. */
  id: number;
  idWanted: number;
  vds: number;
  /** Rds(on) at this Vgs, or the resistance it is acting like when only partly on. */
  rds: number;
  rdsEstimate: boolean;
  power: number;
  /** Junction temperature at 25 °C ambient with no heatsink. */
  tj: number;
  heat: 'cool' | 'warm' | 'hot' | 'too-hot';
  overCurrent: boolean;
  overVoltage: boolean;
}

/** Heat verdict from junction temperature, °C. */
export function heatLevel(tj: number): MosfetResult['heat'] {
  if (tj <= 60) return 'cool';
  if (tj <= 100) return 'warm';
  if (tj <= 150) return 'hot';
  return 'too-hot';
}

/**
 * A MOSFET switching a resistive load that draws `loadA` from `supply` volts.
 * The gate is driven to `drive` volts when high, 0 when low, and floats
 * (unknown) during boot unless a pull-down holds it low.
 */
export function mosfetSwitch(part: MosfetPart, gate: GateState, drive: number, pulldown: boolean, loadA: number, supply = 5): MosfetResult {
  const rLoad = supply / loadA;
  const base = { idWanted: loadA, overVoltage: supply > part.vdsMax };
  const off: MosfetResult = { ...base, state: 'off', vgs: 0, id: 0, vds: supply, rds: Infinity, rdsEstimate: false, power: 0, tj: 25, heat: 'cool', overCurrent: false };
  if (gate === 'low' || (gate === 'floating' && pulldown)) return off;
  if (gate === 'floating') return { ...off, state: 'unknown', vgs: NaN };
  const pt = mosfetPoint(part, drive);
  if (!pt) throw new Error(`mosfet: no point for ${part.id} at ${drive} V`);
  let id = supply / (rLoad + pt.rds);
  let state: MosfetState = 'on';
  let vds: number;
  if (id > pt.idLimit) {
    // The channel can't pass the full current at this gate voltage: it limits
    // the current and the rest of the supply drops across it, as heat.
    id = pt.idLimit;
    vds = supply - id * rLoad;
    state = 'partly';
  } else {
    vds = id * pt.rds;
  }
  const power = id * vds;
  const tj = 25 + power * part.rthJA;
  return {
    ...base,
    state,
    vgs: drive,
    id,
    vds,
    rds: state === 'partly' ? vds / id : pt.rds,
    rdsEstimate: pt.estimate || state === 'partly',
    power,
    tj,
    heat: heatLevel(tj),
    overCurrent: id > part.idMax,
  };
}

// ---------------------------------------------------------------------------
// Flyback: what an inductive load does at switch-off

export type CoilId = 'relay' | 'motor' | 'solenoid';
export type DiodeMode = 'none' | 'correct' | 'reversed';

export interface Coil {
  id: CoilId;
  label: string;
  supply: number;
  /** Coil resistance, Ω. */
  r: number;
  /** Coil inductance, H. A teaching value, not a datasheet one. */
  l: number;
}

export const COILS: Record<CoilId, Coil> = {
  relay: { id: 'relay', label: '5 V relay', supply: 5, r: 70, l: 0.05 },
  motor: { id: 'motor', label: 'Small DC motor', supply: 5, r: 20, l: 0.002 },
  solenoid: { id: 'solenoid', label: '12 V solenoid', supply: 12, r: 24, l: 0.1 },
};

/** Forward drop of a 1N4148 or 1N4007 flyback diode, V. */
export const DIODE_VF = 0.7;
/** The switch is a 2N2222A: its Vceo is what the spike breaks through. */
export const SWITCH_RATING = 40;
/** Stray capacitance at the collector in the teaching model, F. */
export const STRAY_C = 100e-12;
/** Collector voltage while the transistor is on, V. */
export const ON_VCE = 0.2;

export interface FlybackResult {
  /** Coil current at the moment of switch-off, A. */
  i0: number;
  /** Energy stored in the coil, J. */
  energy: number;
  /** Peak collector voltage after switch-off, V. */
  peak: number;
  /** The spike the coil would make if nothing broke down first, V. */
  unclamped: number;
  /** Time constant of the current dying away through the diode, s. */
  tau: number;
  /** The spike goes past the transistor's rating. */
  overRating: boolean;
  /** A reversed diode is forward-biased across the supply whenever the transistor is on. */
  shortsWhenOn: boolean;
}

export function flyback(coil: Coil, diode: DiodeMode): FlybackResult {
  const i0 = coil.supply / coil.r;
  const energy = 0.5 * coil.l * i0 * i0;
  const unclamped = coil.supply + i0 * Math.sqrt(coil.l / STRAY_C);
  const tau = coil.l / coil.r;
  if (diode === 'correct') {
    return { i0, energy, peak: coil.supply + DIODE_VF, unclamped, tau, overRating: false, shortsWhenOn: false };
  }
  // No diode, or one the wrong way round (reverse-biased by the spike, so it
  // doesn't clamp): the voltage climbs until the transistor breaks down.
  const peak = Math.min(unclamped, SWITCH_RATING);
  return { i0, energy, peak, unclamped, tau, overRating: unclamped > SWITCH_RATING, shortsWhenOn: diode === 'reversed' };
}

/**
 * Collector voltage and coil current against time after switch-off, as
 * `n + 1` samples over five diode time constants. A teaching model: with no
 * clamp the spike is drawn as a fast rise to the peak that dies away in the
 * first few percent of the window.
 */
export function flybackTrace(coil: Coil, diode: DiodeMode, n = 100): { t: number; v: number; i: number }[] {
  const r = flyback(coil, diode);
  const window = 5 * r.tau;
  const out: { t: number; v: number; i: number }[] = [];
  for (let k = 0; k <= n; k++) {
    const t = (k / n) * window;
    if (diode === 'correct') {
      const i = r.i0 * Math.exp(-t / r.tau);
      out.push({ t, i, v: i > 0.02 * r.i0 ? coil.supply + DIODE_VF : coil.supply });
    } else {
      const x = k / n;
      const v = k === 0 ? r.peak : coil.supply + (r.peak - coil.supply) * Math.exp(-x / 0.015);
      const i = k === 0 ? r.i0 : r.i0 * Math.exp(-x / 0.01);
      out.push({ t, i, v });
    }
  }
  return out;
}

// ---------------------------------------------------------------------------
// BJT or MOSFET?

export const PICK_CURRENTS = [0.02, 0.05, 0.1, 0.2, 0.5, 1, 2, 3, 5, 10];
export const PICK_SUPPLIES = [5, 12, 24, 48];
export const PICK_DRIVES = [3.3, 5] as const;

export type Pick = 'either' | 'mosfet' | 'neither';

export interface PickResult {
  pick: Pick;
  headline: string;
  part: string;
  reasons: string[];
  bjt: { ok: boolean; ib: number; loss: number; why: string };
  mosfet: { ok: boolean; part: MosfetId; loss: number; tj: number; why: string };
}

/** Recommend a switch for `loadA` on a `supply`-volt rail, driven from a `drive`-volt pin. */
export function pickSwitch(loadA: number, supply: number, drive: number, throughHole: boolean): PickResult {
  // BJT: 2N2222A (PN2222A) at Ic/Ib = 10.
  const p2222 = BJTS.PN2222A;
  const ib = loadA / FORCED_GAIN;
  const bjtLoss = loadA * interpLog(p2222.vceSat, loadA);
  let bjtWhy: string;
  let bjtOk = false;
  if (supply > p2222.vceo) bjtWhy = `${supply} V is more than its 40 V rating.`;
  else if (loadA > p2222.icMax) bjtWhy = `${amps(loadA)} is more than its ${amps(p2222.icMax)} rating.`;
  else if (ib > PIN_MAX_A) bjtWhy = `It would need ${amps(ib)} of base current, more than a pin should give.`;
  else if (ib > PIN_COMFORT_A) bjtWhy = `It would need ${amps(ib)} from the pin, more than we’d like to take.`;
  else {
    bjtOk = true;
    bjtWhy = `${amps(ib)} from the pin through a ${ohms(suggestBaseResistor(loadA, drive) ?? 1000)} base resistor.`;
  }

  // MOSFET: logic-level only at these gate voltages. Through-hole means the
  // IRLZ44N; otherwise the small AO3400A first, then the IRLZ44N when the
  // AO3400A would run too hot or its 30 V rating is too close.
  const candidates: MosfetId[] = throughHole ? ['IRLZ44N'] : ['AO3400A', 'IRLZ44N'];
  let fetId: MosfetId = candidates[0];
  let fetLoss = 0;
  let tj = 25;
  let fetOk = false;
  let fetWhy = '';
  for (const id of candidates) {
    const fet = MOSFETS[id];
    const pt = mosfetPoint(fet, drive)!;
    fetId = id;
    fetLoss = loadA * loadA * pt.rds;
    tj = 25 + fetLoss * fet.rthJA;
    if (supply > fet.vdsMax * 0.8) fetWhy = `${supply} V is too close to the ${fet.label}’s ${fet.vdsMax} V rating.`;
    else if (loadA > pt.idLimit) fetWhy = `At ${drive} V on the gate the ${fet.label} won’t fully turn on at ${amps(loadA)}.`;
    else if (tj > 125) fetWhy = `The ${fet.label} would reach about ${Math.round(tj)} °C without a heatsink.`;
    else {
      fetOk = true;
      fetWhy = `The ${fet.label} turns fully on at ${drive} V and loses ${watts(fetLoss)} as heat.`;
      break;
    }
  }
  const fet = MOSFETS[fetId];

  if (!bjtOk && !fetOk) {
    return {
      pick: 'neither',
      headline: 'Beyond a simple switch',
      part: 'A bigger MOSFET with a heatsink, a gate driver, or a relay',
      reasons: [bjtWhy, fetWhy],
      bjt: { ok: false, ib, loss: bjtLoss, why: bjtWhy },
      mosfet: { ok: false, part: fetId, loss: fetLoss, tj, why: fetWhy },
    };
  }
  const bjt = { ok: bjtOk, ib, loss: bjtLoss, why: bjtWhy };
  const mosfet = { ok: fetOk, part: fetId, loss: fetLoss, tj, why: fetWhy };
  if (bjtOk && fetOk) {
    return {
      pick: 'either',
      headline: 'Either works',
      part: `2N2222A or ${fet.label}`,
      reasons: ['At this current a transistor is cheap and simple, and a MOSFET wastes less power.', bjtWhy, fetWhy],
      bjt,
      mosfet,
    };
  }
  // Every case a transistor handles here, a logic-level MOSFET handles too,
  // so the only one-sided answer is the MOSFET.
  return {
    pick: 'mosfet',
    headline: 'Use a logic-level MOSFET',
    part: fet.label,
    reasons: [fetWhy, `Not a transistor: ${bjtWhy.charAt(0).toLowerCase()}${bjtWhy.slice(1)}`],
    bjt,
    mosfet,
  };
}
