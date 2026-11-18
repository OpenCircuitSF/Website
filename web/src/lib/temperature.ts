// The one place guide temperatures are formatted. Every guide value is stored
// in °C; this module converts and prints it in the visitor's chosen unit.
//
// Rounding rule: °C is always rounded to the nearest whole degree; °F is
// rounded to the nearest `step` (default 1). Callers pass step = 10 for iron
// settings and tip temperatures (a dial value), and the default for melting
// points and junction temperatures. No guide temperature is negative, so
// Math.round's half-up behaviour never differs from half-away-from-zero.

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

export function formatTempRange(lo: number, hi: number, unit: TempUnit, step = 1): string {
  const a = tempValue(lo, unit, step);
  const b = tempValue(hi, unit, step);
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
