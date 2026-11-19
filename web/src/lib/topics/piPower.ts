// Raspberry Pi power and SD cards (#0720): the voltage-sag model behind the
// under-voltage guide, the vcgencmd get_throttled decoder, and the SD card
// write timeline. Plain functions, the demos only draw what these return.

// ---------------------------------------------------------------------------
// Voltage sag. An illustrative model with typical round numbers, not
// measurements: the supply has an internal resistance and sags hard past its
// rating; the cable loses voltage in both wires and at both plugs.

/** Firmware under-voltage flag threshold (Raspberry Pi docs: 4.63 V ±5%). */
export const UNDERVOLTAGE_V = 4.63;
/** Below this the demo says "close to the line". */
export const MARGIN_V = 4.75;
/** Contact resistance: both plugs, both wires. */
export const CONTACT_OHMS = 0.03;
/** Extra sag per amp drawn past a supply's rating. */
export const OVERLOAD_OHMS = 2.0;

export type Awg = 18 | 20 | 22 | 24 | 26 | 28;
/** Copper resistance per conductor, milliohms per metre (standard AWG table). */
export const AWG_MOHM_PER_M: Record<Awg, number> = { 18: 21, 20: 33, 22: 53, 24: 84, 26: 134, 28: 213 };

export type BoardId = 'pi4' | 'zero2w';
export type LoadId = 'idle' | 'busy' | 'usbDrive';
export interface Board {
  id: BoardId;
  label: string;
  officialSupply: string;
  amps: Record<LoadId, number>;
}
export const BOARDS: Board[] = [
  { id: 'pi4', label: 'Pi 4', officialSupply: '5.1 V, 3 A, USB-C', amps: { idle: 0.6, busy: 1.2, usbDrive: 1.7 } },
  { id: 'zero2w', label: 'Pi Zero 2W', officialSupply: '5.1 V, 2.5 A, micro-USB', amps: { idle: 0.15, busy: 0.45, usbDrive: 0.95 } },
];
export const LOADS: { id: LoadId; label: string }[] = [
  { id: 'idle', label: 'Idle' },
  { id: 'busy', label: 'Busy, with WiFi' },
  { id: 'usbDrive', label: 'Busy, plus a USB hard drive' },
];

export type SupplyId = 'official' | 'charger' | 'phone' | 'laptop';
export interface Supply {
  id: SupplyId;
  label: string;
  volts: number;
  ohms: number;
  ratedA: number;
  captive: boolean;
}
export const SUPPLIES: Supply[] = [
  { id: 'official', label: 'Official Raspberry Pi supply', volts: 5.1, ohms: 0.02, ratedA: 2.5, captive: true },
  { id: 'charger', label: 'USB charger, 5 V 2.4 A', volts: 5.0, ohms: 0.05, ratedA: 2.4, captive: false },
  { id: 'phone', label: 'Old phone charger, 5 V 1 A', volts: 5.0, ohms: 0.15, ratedA: 1.0, captive: false },
  { id: 'laptop', label: 'Laptop USB-A port', volts: 5.0, ohms: 0.2, ratedA: 0.9, captive: false },
];

export type CableId = 'thick' | 'typical' | 'thin' | 'captive';
export interface Cable {
  id: CableId;
  label: string;
  lengthM: number;
  awg: Awg;
}
/** The three cables the reader can pick. */
export const CABLES: Cable[] = [
  { id: 'thick', label: 'Short thick cable (1 m, 20 AWG)', lengthM: 1, awg: 20 },
  { id: 'typical', label: 'Typical cable (1 m, 24 AWG)', lengthM: 1, awg: 24 },
  { id: 'thin', label: 'Thin long cable (2 m, 28 AWG)', lengthM: 2, awg: 28 },
];
export const CAPTIVE_CABLE: Cable = { id: 'captive', label: 'Its own built-in cable (1.5 m, 18 AWG)', lengthM: 1.5, awg: 18 };

/** Round-trip resistance: both conductors plus the contacts. */
export function cableOhms(c: Cable): number {
  return (2 * c.lengthM * AWG_MOHM_PER_M[c.awg]) / 1000 + CONTACT_OHMS;
}

/** A supply with its own cable ignores the one picked. */
export function effectiveCable(s: Supply, c: Cable): Cable {
  return s.captive ? CAPTIVE_CABLE : c;
}

/** The supply's output voltage at a given current. */
export function supplyVolts(s: Supply, amps: number): number {
  return s.volts - s.ohms * amps - Math.max(0, amps - s.ratedA) * OVERLOAD_OHMS;
}

export type PiStatus = 'ok' | 'marginal' | 'under';
export function piStatus(v: number): PiStatus {
  if (v < UNDERVOLTAGE_V) return 'under';
  if (v < MARGIN_V) return 'marginal';
  return 'ok';
}
export const SAG_TITLES: Record<PiStatus, string> = {
  ok: 'Plenty of margin',
  marginal: 'Close to the line',
  under: 'Under-voltage: the Pi slows down',
};

