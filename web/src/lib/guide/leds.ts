// A faithful port of the Programming LEDs firmware's render math
// (OpenCircuitSF/Workshops: workshops/programming-leds/firmware/src/main.cpp)
// so the on-page strip does exactly what the student's strip does when they
// upload the same constants. Pure functions only -- no DOM -- so it is
// unit-testable without jsdom (AGENTS.md's DOM-avoidance convention).
//
// LED colors are data here, not theme: they are the literal CRGB values the
// firmware sends down the wire, which is why they are numbers in this module
// rather than tokens in app.css.

export interface Rgb {
  r: number;
  g: number;
  b: number;
}

export interface PaletteColor {
  /** The FastLED name, as typed in main.cpp: CRGB::<name>. */
  name: string;
  rgb: Rgb;
}

/** The CRGB named colors students can pick from. Values are FastLED's own
 * (pixeltypes.h), which follow the HTML named colors -- note CRGB::Green is
 * 0x008000, half-bright, not 0x00FF00. */
export const NAMED_COLORS: readonly PaletteColor[] = [
  { name: 'Red', rgb: { r: 255, g: 0, b: 0 } },
  { name: 'Orange', rgb: { r: 255, g: 165, b: 0 } },
  { name: 'Yellow', rgb: { r: 255, g: 255, b: 0 } },
  { name: 'Green', rgb: { r: 0, g: 128, b: 0 } },
  { name: 'Lime', rgb: { r: 0, g: 255, b: 0 } },
  { name: 'Cyan', rgb: { r: 0, g: 255, b: 255 } },
  { name: 'Blue', rgb: { r: 0, g: 0, b: 255 } },
  { name: 'Purple', rgb: { r: 128, g: 0, b: 128 } },
  { name: 'Magenta', rgb: { r: 255, g: 0, b: 255 } },
  { name: 'HotPink', rgb: { r: 255, g: 105, b: 180 } },
  { name: 'White', rgb: { r: 255, g: 255, b: 255 } },
];

export function namedColor(name: string): PaletteColor {
  const found = NAMED_COLORS.find((c) => c.name === name);
  if (!found) throw new Error(`unknown CRGB color: ${name}`);
  return found;
}

/** Everything a student can change in main.cpp, plus the "stretch" edits
 * from the class plan that touch the loop body rather than a constant. */
export interface StripConfig {
  numLeds: number;
  brightness: number;
  stepMs: number;
  ledSpread: number;
  frameMs: number;
  palette: string[];
  /** FastLED.addLeds<WS2812B, DATA_PIN, GRB> -- the strip's real order is GRB. */
  colorOrder: 'GRB' | 'RGB';
  /** Stretch: `if (i % 2) leds[i] = CRGB::Black;` */
  evenOnly: boolean;
  /** Stretch: `position - i * LED_SPREAD` */
  reverse: boolean;
}

/** The kit as shipped: matches main.cpp's defaults. */
export const DEFAULT_CONFIG: StripConfig = {
  numLeds: 8,
  brightness: 50,
  stepMs: 2000,
  ledSpread: 26,
  frameMs: 16,
  palette: ['Red', 'Orange', 'Green', 'Blue', 'Magenta'],
  colorOrder: 'GRB',
  evenOnly: false,
  reverse: false,
};

/** How many LEDs are physically on the student's strip. Deliberately
 * separate from config.numLeds: the two disagreeing is an exercise. */
export const PHYSICAL_LEDS = 8;

export const UNITS_PER_STEP = 256;

/** FastLED blend(): lerp each channel by amount/256. */
export function blend(a: Rgb, b: Rgb, amount: number): Rgb {
  const lerp = (x: number, y: number) => Math.floor(x + ((y - x) * amount) / 256);
  return { r: lerp(a.r, b.r), g: lerp(a.g, b.g), b: lerp(a.b, b.b) };
}

/** main.cpp's colorAt(): upper bits pick the pair, low 8 bits how far between. */
export function colorAt(position: number, palette: readonly Rgb[]): Rgb {
  const len = palette.length;
  if (len === 0) return { r: 0, g: 0, b: 0 };
  const span = UNITS_PER_STEP * len;
  // uint16_t arithmetic in the firmware; ((x % m) + m) % m also handles the
  // negative positions the "reverse" stretch produces.
  const wrapped = (((position & 0xffff) % span) + span) % span;
  const index = Math.floor(wrapped / UNITS_PER_STEP);
  const amount = wrapped % UNITS_PER_STEP;
  return blend(palette[index], palette[(index + 1) % len], amount);
}

