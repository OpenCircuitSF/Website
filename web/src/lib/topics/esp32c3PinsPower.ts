// Logic for the ESP32-C3 "pins and power" guides (#0671): boot pins,
// reserved pins, analog input, PWM and power. Pin facts come from
// supermini.ts; this file only adds the rules each demo needs.
//
// Sources: ESP32-C3 Series Datasheet v2.5 (Table 3-1 strapping defaults,
// Table 3-3 boot mode control, §2.3 GPIO restrictions, Table 5-2 operating
// conditions, Table 5-6 ADC calibrated ranges, §4.2.1.7 LED PWM: six
// channels, up to 14-bit duty); arduino-esp32 3.x ADC and LEDC API docs.
import { SUPERMINI_PINS, type SuperMiniPin } from './supermini';

export const GPIO_PINS: SuperMiniPin[] = SUPERMINI_PINS.filter((p) => p.kind === 'gpio');

export function pinById(id: string): SuperMiniPin {
  const p = SUPERMINI_PINS.find((x) => x.id === id);
  if (!p) throw new Error(`no SuperMini pin ${id}`);
  return p;
}

export const isStrapping = (p: SuperMiniPin) => p.tags.includes('strapping');
export const hasAdc = (p: SuperMiniPin) => p.tags.includes('ADC');

// ---------------------------------------------------------------------------
// Boot pins: "Will it boot?"
// ---------------------------------------------------------------------------

export type BootPart = 'nothing' | 'button' | 'led' | 'pullup' | 'pulldown' | 'sensor-low';

export const BOOT_PARTS: { id: BootPart; label: string }[] = [
  { id: 'nothing', label: 'Nothing' },
  { id: 'button', label: 'Button to ground' },
  { id: 'led', label: 'LED to ground' },
  { id: 'pullup', label: '10 kΩ pull-up to 3.3 V' },
  { id: 'pulldown', label: '10 kΩ pull-down to ground' },
  { id: 'sensor-low', label: 'Sensor that idles low' },
];

/** The pins the boot demo offers: the three strapping pins and a plain one. */
export const BOOT_DEMO_PINS = ['gpio2', 'gpio8', 'gpio9', 'gpio4'] as const;
export type BootPin = (typeof BOOT_DEMO_PINS)[number];

/** The level the chip sees on the pin at the moment RESET is released. */
export type Level = 'high' | 'low' | 'floating' | 'unsure';

export function levelAtReset(pin: BootPin, part: BootPart, held: boolean): Level {
  // GPIO9 has a weak internal pull-up at reset (datasheet Table 3-1).
  // GPIO8 has no internal pull; on many SuperMinis the onboard LED runs from
  // 3.3 V to it, and in practice BOOT + RESET reaches download mode, so the
  // demo treats it as high. Not checked against a schematic (#0671).
  const idle: Level = pin === 'gpio9' || pin === 'gpio8' ? 'high' : 'floating';
  switch (part) {
    case 'nothing':
      return idle;
    case 'button':
      return held ? 'low' : idle;
    case 'pullup':
      return 'high';
    case 'pulldown':
    case 'sensor-low':
      return 'low';
    case 'led':
      // An LED to ground clamps the pin near its forward voltage (~1.8–2 V),
      // below the chip's 0.75 × VDD (≈2.5 V) "definitely high" threshold.
      return 'unsure';
  }
}

export type BootOutcome = 'runs' | 'download' | 'risky';

export interface BootResult {
  outcome: BootOutcome;
  title: string;
  body: string;
}