export interface SagInput {
  board: BoardId;
  supply: SupplyId;
  cable: CableId;
  load: LoadId;
}
export interface Sag {
  amps: number;
  cable: Cable;
  supplyV: number;
  cableDropV: number;
  piV: number;
  status: PiStatus;
  throttled: number;
}

export function sag(i: SagInput): Sag {
  const board = BOARDS.find((b) => b.id === i.board) ?? BOARDS[0];
  const supply = SUPPLIES.find((s) => s.id === i.supply) ?? SUPPLIES[0];
  const picked = CABLES.find((c) => c.id === i.cable) ?? CABLES[1];
  const cable = effectiveCable(supply, picked);
  const amps = board.amps[i.load];
  const supplyV = supplyVolts(supply, amps);
  const cableDropV = cableOhms(cable) * amps;
  const piV = supplyV - cableDropV;
  const status = piStatus(piV);
  const throttled = status === 'under' ? 0x50005 : status === 'marginal' ? 0x50000 : 0;
  return { amps, cable, supplyV, cableDropV, piV, status, throttled };
}

// ---------------------------------------------------------------------------
// vcgencmd get_throttled.

export type FlagKey = 'underVoltage' | 'freqCapped' | 'throttled' | 'softTemp';
export interface ThrottleFlag {
  bit: number;
  key: FlagKey;
  now: boolean;
  label: string;
  short: string;
}
export const THROTTLE_FLAGS: ThrottleFlag[] = [
  { bit: 0, key: 'underVoltage', now: true, label: 'Under-voltage right now', short: 'UV' },
  { bit: 1, key: 'freqCapped', now: true, label: 'CPU speed capped right now', short: 'CAP' },
  { bit: 2, key: 'throttled', now: true, label: 'Throttled right now', short: 'THR' },
  { bit: 3, key: 'softTemp', now: true, label: 'Soft temperature limit right now', short: 'TMP' },
  { bit: 16, key: 'underVoltage', now: false, label: 'Under-voltage has happened since boot', short: 'UV' },
  { bit: 17, key: 'freqCapped', now: false, label: 'CPU speed capping has happened since boot', short: 'CAP' },
  { bit: 18, key: 'throttled', now: false, label: 'Throttling has happened since boot', short: 'THR' },
  { bit: 19, key: 'softTemp', now: false, label: 'Soft temperature limit has happened since boot', short: 'TMP' },
];

export type ParsedThrottled = { ok: true; value: number } | { ok: false; error: string };

/** Reads what vcgencmd prints (always hex), with or without "throttled=" and "0x". */
export function parseThrottled(input: string): ParsedThrottled {
  let s = input.trim();
  if (/^throttled=/i.test(s)) s = s.slice('throttled='.length);
  if (/^0x/i.test(s)) s = s.slice(2);
  if (input.trim() === '') return { ok: false, error: 'Type a value, like 0x50005.' };
  if (!/^[0-9a-f]+$/i.test(s)) {
    return { ok: false, error: 'That isn’t a hex number. vcgencmd prints something like throttled=0x50005.' };
  }
  if (s.length > 8) return { ok: false, error: 'Too many digits: get_throttled is at most 8 hex digits.' };
  return { ok: true, value: parseInt(s, 16) };
}

function bitSet(value: number, bit: number): boolean {
  return ((value >>> bit) & 1) === 1;
}

export interface Decoded {
  value: number;
  flags: ThrottleFlag[];
  unknownBits: number[];
  title: string;
  body: string;
  tone: 'good' | 'meh' | 'bad';
}

export function decodeThrottled(value: number): Decoded {
  const flags = THROTTLE_FLAGS.filter((f) => bitSet(value, f.bit));
  const documented = new Set(THROTTLE_FLAGS.map((f) => f.bit));
  const unknownBits: number[] = [];
  for (let b = 0; b < 32; b++) if (!documented.has(b) && bitSet(value, b)) unknownBits.push(b);
  const base = { value, flags, unknownBits };
  if (bitSet(value, 0)) {
    return {
      ...base,
      title: 'Power problem now',
      tone: 'bad',
      body: 'The supply or cable can’t hold the voltage up right now. Use the official supply, or a short, thick cable, and unplug power-hungry USB devices.',
    };
  }
  if (bitSet(value, 16)) {
    return {
      ...base,
      title: 'Power problem since boot',
      tone: 'meh',
      body: 'It’s fine this moment, but the voltage dropped at some point since boot, probably under load. The fix is the same: a better supply or cable.',
    };
  }
  if ([1, 2, 3, 17, 18, 19].some((b) => bitSet(value, b))) {
    return {
      ...base,
      title: 'Running hot',
      tone: 'meh',
      body: 'No power problem. The Pi slowed itself down because it got hot: add a heatsink or a case with some airflow.',
    };
  }
  if (value === 0) {
    return { ...base, title: 'All clear', tone: 'good', body: 'No under-voltage and no throttling since the Pi booted.' };
  }
  return { ...base, title: 'No documented flags', tone: 'meh', body: 'None of the eight documented flags is set.' };
}

