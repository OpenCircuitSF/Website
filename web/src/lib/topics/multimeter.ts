// Two multimeters for the multimeter guides (#0666), and the plain logic the
// demos run on: which jack each probe belongs in, and what the display shows
// for a given dial setting, probe placement and thing being measured.
//
// Every coordinate is in the 1x image's own pixel space, the same way
// supermini.ts PAD_GEOMETRY maps the SuperMini photos. The images are SVG
// drawings (assets/guides/multimeter/, rendered by
// scripts/render-guide-art.sh). To swap in a photo, change `image` and move
// `jacks`, `lcd` and `dial` to match it; the logic does not change.

export type MeterId = 'a' | 'b';
export type Probe = 'black' | 'red';

/**
 * What a jack is for. `vma` is the 3-jack meter's shared jack: volts, ohms
 * and small currents.
 */
export type JackRole = 'com' | 'v' | 'vma' | 'ma' | 'a';

export interface Jack {
  id: string;
  /** As printed on the meter. */
  label: string;
  role: JackRole;
  ring: 'black' | 'red';
  /** Fuse rating in amps, or null for an unfused jack. */
  fuseAmps: number | null;
  x: number;
  y: number;
}

export type DialFn = 'off' | 'dcv' | 'acv' | 'ohm' | 'cont' | 'dca' | 'hfe' | 'hz';

/** One display range: readings are shown as value/scale in `unit`. */
export interface Scale {
  unit: string;
  scale: number;
  decimals: number;
  /** Largest value (in `unit`) the display can show. */
  max: number;
}

export interface DialPos {
  id: string;
  fn: DialFn;
  /** As printed next to the dial. */
  label: string;
  /** Spoken name for screen readers and messages. */
  name: string;
  /** Degrees clockwise from straight up. */
  angle: number;
  /** One scale on a manual meter; several, smallest first, on an auto-ranging one. */
  ranges: Scale[];
  /** For current settings: the jack red must be in. */
  jack?: string;
}

export interface MeterDef {
  id: MeterId;
  name: string;
  short: string;
  image: { src: string; src2x: string; width: number; height: number; alt: string };
  autoRanging: boolean;
  /** What the display shows over range: a lone 1 on budget meters, OL on others. */
  overRange: '1' | 'OL';
  /** Continuity beeps below this many ohms. */
  beepBelow: number;
  lcd: { x: number; y: number; w: number; h: number };
  dial: { cx: number; cy: number; knobR: number; labelR: number; positions: DialPos[] };
  jacks: Jack[];
}

const V = (decimals: number, max: number): Scale => ({ unit: 'V', scale: 1, decimals, max });
const mV = (decimals: number, max: number): Scale => ({ unit: 'mV', scale: 1e-3, decimals, max });
const A = (decimals: number, max: number): Scale => ({ unit: 'A', scale: 1, decimals, max });
const mA = (decimals: number, max: number): Scale => ({ unit: 'mA', scale: 1e-3, decimals, max });
const uA = (decimals: number, max: number): Scale => ({ unit: 'µA', scale: 1e-6, decimals, max });
const ohm = (decimals: number, max: number): Scale => ({ unit: 'Ω', scale: 1, decimals, max });
const kohm = (decimals: number, max: number): Scale => ({ unit: 'kΩ', scale: 1e3, decimals, max });
const Mohm = (decimals: number, max: number): Scale => ({ unit: 'MΩ', scale: 1e6, decimals, max });

