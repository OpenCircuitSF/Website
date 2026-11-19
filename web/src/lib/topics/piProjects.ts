// Raspberry Pi: Volumio and RGB panels (#0721). Logic for two guides, with no
// DOM and no timers: pi-volumio-streaming (VolumioDemo.svelte) and
// pi-rgb-matrix-flicker (RgbMatrixDemo.svelte).

// ---------------------------------------------------------------- Volumio

/** The day the free/Premium split below was checked on volumio.com. */
export const VOLUMIO_PLANS_CHECKED = '2026-11-18';

export type PlaySource = 'spotify' | 'airplay' | 'upnp' | 'radio' | 'files' | 'tidal' | 'qobuz' | 'bluetooth';
export type Tier = 'free' | 'premium';

export interface SourceInfo {
  id: PlaySource;
  /** Button text. */
  label: string;
  /** The Volumio feature that handles it. */
  feature: string;
  tier: Tier;
  /** Label on the source → Pi arrow. */
  via: string;
  note: string;
}

export const PLAY_SOURCES: readonly SourceInfo[] = [
  {
    id: 'spotify',
    label: 'Spotify app',
    feature: 'Spotify Connect (plugin)',
    tier: 'free',
    via: 'Spotify Connect',
    note: 'Install the Spotify plugin; each listener picks the speaker in their own app, which needs their own Spotify Premium account.',
  },
  {
    id: 'airplay',
    label: 'iPhone or Mac',
    feature: 'AirPlay',
    tier: 'free',
    via: 'AirPlay over WiFi',
    note: 'Built in: it appears in the AirPlay menu under the player’s name.',
  },
  {
    id: 'upnp',
    label: 'Laptop or Android app',
    feature: 'UPnP/DLNA renderer',
    tier: 'free',
    via: 'UPnP/DLNA',
    note: 'Any UPnP/DLNA control app can send to it.',
  },
  {
    id: 'radio',
    label: 'Internet radio',
    feature: 'Web radio (built in)',
    tier: 'free',
    via: 'internet stream',
    note: 'Pick a station on the Volumio web page; the Pi fetches the stream itself.',
  },
  {
    id: 'files',
    label: 'Files on USB or a share',
    feature: 'Music library (USB, NAS)',
    tier: 'free',
    via: 'USB or network share',
    note: 'Plug a USB stick into the Pi or add a network share (SMB/NFS).',
  },
  { id: 'tidal', label: 'Tidal app', feature: 'Tidal Connect', tier: 'premium', via: 'Tidal Connect', note: 'Needs Volumio Premium.' },
  { id: 'qobuz', label: 'Qobuz app', feature: 'Qobuz Connect', tier: 'premium', via: 'Qobuz Connect', note: 'Needs Volumio Premium.' },
  {
    id: 'bluetooth',
    label: 'Phone over Bluetooth',
    feature: 'Bluetooth input',
    tier: 'premium',
    via: 'Bluetooth',
    note: 'Needs Volumio Premium.',
  },
];

export type PiBoard = 'zero2w' | 'pi4';
export type AudioOut = 'dac-hat' | 'usb-dac' | 'hdmi' | 'jack';

export const AUDIO_OUTS: readonly { id: AudioOut; label: string; node: string }[] = [
  { id: 'dac-hat', label: 'DAC HAT', node: 'DAC HAT' },
  { id: 'usb-dac', label: 'USB DAC', node: 'USB DAC' },
  { id: 'hdmi', label: 'HDMI to a receiver', node: 'HDMI audio' },
  { id: 'jack', label: 'Headphone jack', node: '3.5 mm jack' },
];

export function hasHeadphoneJack(board: PiBoard): boolean {
  return board === 'pi4';
}

export interface ChainNode {
  id: 'source' | 'pi' | 'out' | 'amp' | 'speakers';
  label: string;
  ok: boolean;
}

export interface VolumioChain {
  nodes: ChainNode[];
  ok: boolean;
  problem: string | null;
  tier: Tier;
  feature: string;
}

export function sourceById(id: PlaySource): SourceInfo {
  return PLAY_SOURCES.find((s) => s.id === id) ?? PLAY_SOURCES[0];
}

export function volumioChain(source: PlaySource, board: PiBoard, out: AudioOut): VolumioChain {
  const src = sourceById(source);
  const o = AUDIO_OUTS.find((a) => a.id === out) ?? AUDIO_OUTS[0];
  const broken = out === 'jack' && !hasHeadphoneJack(board);
  const nodes: ChainNode[] = [
    { id: 'source', label: source === 'radio' ? 'Internet' : src.label, ok: true },
    { id: 'pi', label: 'Pi running Volumio', ok: true },
    { id: 'out', label: o.node, ok: !broken },
    { id: 'amp', label: 'Amplifier', ok: !broken },
    { id: 'speakers', label: 'Speakers', ok: !broken },
  ];
  return {
    nodes,
    ok: !broken,
    problem: broken ? 'A Pi Zero 2W has no headphone jack. Use a DAC HAT, a USB DAC or HDMI.' : null,
    tier: src.tier,
    feature: src.feature,
  };
}

// -------------------------------------------------------------- RGB panel

export type GpioMapping = 'regular' | 'adafruit-hat' | 'adafruit-hat-pwm';

export interface MatrixConfig {
  slowdown: number;
  soundOff: boolean;
  isolatedCore: boolean;
  mapping: GpioMapping;
  pwmMod: boolean;
  pwmBits: number;
  chain: number;
}

