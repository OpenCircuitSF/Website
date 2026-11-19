// Raspberry Pi batch 2: Zero audio, RGB panel basics and power (#0726). Logic
// for three guides: pi-zero-audio-out (ZeroAudioDemo.svelte),
// pi-rgb-matrix-basics (RgbLayoutDemo.svelte) and pi-rgb-matrix-power
// (RgbPowerDemo.svelte). Plain functions, no DOM, no timers; the demos only draw
// what these return; the refresh and current figures are illustrative.

// ------------------------------------------------------------ panel sizes

export type PanelSizeId = '32x16' | '32x32' | '64x32' | '64x64';
export interface PanelSize {
  id: PanelSizeId;
  label: string;
  cols: number;
  rows: number;
  /**
   * Datasheet-typical current in amps at 5 V, full white, 100% brightness. A
   * HUB75 panel lights two rows at a time, so its peak follows the number of
   * columns, not rows: 64-wide panels about 4 A, 32-wide about 2 A.
   */
  fullWhiteA: number;
}

export const PANEL_SIZES: readonly PanelSize[] = [
  { id: '32x16', label: '32×16', cols: 32, rows: 16, fullWhiteA: 2 },
  { id: '32x32', label: '32×32', cols: 32, rows: 32, fullWhiteA: 2 },
  { id: '64x32', label: '64×32', cols: 64, rows: 32, fullWhiteA: 4 },
  { id: '64x64', label: '64×64', cols: 64, rows: 64, fullWhiteA: 4 },
];

export function panelSize(id: PanelSizeId): PanelSize {
  const s = PANEL_SIZES.find((p) => p.id === id);
  if (!s) throw new Error(`piProjects2: unknown panel size ${id}`);
  return s;
}

/** Rows lit in turn: rows / 2 (1:8, 1:16, 1:16, 1:32 scan). */
export function scanRows(s: PanelSize): number {
  return s.rows / 2;
}

export type Tone = 'good' | 'meh' | 'bad';

// ------------------------------------------------------- Zero 2W audio out

export type ZeroAudioId = 'i2s' | 'usb' | 'hdmi' | 'bt';
export type AudioAxis = 'quality' | 'cost' | 'latency' | 'parts';
export type Rating = 'good' | 'ok' | 'poor';

export interface ZeroAudioOption {
  id: ZeroAudioId;
  label: string;
  short: string;
  title: string;
  /** Drawn chain, left to right. */
  nodes: string[];
  setupFile: string | null;
  setup: string[];
  setupNote: string;
  ratings: Record<AudioAxis, { rating: Rating; text: string }>;
  link: { href: string; text: string } | null;
}