export const METERS: Record<MeterId, MeterDef> = {
  a: {
    id: 'a',
    name: 'Budget 3-jack meter (manual range)',
    short: '3-jack',
    image: {
      src: '/img/guides/multimeter/meter-a.png',
      src2x: '/img/guides/multimeter/meter-a@2x.png',
      width: 340,
      height: 612,
      alt: 'A yellow budget multimeter with a display, a rotary dial of manual ranges, and three jacks: 10A, COM and VΩmA',
    },
    autoRanging: false,
    overRange: '1',
    beepBelow: 50,
    lcd: { x: 58, y: 68, w: 224, h: 92 },
    dial: {
      cx: 170,
      cy: 338,
      knobR: 66,
      labelR: 102,
      positions: [
        { id: 'off', fn: 'off', label: 'OFF', name: 'Off', angle: 0, ranges: [] },
        { id: 'acv600', fn: 'acv', label: '600', name: 'AC volts, 600 V range', angle: 18, ranges: [V(0, 600)] },
        { id: 'acv200', fn: 'acv', label: '200', name: 'AC volts, 200 V range', angle: 36, ranges: [V(1, 199.9)] },
        { id: 'hfe', fn: 'hfe', label: 'hFE', name: 'Transistor gain (hFE)', angle: 54, ranges: [{ unit: '', scale: 1, decimals: 0, max: 1999 }] },
        { id: 'cont', fn: 'cont', label: 'beep', name: 'Continuity and diode (beep)', angle: 72, ranges: [ohm(1, 199.9)] },
        { id: '10a', fn: 'dca', label: '10A', name: 'DC amps, 10 A range', angle: 90, ranges: [A(2, 19.99)], jack: '10a' },
        { id: 'dca200m', fn: 'dca', label: '200m', name: 'DC current, 200 mA range', angle: 108, ranges: [mA(1, 199.9)], jack: 'vma' },
        { id: 'dca20m', fn: 'dca', label: '20m', name: 'DC current, 20 mA range', angle: 126, ranges: [mA(2, 19.99)], jack: 'vma' },
        { id: 'dca2m', fn: 'dca', label: '2m', name: 'DC current, 2 mA range', angle: 144, ranges: [mA(3, 1.999)], jack: 'vma' },
        { id: 'dca200u', fn: 'dca', label: '200µ', name: 'DC current, 200 µA range', angle: 162, ranges: [uA(1, 199.9)], jack: 'vma' },
        { id: 'ohm2M', fn: 'ohm', label: '2M', name: 'Resistance, 2 MΩ range', angle: 180, ranges: [Mohm(3, 1.999)] },
        { id: 'ohm200k', fn: 'ohm', label: '200k', name: 'Resistance, 200 kΩ range', angle: 198, ranges: [kohm(1, 199.9)] },
        { id: 'ohm20k', fn: 'ohm', label: '20k', name: 'Resistance, 20 kΩ range', angle: 216, ranges: [kohm(2, 19.99)] },
        { id: 'ohm2k', fn: 'ohm', label: '2k', name: 'Resistance, 2 kΩ range', angle: 234, ranges: [kohm(3, 1.999)] },
        { id: 'ohm200', fn: 'ohm', label: '200', name: 'Resistance, 200 Ω range', angle: 252, ranges: [ohm(1, 199.9)] },
        { id: 'dcv200m', fn: 'dcv', label: '200m', name: 'DC volts, 200 mV range', angle: 270, ranges: [mV(1, 199.9)] },
        { id: 'dcv2', fn: 'dcv', label: '2', name: 'DC volts, 2 V range', angle: 288, ranges: [V(3, 1.999)] },
        { id: 'dcv20', fn: 'dcv', label: '20', name: 'DC volts, 20 V range', angle: 306, ranges: [V(2, 19.99)] },
        { id: 'dcv200', fn: 'dcv', label: '200', name: 'DC volts, 200 V range', angle: 324, ranges: [V(1, 199.9)] },
        { id: 'dcv600', fn: 'dcv', label: '600', name: 'DC volts, 600 V range', angle: 342, ranges: [V(0, 600)] },
      ],
    },
    jacks: [
      { id: '10a', label: '10A', role: 'a', ring: 'red', fuseAmps: null, x: 80, y: 540 },
      { id: 'com', label: 'COM', role: 'com', ring: 'black', fuseAmps: null, x: 170, y: 540 },
      { id: 'vma', label: 'VΩmA', role: 'vma', ring: 'red', fuseAmps: 0.2, x: 260, y: 540 },
    ],
  },
  b: {
    id: 'b',
    name: '4-jack auto-ranging meter',
    short: '4-jack',
    image: {
      src: '/img/guides/multimeter/meter-b.png',
      src2x: '/img/guides/multimeter/meter-b@2x.png',
      width: 380,
      height: 700,
      alt: 'An orange auto-ranging multimeter with a display, four buttons, a rotary dial, and four jacks: A, mA µA, COM and VΩHz',
    },
    autoRanging: true,
    overRange: 'OL',
    beepBelow: 30,
    lcd: { x: 58, y: 72, w: 264, h: 126 },
    dial: {
      cx: 190,
      cy: 422,
      knobR: 72,
      labelR: 116,
      positions: [
        { id: 'off', fn: 'off', label: 'OFF', name: 'Off', angle: 0, ranges: [] },
        { id: 'acv', fn: 'acv', label: 'V~', name: 'AC volts', angle: 36, ranges: [V(3, 5.999), V(2, 59.99), V(1, 599.9), V(0, 750)] },
        { id: 'dcv', fn: 'dcv', label: 'V⎓', name: 'DC volts', angle: 72, ranges: [V(3, 5.999), V(2, 59.99), V(1, 599.9), V(0, 1000)] },
        { id: 'mv', fn: 'dcv', label: 'mV', name: 'DC millivolts', angle: 108, ranges: [mV(1, 599.9)] },
        { id: 'ohm', fn: 'ohm', label: 'Ω', name: 'Resistance', angle: 144, ranges: [ohm(1, 599.9), kohm(3, 5.999), kohm(2, 59.99), kohm(1, 599.9), Mohm(3, 5.999), Mohm(2, 59.99)] },
        { id: 'cont', fn: 'cont', label: 'beep', name: 'Continuity and diode (beep)', angle: 180, ranges: [ohm(1, 599.9)] },
        { id: 'hz', fn: 'hz', label: 'Hz%', name: 'Frequency', angle: 216, ranges: [{ unit: 'Hz', scale: 1, decimals: 2, max: 59.99 }] },
        { id: 'ua', fn: 'dca', label: 'µA', name: 'DC microamps', angle: 252, ranges: [uA(1, 599.9), uA(0, 6000)], jack: 'ma' },
        { id: 'ma', fn: 'dca', label: 'mA', name: 'DC milliamps', angle: 288, ranges: [mA(2, 59.99), mA(1, 599.9)], jack: 'ma' },
        { id: 'a', fn: 'dca', label: 'A', name: 'DC amps', angle: 324, ranges: [A(3, 5.999), A(2, 10)], jack: 'a' },
      ],
    },
    jacks: [
      { id: 'a', label: 'A', role: 'a', ring: 'red', fuseAmps: 10, x: 70, y: 616 },
      { id: 'ma', label: 'mA µA', role: 'ma', ring: 'red', fuseAmps: 0.6, x: 150, y: 616 },
      { id: 'com', label: 'COM', role: 'com', ring: 'black', fuseAmps: null, x: 230, y: 616 },
      { id: 'v', label: 'VΩHz', role: 'v', ring: 'red', fuseAmps: null, x: 310, y: 616 },
    ],
  },
};