export function bootResult(pin: BootPin, part: BootPart, held: boolean): BootResult {
  const level = levelAtReset(pin, part, held);
  if (pin === 'gpio4') {
    return {
      outcome: 'runs',
      title: 'Your program runs',
      body: 'GPIO 4 is not a strapping pin. The chip ignores it at reset, so anything can hang off it.',
    };
  }
  if (pin === 'gpio9') {
    if (level === 'low') {
      return {
        outcome: 'download',
        title: 'Download mode: your program does not start',
        body:
          part === 'button'
            ? 'GPIO 9 was low at reset, so the chip waits for new code instead. That is exactly what the BOOT button does. Let go and press RESET to run your program.'
            : 'GPIO 9 was low at reset, so the chip waits for new code instead of running yours. It will do this on every power-up. Move this part to another pin.',
      };
    }
    if (level === 'unsure') {
      return {
        outcome: 'risky',
        title: 'Might not boot',
        body: 'The LED holds GPIO 9 around 2 V: not clearly high or low. Some boards will run, some will sit in download mode. Use another pin for the LED.',
      };
    }
    return {
      outcome: 'runs',
      title: 'Your program runs',
      body:
        part === 'button'
          ? 'The button is not pressed, so the internal pull-up keeps GPIO 9 high. A button here works like a second BOOT button: just don’t hold it while you reset.'
          : 'GPIO 9 is high at reset (its internal pull-up does this when nothing pulls it down), so the chip starts your program.',
    };
  }
  if (pin === 'gpio8') {
    if (level === 'low' || level === 'unsure') {
      return {
        outcome: 'risky',
        title: 'Runs, but uploads can break',
        body: 'Your program still starts: GPIO 8 doesn’t matter for a normal boot. But download mode needs GPIO 8 high, so holding BOOT to load new code won’t work while this is connected. GPIO 8 also drives the onboard LED.',
      };
    }
    return {
      outcome: 'runs',
      title: 'Your program runs',
      body: 'Nothing pulls GPIO 8 low at reset, so both a normal boot and download mode work. (On many SuperMinis the onboard LED runs from 3.3 V to this pin, which helps keep it high.)',
    };
  }
  // gpio2
  if (level === 'low' || level === 'unsure') {
    return {
      outcome: 'risky',
      title: 'Probably runs, but don’t rely on it',
      body: 'Espressif says GPIO 2 doesn’t pick the boot mode, but recommends keeping it high at reset because glitches there can upset booting. Move parts that pull low to another pin.',
    };
  }
  return {
    outcome: 'runs',
    title: 'Your program runs',
    body: 'GPIO 2 is high or floating at reset, which is what the chip wants. After boot it is a normal pin.',
  };
}

// ---------------------------------------------------------------------------
// Reserved pins: a pin chooser
// ---------------------------------------------------------------------------

export type Job = 'button' | 'led' | 'analog' | 'i2c' | 'strip';

export const JOBS: { id: Job; label: string }[] = [
  { id: 'button', label: 'Button' },
  { id: 'led', label: 'LED' },
  { id: 'analog', label: 'Analog sensor' },
  { id: 'i2c', label: 'I2C display or sensor' },
  { id: 'strip', label: 'LED strip data' },
];

export type Verdict = 'good' | 'care' | 'avoid';

export interface PinAdvice {
  verdict: Verdict;
  reason: string;
}

const PLAIN = 'A plain pin with no second job. Good choice.';