/** main.cpp's position: how far along the palette `elapsedMs` has carried LED 0. */
export function positionAt(elapsedMs: number, stepMs: number, paletteLen: number): number {
  if (paletteLen === 0 || stepMs <= 0) return 0;
  const cycle = paletteLen * stepMs;
  const elapsed = elapsedMs % cycle;
  return Math.floor((elapsed * UNITS_PER_STEP) / stepMs);
}

/** Which two palette entries LED 0 is between, and how far -- for the
 * "how it works" readout. */
export function describePosition(position: number, palette: readonly string[]) {
  const len = palette.length;
  if (len === 0) return { from: '', to: '', amount: 0 };
  const wrapped = position % (UNITS_PER_STEP * len);
  const index = Math.floor(wrapped / UNITS_PER_STEP);
  return { from: palette[index], to: palette[(index + 1) % len], amount: wrapped % UNITS_PER_STEP };
}

/** One rendered frame: the colors the code puts in leds[0..numLeds). */
export function renderFrame(elapsedMs: number, config: StripConfig): Rgb[] {
  const palette = config.palette.map((n) => namedColor(n).rgb);
  // The render tick: the strip only changes every FRAME_MS, so a large
  // FRAME_MS looks chunky without being slower.
  const frameMs = Math.max(1, config.frameMs);
  const tick = Math.floor(elapsedMs / frameMs) * frameMs;
  const position = positionAt(tick, config.stepMs, palette.length);
  const out: Rgb[] = [];
  for (let i = 0; i < config.numLeds; i++) {
    const offset = i * config.ledSpread;
    let c = colorAt(config.reverse ? position - offset : position + offset, palette);
    if (config.evenOnly && i % 2) c = { r: 0, g: 0, b: 0 };
    out.push(c);
  }
  return out;
}

// FastLED's power model (power_mgt.cpp) at 5V: milliwatts per channel at full
// value, plus a small idle draw per pixel.
const MW_RED = 16 * 5;
const MW_GREEN = 11 * 5;
const MW_BLUE = 15 * 5;
const MW_DARK = 1 * 5;
export const POWER_LIMIT_MA = 500;

/** Estimated current at full brightness (255) for a frame, in mA. */
export function frameMilliamps(frame: readonly Rgb[], brightness = 255): number {
  let mw = 0;
  for (const c of frame) {
    mw += (c.r * MW_RED + c.g * MW_GREEN + c.b * MW_BLUE) / 256 + MW_DARK;
  }
  return ((mw * brightness) / 255) / 5;
}

/** setMaxPowerInVoltsAndMilliamps(5, 500): the brightness FastLED actually
 * uses after scaling the requested one down to fit the budget. */
export function limitedBrightness(frame: readonly Rgb[], requested: number): number {
  const ma = frameMilliamps(frame, requested);
  if (ma <= POWER_LIMIT_MA) return requested;
  return Math.floor((requested * POWER_LIMIT_MA) / ma);
}

/**
 * What each PHYSICAL LED shows, given what the code sent. Models the
 * pass-along chain: the strip shows the first PHYSICAL_LEDS values sent;
 * anything past that falls off the end; too few leaves the tail dark.
 * colorOrder 'RGB' on a GRB strip swaps red and green on the wire.
 */
export function physicalStrip(sent: readonly Rgb[], config: StripConfig, physical = PHYSICAL_LEDS): (Rgb | null)[] {
  const out: (Rgb | null)[] = [];
  for (let i = 0; i < physical; i++) {
    const c = sent[i];
    if (!c) {
      out.push(null);
      continue;
    }
    out.push(config.colorOrder === 'RGB' ? { r: c.g, g: c.r, b: c.b } : c);
  }
  return out;
}

/**
 * Screen color for an LED. A real LED at BRIGHTNESS 50 is still bright to the
 * eye; a pixel at 50/255 is nearly black on a monitor. A perceptual curve
 * keeps the on-screen strip readable while still showing that lower
 * brightness is dimmer.
 */
export function displayCss(c: Rgb | null, brightness: number): string {
  if (!c) return 'transparent';
  const k = Math.pow(Math.max(0, Math.min(255, brightness)) / 255, 0.45);
  const ch = (v: number) => Math.round(v * k);
  return `rgb(${ch(c.r)}, ${ch(c.g)}, ${ch(c.b)})`;
}

export function isDark(c: Rgb | null): boolean {
  return !c || c.r + c.g + c.b === 0;
}