export function jackById(m: MeterDef, id: string | null): Jack | undefined {
  return m.jacks.find((j) => j.id === id);
}

export function dialById(m: MeterDef, id: string): DialPos {
  const p = m.dial.positions.find((d) => d.id === id);
  if (!p) throw new Error(`multimeter: no dial position ${id} on meter ${m.id}`);
  return p;
}

/** Where a dial label sits on the image, for its hotspot. */
export function dialPoint(m: MeterDef, p: DialPos): [number, number] {
  const t = (p.angle * Math.PI) / 180;
  const { cx, cy, labelR } = m.dial;
  return [Math.round((cx + labelR * Math.sin(t)) * 10) / 10, Math.round((cy - labelR * Math.cos(t)) * 10) / 10];
}

/** The jack red goes in for everything except current. */
export function voltsJack(m: MeterDef): Jack {
  return m.jacks.find((j) => j.role === 'v' || j.role === 'vma')!;
}

/** Where a jack sits in the row, in words: "in the middle", "third from the left". */
export function jackPlace(m: MeterDef, id: string): string {
  const order = [...m.jacks].sort((a, b) => a.x - b.x);
  const i = order.findIndex((j) => j.id === id);
  const n = order.length;
  if (n % 2 === 1 && i === (n - 1) / 2) return 'in the middle';
  if (i === 0) return 'on the far left';
  if (i === n - 1) return 'on the far right';
  const words = ['first', 'second', 'third', 'fourth'];
  return i < n / 2 ? `${words[i]} from the left` : `${words[n - 1 - i]} from the right`;
}