/** '0x' + lower-case hex, no padding. */
export function hex(value: number): string {
  return '0x' + (value >>> 0).toString(16);
}

// ---------------------------------------------------------------------------
// SD card write timeline: a simplification of ext4 in ordered mode (data,
// then the journal, its commit, then the in-place copy), plus the card's own
// flash-block rewrite, shown as one stage although it can happen at any write.

export type StageId = 'before' | 'data' | 'journal' | 'commit' | 'inPlace' | 'flash' | 'done';
export interface Stage {
  id: StageId;
  from: number;
  to: number;
  label: string;
}
/** Slider positions 0..100, inclusive and contiguous. */
export const STAGES: Stage[] = [
  { id: 'before', from: 0, to: 9, label: 'Nothing written yet' },
  { id: 'data', from: 10, to: 34, label: 'Writing the file’s data blocks' },
  { id: 'journal', from: 35, to: 54, label: 'Writing the directory entry to the journal' },
  { id: 'commit', from: 55, to: 59, label: 'Journal commit' },
  { id: 'inPlace', from: 60, to: 79, label: 'Copying the directory entry to its real place' },
  { id: 'flash', from: 80, to: 94, label: 'The card’s controller rewrites a whole flash block' },
  { id: 'done', from: 95, to: 100, label: 'Done' },
];

export function stageAt(t: number): Stage {
  const x = Math.round(Math.min(100, Math.max(0, Number.isFinite(t) ? t : 0)));
  return STAGES.find((s) => x >= s.from && x <= s.to) ?? STAGES[0];
}

export type Fill = 'blank' | 'partial' | 'written';
export interface CardState {
  data: Fill;
  journal: Fill;
  directory: Fill;
  others: 'ok' | 'damaged';
  inRam: boolean;
}
export interface CutResult {
  stage: Stage;
  card: CardState;
  file: 'old' | 'new';
  title: string;
  body: string;
  tone: 'good' | 'meh' | 'bad';
}

type Outcome = Omit<CutResult, 'stage'>;
const card = (data: Fill, journal: Fill, directory: Fill, others: 'ok' | 'damaged' = 'ok'): CardState => ({
  data,
  journal,
  directory,
  others,
  inRam: false,
});
const LOST = 'Change lost, card fine';
const REPLAYED = 'Saved: the journal finishes it at boot';
const OUTCOMES: Record<StageId, Outcome> = {
  before: {
    card: card('blank', 'blank', 'blank'),
    file: 'old',
    tone: 'good',
    title: 'Nothing written yet: card fine',
    body: 'Nothing had reached the card yet, so there is nothing to lose. After boot you have the old file.',
  },
  data: {
    card: card('partial', 'blank', 'blank'),
    file: 'old',
    tone: 'meh',
    title: LOST,
    body: 'The new data is half on the card, but nothing points to it yet. After boot you have the old file.',
  },
  journal: {
    card: card('written', 'partial', 'blank'),
    file: 'old',
    tone: 'meh',
    title: LOST,
    body: 'The journal entry has no commit record, so at boot it is thrown away. You have the old file.',
  },
  commit: {
    card: card('written', 'written', 'blank'),
    file: 'new',
    tone: 'good',
    title: REPLAYED,
    body: 'The journal entry is committed: at boot the journal replays it and finishes the save.',
  },
  inPlace: {
    card: card('written', 'written', 'partial'),
    file: 'new',
    tone: 'good',
    title: REPLAYED,
    body: 'The directory entry is half copied, but the committed journal replays at boot and finishes it.',
  },
  flash: {
    card: card('written', 'written', 'written', 'damaged'),
    file: 'new',
    tone: 'bad',
    title: 'Corrupted: other files damaged',
    body: 'The card moves data in big flash blocks that hold pieces of other files. Cut the power mid-rewrite and those files can come back damaged, even ones nobody was saving, maybe the ones the Pi needs to boot. The journal can’t fix what the filesystem never wrote.',
  },
  done: {
    card: card('written', 'written', 'written'),
    file: 'new',
    tone: 'good',
    title: 'Saved',
    body: 'All written. But Linux writes logs every few seconds, so there is always a next write: shut down first.',
  },
};

export function cutPower(t: number, overlay: boolean): CutResult {
  const stage = stageAt(t);
  if (overlay) {
    return {
      stage,
      card: { data: 'blank', journal: 'blank', directory: 'blank', others: 'ok', inRam: true },
      file: 'old',
      tone: 'good',
      title: 'Card untouched: the save was in memory',
      body: 'With the overlay file system on, the card is read-only. Saves go to memory and disappear at reboot, so the card is never in the middle of a write when the plug comes out.',
    };
  }
  return { stage, ...OUTCOMES[stage.id] };
}
