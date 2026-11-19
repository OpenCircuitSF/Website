// Raspberry Pi hardware (#0724): which Zero 2W port is which, why a Pi 4's
// micro-HDMI gives no picture, and why a Pi 4 slows down when it gets hot.
// Plain functions; the demos only draw what these return. The heat model is
// illustrative, with round numbers rather than measurements.

export { hex } from './piPower';

export type Tone = 'good' | 'meh' | 'bad';

// ---------------------------------------------------------------------------
// 1. Zero 2W ports.

export type ZeroPortId = 'hdmi' | 'usb' | 'pwr';
export interface ZeroPort {
  id: ZeroPortId;
  label: string;
  connector: string;
}
/** Left to right along the board's long edge. */
export const ZERO_PORTS: ZeroPort[] = [
  { id: 'hdmi', label: 'Mini-HDMI', connector: 'mini-HDMI' },
  { id: 'usb', label: 'USB', connector: 'micro-USB (data, OTG)' },
  { id: 'pwr', label: 'PWR IN', connector: 'micro-USB (power only)' },
];

export type PlugId = 'microUsb' | 'usbA' | 'miniHdmi' | 'microHdmi';
export type ZeroDeviceId = 'charger' | 'keyboard' | 'drive' | 'dac' | 'hub' | 'monitor' | 'microHdmi';
export interface ZeroDevice {
  id: ZeroDeviceId;
  label: string;
  plug: PlugId;
}
export const ZERO_DEVICES: ZeroDevice[] = [
  { id: 'charger', label: 'Micro-USB power supply', plug: 'microUsb' },
  { id: 'keyboard', label: 'USB keyboard', plug: 'usbA' },
  { id: 'drive', label: 'USB flash drive', plug: 'usbA' },
  { id: 'dac', label: 'USB DAC (sound card)', plug: 'usbA' },
  { id: 'hub', label: 'Powered USB hub', plug: 'usbA' },
  { id: 'monitor', label: 'Monitor, mini-HDMI cable', plug: 'miniHdmi' },
  { id: 'microHdmi', label: 'Monitor, a Pi 4’s micro-HDMI cable', plug: 'microHdmi' },
];

export type ZeroOutcomeId = 'powered' | 'poweredDataPort' | 'works' | 'noData' | 'noAdapter' | 'video' | 'wrongShape';
export interface ZeroPlug {
  outcome: ZeroOutcomeId;
  fits: boolean;
  title: string;
  body: string;
  tone: Tone;
}

const WORKS_TAIL: Partial<Record<ZeroDeviceId, string>> = {
  keyboard: 'Type away.',
  drive: 'A hard drive, rather than a stick, may need a powered hub: the port can’t supply much.',
  dac: 'Sound out without a HAT: see the Zero audio guide.',
  hub: 'One port becomes several. A powered hub can also feed a hard drive.',
};

export function plugZero(device: ZeroDeviceId, port: ZeroPortId, otgAdapter: boolean): ZeroPlug {
  const plug = ZERO_DEVICES.find((d) => d.id === device)!.plug;
  if (port === 'hdmi') {
    if (plug === 'miniHdmi')
      return {
        outcome: 'video',
        fits: true,
        tone: 'good',
        title: 'Picture: mini-HDMI is the video port',
        body: 'The Zero’s video port is mini-HDMI. A mini-HDMI to HDMI cable or adapter goes straight to a monitor or TV.',
      };
    if (plug === 'microHdmi')
      return {
        outcome: 'wrongShape',
        fits: false,
        tone: 'bad',
        title: 'Doesn’t fit: that’s micro-HDMI',
        body: 'A Pi 4 uses micro-HDMI, a size smaller. The Zero needs a mini-HDMI cable or adapter.',
      };
    return { outcome: 'wrongShape', fits: false, tone: 'bad', title: 'Doesn’t fit', body: 'Only a mini-HDMI plug goes in the video port.' };
  }
  if (plug === 'miniHdmi' || plug === 'microHdmi')
    return {
      outcome: 'wrongShape',
      fits: false,
      tone: 'bad',
      title: 'Doesn’t fit',
      body: 'That’s a video plug; the micro-USB ports take power and USB devices.',
    };
  if (plug === 'usbA' && !otgAdapter)
    return {
      outcome: 'noAdapter',
      fits: false,
      tone: 'bad',
      title: 'Doesn’t fit: you need an OTG adapter',
      body: 'A full-size USB plug doesn’t go into micro-USB. Use a micro-USB OTG adapter (micro-USB plug to a USB-A socket) or a hub with a micro-USB cable, in the port marked USB.',
    };
  if (plug === 'microUsb') {
    if (port === 'pwr')
      return {
        outcome: 'powered',
        fits: true,
        tone: 'good',
        title: 'Powered: the right port',
        body: 'PWR IN takes power only. Use a 5.1 V, 2.5 A supply; see the under-voltage guide.',
      };
    return {
      outcome: 'poweredDataPort',
      fits: true,
      tone: 'meh',
      title: 'It boots, but the data port is used up',
      body: 'The data port can power the Zero too, but then there is nowhere to plug a keyboard or drive. Power goes in PWR IN.',
    };
  }
  if (port === 'pwr')
    return {
      outcome: 'noData',
      fits: true,
      tone: 'bad',
      title: 'Nothing happens: PWR IN has no data',
      body: 'The plug fits, but PWR IN carries power only. The keyboard stays dark, the drive never appears. Move it to the port marked USB.',
    };
  return {
    outcome: 'works',
    fits: true,
    tone: 'good',
    title: 'Works: the data port',
    body: `The port marked USB is the Zero’s only data port. ${WORKS_TAIL[device] ?? ''}`.trim(),
  };
}