export const ZERO_AUDIO: readonly ZeroAudioOption[] = [
  {
    id: 'i2s',
    label: 'DAC HAT (I2S)',
    short: 'a DAC HAT',
    title: 'DAC HAT: the cleanest wired sound',
    nodes: ['Pi Zero 2W', 'DAC HAT on the GPIO header', 'Amplifier or powered speakers'],
    setupFile: '/boot/firmware/config.txt',
    setup: ['dtoverlay=hifiberry-dac'],
    setupNote:
      'Example only: use the dtoverlay line your board’s maker gives. Add it at the end of config.txt, reboot, then check the card appears with aplay -l.',
    ratings: {
      quality: { rating: 'good', text: 'A dedicated DAC chip: clean sound' },
      cost: { rating: 'ok', text: 'A HAT costs more than a USB dongle' },
      latency: { rating: 'good', text: 'No noticeable delay' },
      parts: { rating: 'ok', text: 'A board on the header; the Zero may need its header soldered' },
    },
    link: { href: '/guides/pi-gpio-pinout', text: 'The GPIO header, pin by pin' },
  },
  {
    id: 'usb',
    label: 'USB DAC',
    short: 'a USB DAC',
    title: 'USB DAC: plug in, no setup',
    nodes: ['Pi Zero 2W', 'OTG adapter (USB port)', 'USB DAC', 'Amplifier or powered speakers'],
    setupFile: null,
    setup: ['aplay -l'],
    setupNote: 'No driver to install: a class-compliant USB DAC shows up as a USB Audio card once it is plugged into the port marked USB.',
    ratings: {
      quality: { rating: 'ok', text: 'Good from a decent one; the cheapest can hiss' },
      cost: { rating: 'good', text: 'A basic USB DAC is cheap' },
      latency: { rating: 'good', text: 'No noticeable delay' },
      parts: { rating: 'ok', text: 'An OTG adapter and the DAC hang off the port' },
    },
    link: { href: '/guides/pi-zero-usb-ports', text: 'Which micro-USB port is which' },
  },
  {
    id: 'hdmi',
    label: 'HDMI',
    short: 'HDMI',
    title: 'HDMI: let the TV or receiver do it',
    nodes: ['Pi Zero 2W', 'Mini-HDMI cable', 'TV, receiver or audio extractor', 'Speakers'],
    setupFile: null,
    setup: ['aplay -l'],
    setupNote:
      'HDMI audio is built in and shows up as an HDMI card. Pick it as the output in your player. No TV? An HDMI audio extractor splits the sound out to an amplifier.',
    ratings: {
      quality: { rating: 'good', text: 'As good as the TV or receiver' },
      cost: { rating: 'good', text: 'Nothing to buy if you have a TV or receiver' },
      latency: { rating: 'good', text: 'Little delay; some TVs add a bit' },
      parts: { rating: 'poor', text: 'Needs a TV, a receiver or an HDMI audio extractor' },
    },
    link: null,
  },
  {
    id: 'bt',
    label: 'Bluetooth',
    short: 'Bluetooth',
    title: 'Bluetooth: no wires, some delay',
    nodes: ['Pi Zero 2W', 'Bluetooth (no wire)', 'Bluetooth speaker'],
    setupFile: null,
    setup: ['bluetoothctl', 'scan on', 'pair AA:BB:CC:DD:EE:FF', 'trust AA:BB:CC:DD:EE:FF', 'connect AA:BB:CC:DD:EE:FF'],
    setupNote:
      'Use your speaker’s address from the scan. A headless Lite install also needs an audio server for Bluetooth sound, and players differ, so check yours can send to a Bluetooth speaker.',
    ratings: {
      quality: { rating: 'ok', text: 'Compressed; fine for casual listening' },
      cost: { rating: 'good', text: 'Nothing to buy if the speaker has Bluetooth' },
      latency: { rating: 'poor', text: 'A noticeable delay: fine for music, bad for video or games' },
      parts: { rating: 'good', text: 'No wires to the speaker' },
    },
    link: null,
  },
];

export const AUDIO_AXES: readonly { id: AudioAxis; label: string; row: string; phrase: string }[] = [
  { id: 'quality', label: 'Sound quality', row: 'Quality', phrase: 'sound quality' },
  { id: 'cost', label: 'Low cost', row: 'Cost', phrase: 'cost' },
  { id: 'latency', label: 'Low delay', row: 'Delay', phrase: 'low delay' },
  { id: 'parts', label: 'Fewest extra parts', row: 'Extra parts', phrase: 'few extra parts' },
];

export function zeroAudio(id: ZeroAudioId): ZeroAudioOption {
  const o = ZERO_AUDIO.find((x) => x.id === id);
  if (!o) throw new Error(`piProjects2: unknown audio option ${id}`);
  return o;
}

/** Ids rated 'good' on the axis, in ZERO_AUDIO order. */
export function bestFor(axis: AudioAxis): ZeroAudioId[] {
  return ZERO_AUDIO.filter((o) => o.ratings[axis].rating === 'good').map((o) => o.id);
}

/** ['a','b','c'] → 'a, b or c'; ['a','b'] → 'a or b'; ['a'] → 'a'. */
export function orList(items: string[]): string {
  if (items.length <= 1) return items[0] ?? '';
  return items.slice(0, -1).join(', ') + ' or ' + items[items.length - 1];
}