export function pinAdvice(job: Job, pinId: string): PinAdvice {
  const p = pinById(pinId);
  if (p.kind !== 'gpio') return { verdict: 'avoid', reason: 'This is a power pin, not a GPIO.' };
  const n = Number(pinId.replace('gpio', ''));

  if (job === 'analog') {
    if (n === 5) return { verdict: 'avoid', reason: 'GPIO 5 is on ADC2. Espressif lists ADC2 on the C3 as unreliable, newer software won’t read it by default, and WiFi interferes with it. Use 0–4.' };
    if (!hasAdc(p)) return { verdict: 'avoid', reason: `GPIO ${n} has no ADC, so analogRead() can’t measure it. Use 0–4.` };
    if (n === 2) return { verdict: 'care', reason: 'ADC1 works here, but GPIO 2 is a strapping pin. A sensor that outputs near 0 V at power-up holds it low. Prefer 0, 1, 3 or 4.' };
    return { verdict: 'good', reason: `GPIO ${n} is on ADC1, the calibrated ADC. Good choice.` };
  }

  if (n === 9) {
    if (job === 'button') return { verdict: 'care', reason: 'Works: the BOOT button is already wired here. But holding your button during reset starts download mode.' };
    if (job === 'led') return { verdict: 'avoid', reason: 'An LED to ground can hold GPIO 9 low enough at reset to stop your program from starting.' };
    return { verdict: 'care', reason: 'Works once running, but pressing the BOOT button pulls this pin low and the chip reads it at every reset. Keep it free.' };
  }
  if (n === 8) {
    if (job === 'button') return { verdict: 'avoid', reason: 'A strapping pin with the onboard LED on it. Every press also lights the LED, and holding the button at reset breaks download mode.' };
    if (job === 'led') return { verdict: 'care', reason: 'The onboard blue LED is already here, wired to light when the pin is LOW. Use it, or pick another pin for your own LED.' };
    return { verdict: 'care', reason: 'Strapping pin with the onboard LED on it: the LED flickers along with your data. Pick a plain pin if you can.' };
  }
  if (n === 2) {
    if (job === 'button' || job === 'led') return { verdict: 'avoid', reason: 'A strapping pin that should be high at reset. A button or LED to ground can pull it low. Use a plain pin.' };
    return { verdict: 'care', reason: 'Works: this doesn’t pull the pin low. But keep strapping pins for last.' };
  }
  if (n === 20 || n === 21) {
    const which = n === 20 ? 'RX' : 'TX';
    if (n === 21 && (job === 'led' || job === 'strip'))
      return { verdict: 'care', reason: 'This is UART0 TX: the chip prints boot messages here at every reset, so an LED or strip on it flickers at startup.' };
    return { verdict: 'care', reason: `This is UART0 ${which}. Free to use when you don’t need that serial port (the USB-C port has its own), but plain pins come first.` };
  }
  if (n >= 4 && n <= 7) {
    return { verdict: 'good', reason: `${PLAIN} (It can also be a JTAG debugging pin, but only if you set that up.)` };
  }
  return { verdict: 'good', reason: PLAIN };
}

/** Pins not on the SuperMini's headers, and why. */
export const HIDDEN_PINS = [
  { pins: 'GPIO 11–17', why: 'Wired to the flash chip that holds your program. Not broken out. Never use them.' },
  { pins: 'GPIO 18, 19', why: 'USB D− and D+. The USB-C port needs them for uploads and Serial. Reusing them kills USB.' },
];

// ---------------------------------------------------------------------------
// Analog input
// ---------------------------------------------------------------------------

/** Calibrated range at the default (11/12 dB) attenuation: 0–2500 mV (Table 5-6). */
export const ADC_MAX_MV = 2500;
export const ADC_MAX_RAW = 4095;
export const GPIO_MAX_V = 3.3;

/** Small deterministic noise source, so tests and the demo are repeatable. */
export function noise(seed: number): number {
  let x = (seed * 1103515245 + 12345) >>> 0;
  x ^= x >>> 13;
  x = Math.imul(x, 0x5bd1e995) >>> 0;
  x ^= x >>> 15;
  return (x % 2001) / 1000 - 1; // -1..1
}

/** One noisy raw reading for an input voltage. Flattens near 0 and past ~2.5 V. */
export function rawReading(volts: number, seed: number): number {
  const mv = Math.max(0, volts * 1000);
  const ideal = (Math.min(mv, ADC_MAX_MV + 300) / ADC_MAX_MV) * ADC_MAX_RAW * 0.94;
  // Squash the ends: the bottom few tens of mV read 0, the top saturates.
  const curved = mv < 40 ? 0 : ideal;
  const jitter = noise(seed) * 30;
  return Math.round(Math.min(ADC_MAX_RAW, Math.max(0, curved + jitter)));
}

export function averageReading(volts: number, seed: number, n = 16): number {
  let sum = 0;
  for (let i = 0; i < n; i++) sum += rawReading(volts, seed * 97 + i);
  return Math.round(sum / n);
}

/** What analogReadMilliVolts() reports: calibrated, but only good to ~2.5 V. */
export function milliVolts(volts: number, seed: number): number {
  const mv = Math.max(0, volts * 1000);
  const clipped = Math.min(mv, ADC_MAX_MV + 300);
  return Math.max(0, Math.round(clipped + noise(seed + 7) * 15));
}