// ---------------------------------------------------------------------------
// Probes: which jack each one belongs in.

export interface Feedback {
  ok: boolean;
  title: string;
  body: string;
}

/**
 * Feedback for plugging `probe` into `jackId`, for the probes guide's
 * everyday setup (volts, ohms, continuity). `blackIn` is where black already is.
 */
export function probeFeedback(m: MeterDef, probe: Probe, jackId: string, blackIn: string | null = null): Feedback {
  const j = jackById(m, jackId);
  if (!j) throw new Error(`multimeter: no jack ${jackId}`);
  const com = jackById(m, 'com')!;
  const vj = voltsJack(m);
  if (probe === 'black') {
    if (j.role === 'com')
      return {
        ok: true,
        title: 'Black goes in COM.',
        body: 'COM means common. It is the reference point every reading is measured from. Black goes here on every meter, for every measurement. Now plug in red.',
      };
    if (j.role === 'v' || j.role === 'vma')
      return {
        ok: false,
        title: 'That is the red probe’s jack.',
        body: `${j.label} is where red goes. Black always goes in COM, even when the labels are hard to read. On this meter COM is the jack ${jackPlace(m, com.id)}, with the black ring.`,
      };
    return {
      ok: false,
      title: 'That jack is only for current.',
      body: `${j.label} is used only when you measure current. Black never goes there. Black always goes in COM, the jack ${jackPlace(m, com.id)}.`,
    };
  }
  if (jackId === blackIn)
    return { ok: false, title: 'Black is already there.', body: 'Each jack takes one probe. Red goes in a different jack.' };
  if (j.role === 'com')
    return {
      ok: false,
      title: 'COM is black’s jack.',
      body: 'Black goes in COM. Red goes in one of the other jacks, and for almost everything that is the one marked V.',
    };
  if (j.role === 'vma')
    return {
      ok: true,
      title: `Red goes in ${j.label}.`,
      body: 'On a 3-jack meter one jack does volts (V), resistance (Ω) and small currents (mA). Leave red here for almost everything you measure.',
    };
  if (j.role === 'v')
    return {
      ok: true,
      title: `Red goes in ${j.label}.`,
      body: 'This jack handles volts (V), resistance (Ω) and continuity. Leave red here for everything except current.',
    };
  const fuse = j.fuseAmps !== null
    ? 'Touch the probes across a battery with red here and you short it through the meter.'
    : 'On this meter it has no fuse, so a mistake here can damage the meter or the battery.';
  return {
    ok: false,
    title: j.role === 'ma' ? 'That is the small-current jack.' : 'That is the big-current jack.',
    body: `${j.label} is only for measuring current, with the meter put into the circuit. Inside, it is almost a plain wire to COM. ${fuse} Put red in ${vj.label}, and move it here only when you measure current.`,
  };
}

// ---------------------------------------------------------------------------
// Measuring: what the display shows.

/** Something to touch the probes to. */
export type Target =
  /** A DC voltage source, probes across it: a battery or a board pin. */
  | { kind: 'source'; id: string; name: string; volts: number }
  /** Something unpowered: a wire, a switch, a resistor. Infinity is open. */
  | { kind: 'resistance'; id: string; name: string; ohms: number }
  /** A powered circuit broken open, with the meter bridging the gap. */
  | { kind: 'series'; id: string; name: string; supply: number; amps: number };