export function audioVerdict(id: ZeroAudioId, axis: AudioAxis): { tone: Tone; line: string } {
  const ax = AUDIO_AXES.find((a) => a.id === axis)!;
  const best = orList(
    bestFor(axis)
      .filter((x) => x !== id)
      .map((x) => zeroAudio(x).short),
  );
  const r = zeroAudio(id).ratings[axis].rating;
  if (r === 'good') return { tone: 'good', line: 'A good pick when ' + ax.phrase + ' matters most.' };
  if (r === 'ok') return { tone: 'meh', line: 'Workable when ' + ax.phrase + ' matters most; ' + best + ' would be better.' };
  return { tone: 'bad', line: 'A poor pick when ' + ax.phrase + ' matters most; try ' + best + '.' };
}

// ------------------------------------------- RGB panel layout and refresh

/** Same as piProjects.refreshHz: the library's lowest-bit time. */
export const LSB_NS = 130;
/** Same as piProjects.refreshHz (invented, illustrative). */
export const CLOCK_NS = 40;
/** A Pi 4 needs 2 or more (see the flicker guide). */
export const BASICS_SLOWDOWN = 2;
/** Library default. */
export const BASICS_PWM_BITS = 11;

/**
 * #0721's illustrative refresh model, generalised to any panel size.
 * Parallel chains are clocked out together, so `parallel` is not a
 * parameter: that is the point of them.
 */
export function layoutRefreshHz(s: PanelSize, chain: number, pwmBits = BASICS_PWM_BITS, slowdown = BASICS_SLOWDOWN): number {
  const ns = (2 ** pwmBits - 1) * LSB_NS + pwmBits * s.cols * chain * CLOCK_NS * (slowdown + 1);
  return Math.round(1e9 / (scanRows(s) * ns));
}

export type AdapterId = 'single' | 'triple';
export const ADAPTERS: readonly { id: AdapterId; label: string; maxParallel: number }[] = [
  { id: 'single', label: 'Single-chain HAT or bonnet', maxParallel: 1 },
  { id: 'triple', label: 'Three-output adapter board', maxParallel: 3 },
];
export const MAX_CHAIN = 8;

export type Flicker = 'visible' | 'camera' | 'steady';

/** #0721's thresholds: under 100 Hz visible, under 200 Hz a camera may catch it. */
export function flickerClass(hz: number): Flicker {
  if (hz < 100) return 'visible';
  if (hz < 200) return 'camera';
  return 'steady';
}

/** x, y in panel pixels on the combined canvas. */
export interface PanelPos {
  chainIndex: number;
  parallelIndex: number;
  x: number;
  y: number;
}

export interface MatrixLayout {
  width: number;
  height: number;
  pixels: number;
  panels: PanelPos[];
  refreshHz: number;
  flicker: Flicker;
  title: string;
  tone: Tone;
  problem: string | null;
  command: string;
}

export function matrixLayout(size: PanelSizeId, chain: number, parallel: number, adapter: AdapterId): MatrixLayout {
  const s = panelSize(size);
  const width = s.cols * chain;
  const height = s.rows * parallel;
  const panels: PanelPos[] = [];
  for (let p = 0; p < parallel; p++) {
    for (let c = 0; c < chain; c++) panels.push({ chainIndex: c, parallelIndex: p, x: c * s.cols, y: p * s.rows });
  }
  const refreshHz = layoutRefreshHz(s, chain);
  const flicker = flickerClass(refreshHz);
  const ad = ADAPTERS.find((a) => a.id === adapter)!;
  let title: string;
  let tone: Tone;
  let problem: string | null = null;
  if (parallel > ad.maxParallel) {
    problem = 'This adapter drives one chain. Parallel chains need an adapter board with more outputs.';
    title = 'One chain only on this adapter';
    tone = 'bad';
  } else if (flicker === 'steady') {
    title = `Steady: ${refreshHz} Hz`;
    tone = 'good';
  } else if (flicker === 'camera') {
    title = `Fine to the eye: ${refreshHz} Hz`;
    tone = 'meh';
  } else {
    title = `Flicker likely: ${refreshHz} Hz`;
    tone = 'bad';
  }
  const command = `sudo ./demo -D 0 --led-rows=${s.rows} --led-cols=${s.cols} --led-chain=${chain} --led-parallel=${parallel} --led-slowdown-gpio=2 --led-show-refresh`;
  return { width, height, pixels: width * height, panels, refreshHz, flicker, title, tone, problem, command };
}