// ---------------------------------------------------------------------------
// 2. Pi 4 micro-HDMI.

export type HdmiPortId = 'hdmi0' | 'hdmi1';
export const HDMI_PORTS: { id: HdmiPortId; label: string; kms: string }[] = [
  { id: 'hdmi0', label: 'HDMI0 (next to the USB-C power)', kms: 'HDMI-A-1' },
  { id: 'hdmi1', label: 'HDMI1', kms: 'HDMI-A-2' },
];
export type ScreenOnId = 'before' | 'after';
export const SCREEN_ON: { id: ScreenOnId; label: string }[] = [
  { id: 'before', label: 'Before the Pi boots' },
  { id: 'after', label: 'After the Pi boots' },
];
export type HdmiCableId = 'microCable' | 'microAdapter' | 'miniCable' | 'cheapCable' | 'vga';
export const HDMI_CABLES: { id: HdmiCableId; label: string }[] = [
  { id: 'microCable', label: 'Micro-HDMI to HDMI cable' },
  { id: 'microAdapter', label: 'Micro-HDMI adapter + HDMI cable' },
  { id: 'miniCable', label: 'Mini-HDMI cable (the Zero’s)' },
  { id: 'cheapCable', label: 'Very cheap or worn cable' },
  { id: 'vga', label: 'HDMI to VGA adapter' },
];
export type Picture = 'yes' | 'maybe' | 'no';
export interface HdmiInput {
  port: HdmiPortId;
  screenOn: ScreenOnId;
  cable: HdmiCableId;
}
export interface HdmiResult {
  picture: Picture;
  title: string;
  body: string;
  tone: Tone;
  fixLine: string | null;
}

export function forceModeLine(port: HdmiPortId): string {
  return `video=${HDMI_PORTS.find((p) => p.id === port)!.kms}:1280x720@60D`;
}

export function hdmiOutcome(i: HdmiInput): HdmiResult {
  if (i.cable === 'miniCable')
    return {
      picture: 'no',
      tone: 'bad',
      title: 'Doesn’t fit: that’s mini-HDMI',
      fixLine: null,
      body: 'Mini-HDMI is the Zero’s port, a size bigger than the Pi 4’s micro-HDMI. Use a micro-HDMI cable or adapter.',
    };
  if (i.cable === 'cheapCable')
    return {
      picture: 'no',
      tone: 'bad',
      title: 'No picture: the cable',
      fixLine: forceModeLine(i.port),
      body: 'Some very cheap cables leave out the hotplug wire, so the Pi never learns a screen is there. Try a better cable; or force the output on with this line at the end of cmdline.txt.',
    };
  if (i.cable === 'vga')
    return {
      picture: 'maybe',
      tone: 'meh',
      title: 'Maybe: VGA needs a converter',
      fixLine: forceModeLine(i.port),
      body: 'VGA is analogue, so the adapter has to convert the signal, and the Pi often can’t read the screen’s details through it. If it stays blank, force a mode.',
    };
  if (i.screenOn === 'after')
    return {
      picture: 'maybe',
      tone: 'meh',
      title: 'Usually fine, sometimes blank',
      fixLine: forceModeLine(i.port),
      body: 'Most screens are noticed when they come on. A few don’t answer while they are off, so the Pi picks no mode and stays blank. Turn the screen on first, or force a mode.',
    };
  if (i.port === 'hdmi1')
    return {
      picture: 'yes',
      tone: 'meh',
      title: 'Picture, but use HDMI0 for one screen',
      fixLine: null,
      body: 'HDMI0, next to the USB-C power socket, is the first output. HDMI1 normally works too; if one port gives nothing, try the other.',
    };
  return {
    picture: 'yes',
    tone: 'good',
    title: 'Picture',
    fixLine: null,
    body: 'HDMI0, the screen on before boot, a proper cable: the setup that just works.',
  };
}

// ---------------------------------------------------------------------------
// 3. Pi 4 heat throttling. An illustrative first-order thermal model.