export interface Setup {
  meter: MeterDef;
  dial: string;
  black: string | null;
  red: string | null;
  /** Probes touched the wrong way round. */
  reversed?: boolean;
  /** Jacks whose fuse has blown. */
  blown?: readonly string[];
}

export type Tone = 'ok' | 'info' | 'warn' | 'bad';

export interface Reading {
  /** The digits, including any minus sign. Empty when the meter is off. */
  display: string;
  unit: string;
  overRange: boolean;
  beep: boolean;
  /** A fuse is blown (by this measurement or before it). */
  blowsFuse: boolean;
  /** For series circuits: whether current flows, so the LED is lit. */
  ledLit: boolean;
  tone: Tone;
  title: string;
  message: string;
}

/** How hard a 9 V battery pushes into a near short: a few amps. */
export const SHORT_CIRCUIT_AMPS = 3;
/** Voltage a red LED drops while lit. */
export const LED_VOLTS = 2;

/** Formats `value` (base units) for a dial position, picking the range. */
export function formatValue(m: MeterDef, p: DialPos, value: number): { display: string; unit: string; overRange: boolean } {
  for (const r of p.ranges) {
    const v = value / r.scale;
    if (Number.isFinite(v) && Math.abs(v) <= r.max) {
      let s = v.toFixed(r.decimals);
      if (/^-0\.?0*$/.test(s)) s = s.slice(1);
      return { display: s, unit: r.unit, overRange: false };
    }
  }
  const last = p.ranges[p.ranges.length - 1];
  return { display: m.overRange, unit: last?.unit ?? '', overRange: true };
}

function zero(m: MeterDef, p: DialPos) {
  if (p.fn === 'cont' || p.fn === 'ohm') return formatValue(m, p, Infinity);
  return formatValue(m, p, 0);
}

function reading(
  shown: { display: string; unit: string; overRange: boolean },
  tone: Tone,
  title: string,
  message: string,
  extra: Partial<Reading> = {},
): Reading {
  return { ...shown, beep: false, blowsFuse: false, ledLit: false, tone, title, message, ...extra };
}

const FUSE_TEXT =
  'Current jacks have almost no resistance inside: they are a thick wire to COM. Put the probes across a battery and you have made a short circuit through the meter. The fuse blew to protect it. To measure current, break the circuit open and let the current flow through the meter (in series), never across a battery.';