// --------------------------------------------------------- RGB panel power

export type ContentId = 'white' | 'image' | 'text';
/** Fraction of full-white current; illustrative. */
export const PANEL_CONTENT: readonly { id: ContentId; label: string; fraction: number }[] = [
  { id: 'white', label: 'Full white', fraction: 1 },
  { id: 'image', label: 'A typical picture or video', fraction: 0.35 },
  { id: 'text', label: 'Text on black', fraction: 0.08 },
];
export const SUPPLY_SIZES_A = [2, 4, 5, 8, 10, 20, 30, 40, 60];
export const SUPPLY_CHOICES_A = [4, 10, 20, 40];
export const HEADROOM = 1.2;
export const MAX_PANELS = 6;

/** Smallest standard supply at least worstA × HEADROOM, else null. */
export function recommendedSupplyA(worstA: number): number | null {
  const need = worstA * HEADROOM;
  return SUPPLY_SIZES_A.find((a) => a >= need - 1e-9) ?? null;
}

/** Conservative gauges for short 5 V runs; illustrative. */
export function wireGauge(a: number): string | null {
  if (a <= 3) return '20 AWG (0.5 mm²)';
  if (a <= 5) return '18 AWG (0.8 mm²)';
  if (a <= 8) return '16 AWG (1.3 mm²)';
  if (a <= 13) return '14 AWG (2.1 mm²)';
  if (a <= 20) return '12 AWG (3.3 mm²)';
  return null;
}

export function amps(a: number): string {
  return a.toFixed(1) + ' A';
}

export interface PanelPowerInput {
  size: PanelSizeId;
  panels: number;
  brightness: number;
  content: ContentId;
  supplyA: number;
}
export type PowerStatus = 'over' | 'whiteOver' | 'noRoom' | 'ok';
export const POWER_TITLES: Record<PowerStatus, string> = {
  over: 'Over the supply’s rating',
  whiteOver: 'OK for this content; full white would overload the supply',
  noRoom: 'Enough, with little headroom',
  ok: 'Plenty of supply',
};
export interface PanelPower {
  perPanelA: number;
  totalA: number;
  worstPerPanelA: number;
  worstTotalA: number;
  recommendedA: number | null;
  panelWire: string | null;
  mainWire: string | null;
  status: PowerStatus;
  tone: Tone;
}

/** Wires and supply are sized for the worst case, because any frame can be white. */
export function panelPower(i: PanelPowerInput): PanelPower {
  const s = panelSize(i.size);
  const fraction = PANEL_CONTENT.find((c) => c.id === i.content)!.fraction;
  const worstPerPanelA = (s.fullWhiteA * i.brightness) / 100;
  const perPanelA = worstPerPanelA * fraction;
  const totalA = perPanelA * i.panels;
  const worstTotalA = worstPerPanelA * i.panels;
  let status: PowerStatus;
  let tone: Tone;
  if (totalA > i.supplyA) [status, tone] = ['over', 'bad'];
  else if (worstTotalA > i.supplyA) [status, tone] = ['whiteOver', 'meh'];
  else if (worstTotalA > 0.8 * i.supplyA) [status, tone] = ['noRoom', 'meh'];
  else [status, tone] = ['ok', 'good'];
  return {
    perPanelA,
    totalA,
    worstPerPanelA,
    worstTotalA,
    recommendedA: recommendedSupplyA(worstTotalA),
    panelWire: wireGauge(worstPerPanelA),
    mainWire: wireGauge(worstTotalA),
    status,
    tone,
  };
}