export const AMBIENT_C = 25;
/** Arm cores throttled progressively from here (Raspberry Pi docs). */
export const SOFT_C = 80;
/** Arm cores and GPU throttled (Raspberry Pi docs). */
export const HARD_C = 85;
/** Model only: the clock bottoms out here. */
export const FLOOR_C = 90;
export const MAX_MHZ = 1500;
/** Model only: the clock at 85 °C. */
export const SOFT_MIN_MHZ = 1000;
/** Model only. */
export const MIN_MHZ = 600;
/** Model only. */
export const IDLE_W = 2.7;
/** Model only. */
export const FULL_W = 6.4;
export const DURATION_S = 600;
/** Capped + throttled, now and since boot. */
export const HOT_THROTTLED = 0x60006;

export type HeatLoadId = 'idle' | 'music' | 'rgb' | 'compile';
export const HEAT_LOADS: { id: HeatLoadId; label: string; fraction: number }[] = [
  { id: 'idle', label: 'Idle', fraction: 0 },
  { id: 'music', label: 'Playing music or a small web server', fraction: 0.25 },
  { id: 'rgb', label: 'Driving an RGB LED panel', fraction: 0.6 },
  { id: 'compile', label: 'Compiling code on all four cores', fraction: 1 },
];
export type CoolingId = 'closedBox' | 'bare' | 'heatsink' | 'fan';
export const COOLING: { id: CoolingId; label: string; cPerW: number; tauS: number }[] = [
  { id: 'closedBox', label: 'Bare board in a closed box', cPerW: 13, tauS: 120 },
  { id: 'bare', label: 'Bare board, open air', cPerW: 9, tauS: 90 },
  { id: 'heatsink', label: 'Heatsinks, open air or a vented case', cPerW: 7, tauS: 150 },
  { id: 'fan', label: 'Heatsink and fan', cPerW: 3, tauS: 60 },
];

export function clockMhz(tempC: number): number {
  if (tempC < SOFT_C) return MAX_MHZ;
  if (tempC < HARD_C) return MAX_MHZ - ((tempC - SOFT_C) / (HARD_C - SOFT_C)) * (MAX_MHZ - SOFT_MIN_MHZ);
  if (tempC < FLOOR_C) return SOFT_MIN_MHZ - ((tempC - HARD_C) / (FLOOR_C - HARD_C)) * (SOFT_MIN_MHZ - MIN_MHZ);
  return MIN_MHZ;
}

export function powerW(load: HeatLoadId, mhz: number): number {
  const fraction = HEAT_LOADS.find((l) => l.id === load)!.fraction;
  return IDLE_W + fraction * (FULL_W - IDLE_W) * (mhz / MAX_MHZ);
}

export interface HeatSample {
  t: number;
  tempC: number;
  mhz: number;
}
export type HeatStatus = 'ok' | 'warm' | 'soft' | 'hard';
export const HEAT_TITLES: Record<HeatStatus, string> = {
  ok: 'Full speed, plenty of room',
  warm: 'Warm, still full speed',
  soft: 'Throttling: the Pi slows itself down',
  hard: 'Hot: Arm and GPU both throttled',
};
export interface HeatRun {
  samples: HeatSample[];
  startC: number;
  endC: number;
  peakC: number;
  endMhz: number;
  minMhz: number;
  softAtS: number | null;
  hardAtS: number | null;
  status: HeatStatus;
  throttled: number;
}

export function heatRun(load: HeatLoadId, cooling: CoolingId): HeatRun {
  const c = COOLING.find((x) => x.id === cooling)!;
  let T = AMBIENT_C + c.cPerW * IDLE_W;
  const samples: HeatSample[] = [];
  for (let t = 0; t <= DURATION_S; t++) {
    const mhz = clockMhz(T);
    samples.push({ t, tempC: T, mhz });
    T += (AMBIENT_C + c.cPerW * powerW(load, mhz) - T) / c.tauS;
  }
  const last = samples[DURATION_S];
  const endC = last.tempC;
  const status: HeatStatus = endC < 70 ? 'ok' : endC < SOFT_C ? 'warm' : endC < HARD_C ? 'soft' : 'hard';
  return {
    samples,
    startC: samples[0].tempC,
    endC,
    peakC: Math.max(...samples.map((s) => s.tempC)),
    endMhz: Math.round(last.mhz),
    minMhz: Math.round(Math.min(...samples.map((s) => s.mhz))),
    softAtS: samples.find((s) => s.tempC >= SOFT_C)?.t ?? null,
    hardAtS: samples.find((s) => s.tempC >= HARD_C)?.t ?? null,
    status,
    throttled: status === 'soft' || status === 'hard' ? HOT_THROTTLED : 0,
  };
}

/** 64 → '1 min 4 s'; 120 → '2 min'; 45 → '45 s'. */
export function minSec(s: number): string {
  const m = Math.floor(s / 60);
  const r = s % 60;
  if (m === 0) return `${r} s`;
  return r === 0 ? `${m} min` : `${m} min ${r} s`;
}