/** What the meter shows for `target` with this setup. */
export function measure(s: Setup, target: Target | null): Reading {
  const m = s.meter;
  const p = dialById(m, s.dial);
  const blown = s.blown ?? [];
  if (p.fn === 'off') return reading({ display: '', unit: '', overRange: false }, 'info', 'Off', 'The meter is off. Turn the dial to a setting.');
  const idle = zero(m, p);
  const black = jackById(m, s.black);
  const red = jackById(m, s.red);
  if (!black || !red) return reading(idle, 'info', 'Plug in both probes', 'Plug in the black and the red probe first.');
  if (black.role !== 'com')
    return reading(idle, 'warn', 'Black is not in COM', 'Black belongs in COM. Every reading is measured from there.');
  if (!target) return reading(idle, 'info', 'Ready', 'Touch the probes to something to measure it.');

  // Red in a current jack is a near short to COM, whatever the dial says. The
  // 3-jack meter's shared jack only becomes one on its mA settings.
  const ampPath = red.role === 'a' || red.role === 'ma' || (red.role === 'vma' && p.fn === 'dca' && p.jack === red.id);
  const isBlown = blown.includes(red.id);
  const vj = voltsJack(m);
  const sign = s.reversed ? -1 : 1;

  if (target.kind === 'source') {
    if (ampPath) {
      if (isBlown || (red.fuseAmps !== null && red.fuseAmps < SHORT_CIRCUIT_AMPS))
        return reading(p.fn === 'dca' ? formatValue(m, p, 0) : idle, 'bad', 'Fuse blown', FUSE_TEXT, { blowsFuse: true });
      const shown = p.fn === 'dca' && p.jack === red.id ? formatValue(m, p, SHORT_CIRCUIT_AMPS * sign) : idle;
      return reading(
        shown,
        'bad',
        'Short circuit!',
        `Red is in ${red.label}, which is almost a plain wire to COM, so the meter is shorting the ${target.name}. ${red.fuseAmps === null ? 'This jack has no fuse' : `Its ${red.fuseAmps} A fuse is too big to blow here`}: several amps flow, the leads and battery get hot, and the meter or battery can be damaged. Let go, and move red back to ${vj.label}.`,
      );
    }
    if (p.fn === 'dcv') {
      const shown = formatValue(m, p, target.volts * sign);
      if (shown.overRange)
        return reading(
          shown,
          'warn',
          'Over range',
          m.autoRanging
            ? `OL means over limit: the ${target.name} is more than this setting can show. Use V⎓ for volts.`
            : `The lone 1 at the left means over range: about ${target.volts.toFixed(1)} V is more than this range can show. Turn the dial to the next range up.`,
        );
      if (s.reversed)
        return reading(
          shown,
          'info',
          'Minus sign: probes reversed',
          'The minus sign means red is on the lower voltage. Nothing is harmed. The number is still right; swap the probes to lose the minus.',
        );
      const coarse = !m.autoRanging && Math.abs(target.volts) < p.ranges[0].max / 10;
      return reading(
        shown,
        'ok',
        `${shown.display} ${shown.unit}`,
        coarse
          ? `That works, but this range shows few digits. A lower range that is still above ${target.volts.toFixed(1)} V gives more detail.`
          : `The ${target.name} measures ${shown.display} ${shown.unit}.`,
      );
    }
    if (p.fn === 'acv')
      return reading(formatValue(m, p, 0), 'warn', 'Wrong kind of volts', 'This is AC volts (~), for wall power. Batteries, USB and boards are DC. Use V⎓.');
    if (p.fn === 'ohm' || p.fn === 'cont')
      return reading(idle, 'warn', 'Not on a powered circuit', 'Resistance and continuity only work on something unpowered. The meter supplies its own small voltage for them. For a battery or pin, use V⎓.');
    if (p.fn === 'dca')
      return reading(formatValue(m, p, 0), 'warn', 'Dial on current', `The dial is on current, but red is in ${red.label}. For volts, turn the dial to V⎓.`);
    return reading(idle, 'info', 'Not this setting', `${p.name} is for something else. Turn the dial to V⎓.`);
  }

  if (target.kind === 'resistance') {
    if (ampPath)
      return reading(p.fn === 'dca' ? formatValue(m, p, 0) : idle, 'warn', 'Red is in a current jack', `Move red to ${vj.label} for continuity and resistance.`);
    if (p.fn === 'cont') {
      const shown = formatValue(m, p, target.ohms);
      if (target.ohms < m.beepBelow)
        return reading(shown, 'ok', 'Beep!', `It beeps: there is a complete path through the ${target.name}, under about ${m.beepBelow} Ω.`, { beep: true });
      if (shown.overRange && !Number.isFinite(target.ohms))
        return reading(shown, 'info', 'No beep', `${m.overRange === '1' ? 'A lone 1' : 'OL'} and no beep: there is no path through the ${target.name}. It is open.`);
      if (shown.overRange)
        return reading(
          shown,
          'info',
          'No beep',
          `${m.overRange === '1' ? 'A lone 1' : 'OL'} and no beep: the ${target.name} has more resistance than this setting can show. It is a path, but not a connection. To read its value, use a resistance (Ω) setting.`,
        );
      return reading(shown, 'info', 'No beep', `${shown.display} ${shown.unit} is a path, but too much resistance to count as a connection, so no beep.`);
    }
    if (p.fn === 'ohm') {
      const shown = formatValue(m, p, target.ohms);
      return reading(
        shown,
        'info',
        shown.overRange ? 'Over range' : `${shown.display} ${shown.unit}`,
        shown.overRange
          ? 'Over range: an open circuit, or more resistance than this range shows. For a quick yes or no, the beep setting is easier.'
          : 'That is the resistance. For a quick yes or no, the beep setting is easier.',
      );
    }
    if (p.fn === 'dcv' || p.fn === 'acv')
      return reading(formatValue(m, p, 0), 'info', 'No voltage here', `Nothing is powered, so there is no voltage to measure. Turn the dial to the beep symbol.`);
    return reading(idle, 'info', 'Not this setting', `${p.name} is for something else. Turn the dial to the beep symbol.`);
  }

  // A series circuit: the meter bridges a gap in it.
  if (ampPath) {
    if (isBlown)
      return reading(formatValue(m, p, 0), 'bad', 'Fuse blown', `The ${red.label} fuse is blown, so the meter is an open gap: no current flows, the LED stays dark and the display reads zero. Replace the fuse.`, { blowsFuse: true });
    if (p.fn !== 'dca' || p.jack !== red.id) {
      const want = m.dial.positions.filter((d) => d.jack === red.id).map((d) => d.label);
      return reading(
        p.fn === 'dca' ? formatValue(m, p, 0) : idle,
        'warn',
        'Dial and jack don’t match',
        `Current flows through the meter and the LED lights, but red is in ${red.label} and the dial isn’t set for it. Turn the dial to ${want.join(' or ')}.`,
        { ledLit: true },
      );
    }
    const shown = formatValue(m, p, target.amps * sign);
    if (shown.overRange)
      return reading(shown, 'warn', 'Over range', `About ${Math.round(target.amps * 1000)} mA is more than this range can show. Turn to the next range up.`, { ledLit: true });
    if (red.role === 'a')
      return reading(
        shown,
        'info',
        `${shown.display} ${shown.unit}`,
        `That works, but the big-current setting can’t show much detail. For small currents like an LED’s, use the ${m.jacks.find((j) => j.role === 'ma' || j.role === 'vma')!.label} jack and a mA setting.`,
        { ledLit: true },
      );
    return reading(
      shown,
      'ok',
      `${shown.display} ${shown.unit}`,
      `All the current now flows through the meter, so it reads what the LED draws: about ${Math.round(target.amps * 1000)} mA. When you are done, move red back to ${vj.label}.`,
      { ledLit: true },
    );
  }
  if (p.fn === 'dcv')
    return reading(
      formatValue(m, p, (target.supply - 1.4) * sign),
      'warn',
      'The LED went dark',
      'On volts the meter is almost no path at all, so in series it blocks the current. It reads most of the battery voltage and the LED goes out. To measure current, red goes in a current jack and the dial on a current setting.',
    );
  if (p.fn === 'dca')
    return reading(formatValue(m, p, 0), 'warn', 'Dial and jack don’t match', `The dial is on current, but red is still in ${vj.label}. Move red to ${jackById(m, p.jack!)!.label}.`);
  if (p.fn === 'ohm' || p.fn === 'cont')
    return reading(idle, 'warn', 'Not on a powered circuit', 'Resistance and continuity only work on something unpowered. For current, turn to a current setting.');
  return reading(idle, 'info', 'Not this setting', `${p.name} is for something else. Turn the dial to a current setting.`);
}

