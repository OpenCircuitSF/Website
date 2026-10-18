// Pacific time for the admin (#0585, review findings K1, K2, E6, I6).
//
// Workshops happen in San Francisco, and the post text, the flyer and the Go
// side all say Pacific. The admin used to render in the browser's zone and
// convert datetime-local inputs through getTimezoneOffset(), so a 6:30 PM
// Oct 31 workshop read "Nov 1, 1:30 AM" to anyone not in Pacific. Everything
// here is explicit about the zone and independent of the machine's TZ.

export const PACIFIC_ZONE = 'America/Los_Angeles';

const PARTS_FMT = new Intl.DateTimeFormat('en-US', {
  timeZone: PACIFIC_ZONE,
  hourCycle: 'h23',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: '2-digit',
  minute: '2-digit',
});

interface WallClock {
  y: number;
  mo: number;
  d: number;
  h: number;
  mi: number;
}

function wallClock(ms: number): WallClock {
  const out: Record<string, number> = {};
  for (const p of PARTS_FMT.formatToParts(new Date(ms))) {
    if (p.type !== 'literal') out[p.type] = Number(p.value);
  }
  return { y: out.year, mo: out.month, d: out.day, h: out.hour % 24, mi: out.minute };
}

const pad2 = (n: number): string => String(n).padStart(2, '0');

function inputString(w: WallClock): string {
  return `${w.y}-${pad2(w.mo)}-${pad2(w.d)}T${pad2(w.h)}:${pad2(w.mi)}`;
}

/** An ISO instant as a datetime-local value ("YYYY-MM-DDTHH:mm") in Pacific time, or '' when unparseable. */
export function toPacificInput(iso: string | null | undefined): string {
  if (!iso) return '';
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  return inputString(wallClock(t));
}

/**
 * A Pacific wall-clock datetime-local value as an RFC 3339 UTC instant, or
 * null for an unparseable value.
 *
 * Daylight-saving edges: a wall time that occurs twice (the fall-back hour,
 * 2026-11-01 01:00-01:59) resolves to the FIRST occurrence, the daylight one
 * (UTC-7); a wall time that does not exist (the spring-forward gap, e.g.
 * 2026-03-08 02:30) rolls forward by the gap, to 03:30 daylight time.
 */
export function fromPacificInput(value: string): string | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(value.trim());
  if (!m) return null;
  const wall = Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4]), Number(m[5]));
  if (Number.isNaN(wall)) return null;
  const want = `${m[1]}-${m[2]}-${m[3]}T${m[4]}:${m[5]}`;
  const HOUR = 3600000;
  // Daylight (UTC-7) first, so an ambiguous time takes its first occurrence.
  for (const offsetHours of [7, 8]) {
    const utc = wall + offsetHours * HOUR;
    if (toPacificInput(new Date(utc).toISOString()) === want) {
      return new Date(utc).toISOString().replace(/\.\d{3}Z$/, 'Z');
    }
  }
  // In the gap: neither offset reproduces the wall time. Standard-offset
  // arithmetic lands an hour later on the wall clock, i.e. rolls forward.
  return new Date(wall + 8 * HOUR).toISOString().replace(/\.\d{3}Z$/, 'Z');
}

/** "Oct 31, 2026, 6:30 PM PT", or the raw string when it does not parse. */
export function formatPacificDateTime(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  return `${new Date(t).toLocaleString('en-US', {
    timeZone: PACIFIC_ZONE,
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  })} PT`;
}

/** "Sat, Oct 31, 6:30 PM PT", or the raw string when it does not parse. */
export function formatPacificWeekdayTime(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return iso;
  return `${new Date(t).toLocaleString('en-US', {
    timeZone: PACIFIC_ZONE,
    weekday: 'short',
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  })} PT`;
}
