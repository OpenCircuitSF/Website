// The Outreach Luma check's copy (#0648): each event's state against the Luma
// calendar, the fields that differ, and the line that says how the read went.
// The server reads Luma's feed; nothing here calls Luma.
import { formatDateTime } from './admin';
import type { OutreachLuma, OutreachLumaDiff, OutreachLumaEvent } from './types';

/** The Luma state of each event, by workshop id. */
export function lumaByWorkshop(l: OutreachLuma | null): Map<number, OutreachLumaEvent> {
  const m = new Map<number, OutreachLumaEvent>();
  for (const ev of l?.events ?? []) m.set(ev.workshop_id, ev);
  return m;
}

/** The badge for an event's Luma state, or null when it has none. */
export function lumaChip(ev: OutreachLumaEvent | undefined | null): { label: string; tone: string } | null {
  if (!ev) return null;
  switch (ev.state) {
    case 'on_luma':
      return { label: 'On Luma', tone: 'success' };
    case 'differs':
      return { label: 'Differs', tone: 'danger' };
    case 'not_on_luma':
      return { label: 'Not on Luma', tone: 'muted' };
    default:
      return null;
  }
}

/** One differing field, with both values: "Start: site 6:30 PM, Luma 6:00 PM". */
export function lumaDiffText(d: OutreachLumaDiff): string {
  return `${d.label}: site ${d.site}, Luma ${d.luma}`;
}

/** "Luma title: …" when Luma names the event differently, else null. */
export function lumaTitleNote(ev: OutreachLumaEvent | undefined | null, siteTitle: string): string | null {
  const t = ev?.luma_title?.trim();
  if (!t || t === siteTitle.trim()) return null;
  return `Luma title: ${t}`;
}

function lastGood(l: OutreachLuma): string {
  return l.last_good_at ? `Last good read ${formatDateTime(l.last_good_at)}.` : 'No good read yet.';
}

/** The line under the list that says how the Luma read went. */
export function lumaStatusLine(l: OutreachLuma | null, failed: boolean): string {
  if (failed) return "Couldn't check Luma.";
  if (!l) return 'Checking Luma…';
  switch (l.status) {
    case 'ok':
      return `Compared with Luma's calendar, read ${l.read_at ? formatDateTime(l.read_at) : ''}.`;
    case 'off':
      return 'The Luma check is off. Set the Luma calendar under Settings, Server settings, Outreach.';
    case 'unreachable':
      return `Couldn't reach Luma. ${lastGood(l)}`;
    case 'malformed':
      return `Luma's calendar feed could not be read. ${lastGood(l)}`;
    default:
      return "Couldn't check Luma.";
  }
}

/** The event page's Luma line, without the link: "Luma: Differs · Start: …". */
export function lumaEventLine(ev: OutreachLumaEvent): string {
  const chip = lumaChip(ev);
  const parts = [`Luma: ${chip ? chip.label : ''}`];
  if (ev.state === 'differs') for (const d of ev.diffs) parts.push(lumaDiffText(d));
  return parts.join(' · ');
}

/** The link text for a Luma event page. */
export const OPEN_ON_LUMA = 'Open on Luma';