// ---------------------------------------------------------------------------
// What the demos measure.

export const CONTINUITY_TARGETS: Target[] = [
  { kind: 'resistance', id: 'wire', name: 'good wire', ohms: 0.3 },
  { kind: 'resistance', id: 'broken', name: 'broken wire', ohms: Infinity },
  { kind: 'resistance', id: 'pressed', name: 'pressed switch', ohms: 0.2 },
  { kind: 'resistance', id: 'released', name: 'released switch', ohms: Infinity },
  { kind: 'resistance', id: 'resistor', name: '220 Ω resistor', ohms: 220 },
];

export const VOLTAGE_TARGETS: Target[] = [
  { kind: 'source', id: 'aa', name: 'AA battery', volts: 1.58 },
  { kind: 'source', id: '9v', name: '9 V battery', volts: 9.42 },
  { kind: 'source', id: 'pin5v', name: 'board’s 5V pin', volts: 4.98 },
  { kind: 'source', id: 'pin3v3', name: 'board’s 3.3 pin', volts: 3.31 },
];

/** 9 V battery, 390 Ω resistor and a red LED: (9 - 2) / 390 ≈ 18 mA. */
export const LED_CIRCUIT: Target = {
  kind: 'series',
  id: 'led',
  name: 'LED circuit',
  supply: 9,
  amps: (9 - LED_VOLTS) / 390,
};