export type AnalogZone = 'ok' | 'top' | 'over' | 'danger';

export function analogZone(volts: number): AnalogZone {
  if (volts > GPIO_MAX_V + 0.05) return 'danger';
  if (volts > 3.25) return 'over';
  if (volts * 1000 > ADC_MAX_MV) return 'top';
  return 'ok';
}

export interface Divider {
  vout: number;
  ok: boolean;
  message: string;
}

/** Vout = Vin × R2 / (R1 + R2), R1 on top (to Vin), R2 to ground. */
export function divider(vin: number, r1k: number, r2k: number): Divider {
  const vout = (vin * r2k) / (r1k + r2k);
  const rounded = Math.round(vout * 100) / 100;
  if (vout > GPIO_MAX_V) return { vout: rounded, ok: false, message: 'Too high: more than 3.3 V on a GPIO can damage it.' };
  if (vout * 1000 > ADC_MAX_MV) return { vout: rounded, ok: false, message: 'Safe for the pin, but above about 2.5 V the ADC can’t read it accurately. Make R2 smaller or R1 bigger.' };
  return { vout: rounded, ok: true, message: `Good: reads in range. Multiply the reading by ${Math.round(((r1k + r2k) / r2k) * 100) / 100} to get the real voltage.` };
}

// ---------------------------------------------------------------------------
// PWM
// ---------------------------------------------------------------------------

/** LEDC counts on an 80 MHz clock, so frequency × 2^bits must fit in it. */
export const LEDC_CLOCK_HZ = 80_000_000;
export const LEDC_MAX_BITS = 14;
export const LEDC_CHANNELS = 6;

export function maxResolutionBits(freqHz: number): number {
  return Math.min(LEDC_MAX_BITS, Math.floor(Math.log2(LEDC_CLOCK_HZ / freqHz)));
}

export function dutyValue(dutyPct: number, bits: number): number {
  return Math.round((dutyPct / 100) * (2 ** bits - 1));
}

/** Rough perceived brightness: eyes respond to light about like L^(1/2.2). */
export function perceived(dutyPct: number): number {
  return Math.round(100 * Math.pow(dutyPct / 100, 1 / 2.2));
}

/** Gamma correction: the duty needed to look `lookPct` bright. */
export function gammaDuty(lookPct: number): number {
  return Math.round(1000 * Math.pow(lookPct / 100, 2.2)) / 10;
}

export function periodLabel(freqHz: number): string {
  const ms = 1000 / freqHz;
  return ms >= 1 ? `${Math.round(ms * 10) / 10} ms` : `${Math.round(ms * 1000)} µs`;
}

// ---------------------------------------------------------------------------
// Power
// ---------------------------------------------------------------------------

export type Source = 'usb-charger' | 'aa3' | 'aa4' | 'lipo' | 'nine' | 'reg33' | 'aa2';
export type Entry = 'usb-c' | '5v' | '3v3';

export const SOURCES: { id: Source; label: string; volts: string }[] = [
  { id: 'usb-charger', label: 'USB charger or power bank', volts: '5 V' },
  { id: 'aa3', label: '3 × AA', volts: '≈4.5 V' },
  { id: 'aa4', label: '4 × AA', volts: '≈6 V' },
  { id: 'lipo', label: 'One LiPo cell', volts: '3.7–4.2 V' },
  { id: 'nine', label: '9 V battery', volts: '9 V' },
  { id: 'reg33', label: 'Regulated 3.3 V supply', volts: '3.3 V' },
  { id: 'aa2', label: '2 × AA', volts: '≈3 V' },
];

export const ENTRIES: { id: Entry; label: string }[] = [
  { id: 'usb-c', label: 'USB-C port' },
  { id: '5v', label: '5V pin' },
  { id: '3v3', label: '3.3 pin' },
];

export type PowerVerdict = 'safe' | 'works' | 'wont' | 'damage';

