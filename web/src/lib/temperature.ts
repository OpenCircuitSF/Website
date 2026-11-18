// The one place guide temperatures are formatted. Every guide value is stored
// in °C; this module converts and prints it in the visitor's chosen unit.
//
// Rounding rule: °C is always rounded to the nearest whole degree; °F is
// rounded to the nearest `step` (default 1). Callers pass step = 10 for iron
// settings and tip temperatures (a dial value), and the default for melting
// points and junction temperatures. A stepped °F range rounds inward, so every
// setting it names lies inside the true range. No guide temperature is
// negative, so Math.round's half-up behaviour never differs from
// half-away-from-zero.

export type TempUnit = 'F' | 'C';
export const DEFAULT_TEMP_UNIT: TempUnit = 'F';
export const TEMP_UNIT_KEY = 'temp-unit';
export const TEMP_UNIT_SYMBOL: Record<TempUnit, string> = { F: '°F', C: '°C' };

export function cToF(c: number): number {
  return (c * 9) / 5 + 32;
}

export function fToC(f: number): number {
  return ((f - 32) * 5) / 9;
}

export function tempValue(c: number, unit: TempUnit, step = 1): number {
  if (unit === 'C') return Math.round(c);
  return Math.round(cToF(c) / step) * step;
}

export function formatTemp(c: number, unit: TempUnit, step = 1): string {
  return `${tempValue(c, unit, step)} ${TEMP_UNIT_SYMBOL[unit]}`;
}

/**
 * Both ends of a range in the chosen unit. A stepped °F range rounds inward
 * (low end up, high end down) so each named setting lies inside the true
 * range; if that would cross, both ends fall back to nearest rounding.
 */
export function tempRange(lo: number, hi: number, unit: TempUnit, step = 1): [number, number] {
  if (unit === 'C') return [Math.round(lo), Math.round(hi)];
  if (step > 1) {
    const a = Math.ceil(cToF(lo) / step) * step;
    const b = Math.floor(cToF(hi) / step) * step;
    if (a <= b) return [a, b];
  }
  return [tempValue(lo, unit, step), tempValue(hi, unit, step)];
}

export function formatTempRange(lo: number, hi: number, unit: TempUnit, step = 1): string {
  const [a, b] = tempRange(lo, hi, unit, step);
  if (a === b) return formatTemp(lo, unit, step);
  return `${a}–${b} ${TEMP_UNIT_SYMBOL[unit]}`;
}

const TOKEN = /\{temp:(\d+)(?:-(\d+))?(?::(\d+))?\}/g;

/** Replaces {temp:LO}, {temp:LO-HI} and either with :STEP appended. */
export function renderTemps(text: string, unit: TempUnit): string {
  return text.replace(TOKEN, (_m, lo: string, hi: string | undefined, step: string | undefined) => {
    const s = step ? Number(step) : 1;
    return hi === undefined ? formatTemp(Number(lo), unit, s) : formatTempRange(Number(lo), Number(hi), unit, s);
  });
}

export function parseTempUnit(raw: string | null | undefined): TempUnit {
  return raw === 'C' ? 'C' : 'F';
}