export const BATTERY_9V: Target = VOLTAGE_TARGETS[1];

// ---- Dial hints (user feedback, 2026-11-14) ----
// Each measuring demo starts with the dial on OFF. Until the reader turns it
// to the right setting, that setting pulses and a card says what its symbol
// looks like and where it sits on the meter in view.

export type DialHintGuide = 'continuity' | 'voltage' | 'current';

export interface DialHint {
  /** The dial position id to point at. */
  target: string;
  /** Which symbol to draw on the card. */
  symbol: 'cont' | 'dcv' | 'dca';
  /** Short name used in the heading and button: "Turn the dial to …". */
  setting: string;
  /** What the symbol looks like. */
  what: string;
  /** Where it is on this meter, and anything else to set. */
  where: string;
}

export const DIAL_HINTS: Record<DialHintGuide, Record<MeterId, DialHint>> = {
  continuity: {
    a: {
      target: 'cont',
      symbol: 'cont',
      setting: 'continuity',
      what: 'A diode (a triangle pointing at a bar) with sound waves beside it. Some meters print a speaker or •))) instead.',
      where: 'On this meter it is on the right of the dial, between hFE and 10A.',
    },
    b: {
      target: 'cont',
      symbol: 'cont',
      setting: 'continuity',
      what: 'A diode (a triangle pointing at a bar) with sound waves beside it. Some meters print a speaker or •))) instead.',
      where: 'On this meter it is at the bottom of the dial, between Ω and Hz%.',
    },
  },
  voltage: {
    a: {
      target: 'dcv20',
      symbol: 'dcv',
      setting: 'DC volts, 20',
      what: 'V with a solid line over a dashed line means DC volts. V with a wavy line (V~) is AC, for wall power. Never use it here.',
      where:
        'On this meter DC volts is the group on the left. Pick 20: it reads up to 19.99 V, enough for a 9 V battery or the board’s 5V pin. For a AA, 2 gives one more digit.',
    },
    b: {
      target: 'dcv',
      symbol: 'dcv',
      setting: 'DC volts',
      what: 'V with a solid line over a dashed line means DC volts. V with a wavy line (V~) is AC, for wall power. Never use it here.',
      where: 'On this meter it is at the upper right, between V~ and mV. It picks the range itself, so there is nothing else to set.',
    },
  },
  current: {
    a: {
      target: 'dca20m',
      symbol: 'dca',
      setting: 'DC current, 20m',
      what: 'A with a solid line over a dashed line means DC current. The m means milliamps: 20m reads up to 19.99 mA.',
      where:
        'On this meter DC current is the group at the lower right. An LED draws under 20 mA, so pick 20m. Red stays in VΩmA for milliamps.',
    },
    b: {
      target: 'ma',
      symbol: 'dca',
      setting: 'mA',
      what: 'mA means milliamps of DC current. An LED draws under 20 mA, so mA is the right setting.',
      where: 'On this meter it is on the left of the dial, between µA and A. Then move red to the mA µA jack.',
    },
  },
};

/** The note shown while the dial is still off. */
export function dialOffNote(h: DialHint): { tone: Tone; title: string; body: string } {
  return {
    tone: 'info',
    title: `Turn the dial to ${h.setting}`,
    body: 'Click the flashing setting on the dial, or use the button.',
  };
}