export interface PowerResult {
  verdict: PowerVerdict;
  title: string;
  body: string;
}

export function powerResult(src: Source, entry: Entry, usbToo: boolean): PowerResult {
  if (entry === 'usb-c') {
    if (src !== 'usb-charger')
      return { verdict: 'wont', title: 'Doesn’t plug in', body: 'Batteries and bench supplies don’t have a USB-C plug. Wire them to the 5V or 3.3 pin instead.' };
    return { verdict: 'safe', title: 'Safe', body: 'The easiest way: any USB charger or power bank. Some power banks switch off when the draw is very small; if yours does, pick one with an “always on” or low-current mode.' };
  }
  if (usbToo && src !== 'usb-charger') {
    return {
      verdict: 'damage',
      title: 'Don’t connect both',
      body:
        entry === '5v'
          ? 'With USB plugged in, the 5V pin is already connected to your computer’s USB power. Many SuperMinis have no diode between them, so two supplies fight and current can flow back into the laptop. Check your board; safest is never both at once.'
          : 'With USB plugged in, the board’s regulator is already driving the 3.3 pin. Two supplies on one rail fight each other. Unplug one.',
    };
  }
  if (entry === '5v') {
    switch (src) {
      case 'usb-charger':
        return usbToo
          ? { verdict: 'damage', title: 'Don’t connect both', body: 'Two 5 V supplies at once, with USB plugged in too. Many SuperMinis have no diode to keep them apart, so check your board; safest is never both.' }
          : { verdict: 'safe', title: 'Safe', body: '5 V into the 5V pin, through the onboard regulator down to 3.3 V. Exactly what the pin is for.' };
      case 'aa3':
        return { verdict: 'safe', title: 'Safe', body: 'About 4.5 V (fresh alkalines up to ~4.8 V) is plenty for the 3.3 V regulator. Grounds go together: battery minus to G.' };
      case 'aa4':
        return { verdict: 'works', title: 'Works, but near the limit', body: 'Four fresh alkalines can read 6.4 V, at or past what the small regulator is rated for. Rechargeable NiMH cells (4 × 1.2 V = 4.8 V) are the safer choice.' };
      case 'lipo':
        return { verdict: 'works', title: 'Works, until it runs low', body: 'A full LiPo is 4.2 V, enough for the regulator to make 3.3 V. As it drains toward 3.7 V and below, the regulator can’t keep up and the board may reset. Good for short projects.' };
      case 'nine':
        return { verdict: 'damage', title: 'Can damage the board', body: '9 V is far more than the small onboard regulator can take. Use a 5 V buck converter in between.' };
      case 'reg33':
        return { verdict: 'wont', title: 'Too low for this pin', body: 'The regulator needs a little more than 3.3 V in to make 3.3 V out, so the chip gets less than it should and may reset or not start. Feed a 3.3 V supply into the 3.3 pin instead.' };
      case 'aa2':
        return { verdict: 'wont', title: 'Too low', body: 'About 3 V into the regulator comes out below what the chip needs. The board may not start at all.' };
    }
  }
  // 3v3 pin: bypasses the regulator, chip limit 3.6 V.
  switch (src) {
    case 'reg33':
      return { verdict: 'safe', title: 'Safe', body: 'Exactly 3.3 V straight onto the chip’s rail. This skips the regulator, so the supply must be steady and never above 3.6 V.' };
    case 'aa2':
      return { verdict: 'works', title: 'Works, briefly', body: 'Fresh cells give about 3.2 V, inside the chip’s 3.0–3.6 V range. As they drain below 3 V the board starts resetting.' };
    case 'lipo':
      return { verdict: 'damage', title: 'Can damage the chip', body: 'A LiPo is up to 4.2 V, above the chip’s 3.6 V maximum, and nothing on this pin brings it down. Use the 5V pin, or add a 3.3 V regulator.' };
    default:
      return { verdict: 'damage', title: 'Can damage the chip', body: 'The 3.3 pin goes straight to the chip, which can take no more than 3.6 V. Never put 5 V or more here; use the 5V pin.' };
  }
}
