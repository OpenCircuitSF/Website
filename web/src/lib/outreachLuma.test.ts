import { describe, expect, it } from 'vitest';
import { lumaByWorkshop, lumaChip, lumaDiffText, lumaEventLine, lumaStatusLine, lumaTitleNote } from './outreachLuma';
import { formatDateTime } from './admin';
import type { OutreachLuma, OutreachLumaEvent } from './types';

const ev = (over: Partial<OutreachLumaEvent> = {}): OutreachLumaEvent => ({
  workshop_id: 1, state: 'on_luma', luma_url: 'https://luma.com/a', luma_title: 'Hack Night', diffs: [], ...over,
});
const feed = (over: Partial<OutreachLuma> = {}): OutreachLuma => ({
  status: 'ok', read_at: '2026-11-07T23:41:00Z', last_good_at: '2026-11-07T23:41:00Z', events: [], leftovers: [], ...over,
});

describe('outreachLuma (#0648)', () => {
  it('labels the three states', () => {
    expect(lumaChip(ev())).toEqual({ label: 'On Luma', tone: 'success' });
    expect(lumaChip(ev({ state: 'differs' }))).toEqual({ label: 'Differs', tone: 'danger' });
    expect(lumaChip(ev({ state: 'not_on_luma' }))).toEqual({ label: 'Not on Luma', tone: 'muted' });
    expect(lumaChip(undefined)).toBeNull();
  });

  it('names each differing field with both values', () => {
    expect(lumaDiffText({ field: 'start', label: 'Start', site: '6:30 PM', luma: '6:00 PM' })).toBe('Start: site 6:30 PM, Luma 6:00 PM');
    const e = ev({ state: 'differs', diffs: [
      { field: 'start', label: 'Start', site: '6:30 PM', luma: '6:00 PM' },
      { field: 'end', label: 'End', site: '9:00 PM', luma: '8:00 PM' },
    ] });
    expect(lumaEventLine(e)).toBe('Luma: Differs · Start: site 6:30 PM, Luma 6:00 PM · End: site 9:00 PM, Luma 8:00 PM');
    expect(lumaEventLine(ev())).toBe('Luma: On Luma');
    expect(lumaEventLine(ev({ state: 'not_on_luma' }))).toBe('Luma: Not on Luma');
  });

  it('notes the Luma title only when it differs', () => {
    expect(lumaTitleNote(ev(), 'Hack Night')).toBeNull();
    expect(lumaTitleNote(ev(), ' Hack Night ')).toBeNull();
    expect(lumaTitleNote(ev({ luma_title: 'Intro to Soldering' }), 'Introduction to Soldering')).toBe('Luma title: Intro to Soldering');
    expect(lumaTitleNote(ev({ luma_title: 'hack night' }), 'Hack Night')).toBe('Luma title: hack night');
    expect(lumaTitleNote(ev({ luma_title: null }), 'Hack Night')).toBeNull();
  });

  it('words each status line', () => {
    const t = formatDateTime('2026-11-07T23:41:00Z');
    expect(lumaStatusLine(feed(), false)).toBe(`Compared with Luma's calendar, read ${t}.`);
    expect(lumaStatusLine(null, false)).toBe('Checking Luma…');
    expect(lumaStatusLine(null, true)).toBe("Couldn't check Luma.");
    expect(lumaStatusLine(feed({ status: 'off' }), false)).toBe('The Luma check is off. Set the Luma calendar under Settings, Server settings, Outreach.');
    expect(lumaStatusLine(feed({ status: 'unreachable', read_at: null }), false)).toBe(`Couldn't reach Luma. Last good read ${t}.`);
    expect(lumaStatusLine(feed({ status: 'unreachable', read_at: null, last_good_at: null }), false)).toBe("Couldn't reach Luma. No good read yet.");
    expect(lumaStatusLine(feed({ status: 'malformed', read_at: null }), false)).toBe(`Luma's calendar feed could not be read. Last good read ${t}.`);
    expect(lumaStatusLine(feed({ status: 'malformed', read_at: null, last_good_at: null }), false)).toBe("Luma's calendar feed could not be read. No good read yet.");
  });

  it('maps events by workshop id', () => {
    const m = lumaByWorkshop(feed({ events: [ev({ workshop_id: 4 }), ev({ workshop_id: 9, state: 'differs' })] }));
    expect(m.get(4)?.state).toBe('on_luma');
    expect(m.get(9)?.state).toBe('differs');
    expect(m.get(1)).toBeUndefined();
    expect(lumaByWorkshop(null).size).toBe(0);
  });

  it('never calls Luma from the browser', () => {
    const sources = import.meta.glob(['../**/*.{ts,svelte}', '!../**/*.test.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>;
    expect(Object.keys(sources).length).toBeGreaterThan(50);
    for (const [path, src] of Object.entries(sources)) {
      expect(src.includes('api.luma.com'), path).toBe(false);
      expect(src.includes('ics/get'), path).toBe(false);
    }
  });
});