/** A fresh install: library defaults and Raspberry Pi OS defaults. */
export const MATRIX_START: MatrixConfig = {
  slowdown: 1,
  soundOff: false,
  isolatedCore: false,
  mapping: 'regular',
  pwmMod: false,
  pwmBits: 11,
  chain: 1,
};

/** Drawn cells: a half-resolution picture of a 64×32 panel. */
export const PANEL_W = 32;
export const PANEL_H = 16;

export type Look = 'dark' | 'garbage' | 'shimmer' | 'clean';
export type Problem = 'sound' | 'mapping' | 'slowdown' | 'core' | 'refresh';

export interface MatrixState {
  runs: boolean;
  look: Look;
  jitter: boolean;
  refreshHz: number;
  refreshLowHz: number;
  flicker: 'visible' | 'camera' | 'steady' | 'none';
  problems: Problem[];
}

/**
 * An ILLUSTRATIVE refresh model, not a measurement, for 64×32 panels (1:16
 * scan) chained `chain` times. 130 ns is the library's lowest-bit time; the
 * 40 ns × (slowdown + 1) per clocked pixel is invented to give realistic
 * magnitudes. Real numbers come from --led-show-refresh.
 */
export function refreshHz(cfg: MatrixConfig): number {
  const bits = cfg.pwmBits;
  const ns = (2 ** bits - 1) * 130 + bits * 64 * cfg.chain * 40 * (cfg.slowdown + 1);
  return Math.round(1e9 / (16 * ns));
}

export function colorLevels(bits: number): number {
  return 2 ** bits;
}

function mappingWrong(cfg: MatrixConfig): boolean {
  return cfg.mapping === 'regular' || (cfg.mapping === 'adafruit-hat-pwm' && !cfg.pwmMod);
}

export function matrixState(cfg: MatrixConfig): MatrixState {
  if (!cfg.soundOff) {
    return { runs: false, look: 'dark', jitter: false, refreshHz: 0, refreshLowHz: 0, flicker: 'none', problems: ['sound'] };
  }
  const problems: Problem[] = [];
  const hz = refreshHz(cfg);
  let look: Look;
  if (mappingWrong(cfg)) {
    look = 'garbage';
    problems.push('mapping');
  } else if (cfg.slowdown <= 0) {
    look = 'garbage';
    problems.push('slowdown');
  } else if (cfg.slowdown === 1) {
    look = 'shimmer';
    problems.push('slowdown');
  } else {
    look = 'clean';
  }
  const jitter = !cfg.isolatedCore;
  const low = jitter ? Math.round(hz * 0.7) : hz;
  if (jitter) problems.push('core');
  let flicker: MatrixState['flicker'];
  if (low < 100) {
    flicker = 'visible';
    problems.push('refresh');
  } else if (low < 200) {
    flicker = 'camera';
  } else {
    flicker = 'steady';
  }
  return { runs: true, look, jitter, refreshHz: hz, refreshLowHz: low, flicker, problems };
}

const BARS = ['#ffffff', '#ffff00', '#00ffff', '#00ff00', '#ff00ff', '#ff0000', '#0000ff', '#000000'];

function hex2(v: number): string {
  return Math.max(0, Math.min(255, Math.round(v))).toString(16).padStart(2, '0');
}

function cleanPixel(i: number, pwmBits: number): string {
  const row = Math.floor(i / PANEL_W);
  const col = i % PANEL_W;
  if (row < 12) return BARS[Math.floor(col / 4)];
  const levels = Math.min(colorLevels(pwmBits), PANEL_W);
  const step = Math.floor((col * levels) / PANEL_W);
  const g = hex2(levels > 1 ? (step * 255) / (levels - 1) : 0);
  return `#${g}${g}${g}`;
}

/** PANEL_W × PANEL_H colors, row-major. Deterministic: never random or time-based. */
export function panelPixels(state: MatrixState, pwmBits: number): string[] {
  const n = PANEL_W * PANEL_H;
  const out: string[] = [];
  for (let i = 0; i < n; i++) {
    if (state.look === 'dark') out.push('#111111');
    else if (state.look === 'garbage') out.push(BARS[((i * 2654435761) >>> 0) % 8]);
    else if (state.look === 'shimmer' && (i * 7) % 11 === 0) out.push(BARS[(Math.floor((i % PANEL_W) / 4) + 3) % 8]);
    else out.push(cleanPixel(i, pwmBits));
  }
  return out;
}

/** A static dimming per row, standing in for jitter and flicker. Never animated. */
export function rowOpacity(row: number, state: MatrixState): number {
  let o = 1;
  if (state.jitter && (row === 4 || row === 5 || row === 12 || row === 13)) o = Math.min(o, 0.55);
  if (state.flicker === 'visible' && Math.floor(row / 2) % 2 === 1) o = Math.min(o, 0.45);
  return o;
}

/** The library's examples-api-use/demo with these settings. */
export function matrixCommand(cfg: MatrixConfig): string {
  return (
    `sudo ./demo -D 0 --led-rows=32 --led-cols=64 --led-chain=${cfg.chain}` +
    ` --led-gpio-mapping=${cfg.mapping} --led-slowdown-gpio=${cfg.slowdown}` +
    ` --led-pwm-bits=${cfg.pwmBits} --led-show-refresh`
  );
}
